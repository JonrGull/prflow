package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/git"
	"github.com/JonrGull/prflow/internal/models"
	"github.com/JonrGull/prflow/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
)

// First-run setup.
//
// Previously the app wrote a default config pointing at a guessed directory
// whether or not that existed, then dropped you on an empty repo list with no
// explanation. Every failure mode looked the same: a wrong path, a glob that
// matched nothing, and a genuinely empty directory were indistinguishable.
//
// This screen asks for the directory and then *shows what it found* before
// anything is written, so a wrong answer is visible immediately rather than
// three screens later.

// firstRunState is the first-launch setup screen.
type firstRunState struct {
	value    string // the directory being typed
	scanning bool
	preview  firstRunPreview
}

// firstRunPreview holds the outcome of scanning a candidate directory.
type firstRunPreview struct {
	Path  string
	Repos []models.RepoInfo
	Err   error
	Ran   bool

	// Globs is set when the configured globs found nothing and the scan fell
	// back to globs read off the directory's layout. Accepting saves them.
	Globs []config.GlobEntry
}

// checkFirstRunPathCmd scans a candidate directory. It runs as a command
// because globbing a large tree — especially over a network mount — can take a
// moment, and blocking the UI on filesystem work is what the rest of this
// session was spent removing.
//
// It tries the configured globs first. Those are frontend/* and backend/* on a
// first run, so on their own a plain ~/Projects/<repo> layout found nothing and
// the screen could never be accepted.
func checkFirstRunPathCmd(cfg *config.Config, path string) tea.Cmd {
	return func() tea.Msg {
		dir := config.ExpandTilde(path)
		repos, err := git.FindRepos(dir, cfg.GlobEntries(), cfg.ExplicitRepos())

		var detected []config.GlobEntry
		if err == nil && len(repos) == 0 {
			if detected = detectGlobs(dir); len(detected) > 0 {
				globs := make([]models.GlobEntry, len(detected))
				for i, g := range detected {
					globs[i] = models.GlobEntry{Pattern: g.Pattern, Group: g.Group}
				}
				repos, err = git.FindRepos(dir, globs, cfg.ExplicitRepos())
			}
		}

		return firstRunPreviewResult{preview: firstRunPreview{
			Path:  path,
			Repos: repos,
			Err:   err,
			Ran:   true,
			Globs: detected,
		}}
	}
}

// detectGlobs describes where dir keeps its repos: "*" for repos directly in
// it, and "<folder>/*" for each folder that holds repos one level down, grouped
// by the folder's name — the same shape as the frontend/* default.
func detectGlobs(dir string) []config.GlobEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var globs []config.GlobEntry
	direct := false
	for _, e := range entries {
		name := e.Name()
		child := filepath.Join(dir, name)
		// Skip hidden folders, and names a glob would read as a pattern.
		if !isDir(child) || strings.HasPrefix(name, ".") || strings.ContainsAny(name, `*?[\`) {
			continue
		}
		if git.IsGitRepo(child) {
			direct = true
			continue
		}
		if holdsRepos(child) {
			first, size := utf8.DecodeRuneInString(name)
			group := string(unicode.ToUpper(first)) + name[size:]
			globs = append(globs, config.GlobEntry{Pattern: name + "/*", Group: group})
		}
	}

	if direct {
		globs = append([]config.GlobEntry{{Pattern: "*", Group: "Repos"}}, globs...)
	}
	return globs
}

// holdsRepos reports whether any folder directly inside dir is a git repo.
func holdsRepos(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if child := filepath.Join(dir, e.Name()); isDir(child) && git.IsGitRepo(child) {
			return true
		}
	}
	return false
}

// isDir follows symlinks, as FindRepos does, so detection and discovery agree.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// columnsFor puts the first detected group on the left and the rest on the
// right, replacing the Frontend/Backend defaults, which would otherwise be
// flagged as columns naming groups nothing produces.
func columnsFor(globs []config.GlobEntry) config.ColumnsConfig {
	cols := config.ColumnsConfig{Right: []string{}}
	for i, g := range globs {
		if i == 0 {
			cols.Left = []string{g.Group}
		} else {
			cols.Right = append(cols.Right, g.Group)
		}
	}
	return cols
}

type firstRunPreviewResult struct {
	preview firstRunPreview
}

// handleFirstRunKey drives the setup screen: type a path, Enter to scan it,
// Enter again to accept once it has found something.
func (m Model) handleFirstRunKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		path := strings.TrimSpace(m.firstRun.value)

		// Second Enter on a successful scan accepts it.
		if m.firstRun.preview.Ran && m.firstRun.preview.Path == path && len(m.firstRun.preview.Repos) > 0 {
			m.config.Paths.ReposDir = path
			if globs := m.firstRun.preview.Globs; len(globs) > 0 {
				m.config.Globs = globs
				m.config.Columns = columnsFor(globs)
			}
			// A dry run deliberately does not write, so it must not trap the
			// user here — the setting holds for the rest of the session.
			if err := m.saveConfig(); err != nil && !errors.Is(err, errDryRun) {
				// Staying put beats dropping the user on a main menu that will
				// be empty again next launch because nothing was written.
				m.firstRun.preview.Err = err
				return m, nil
			}
			m.configDiagnostics = m.config.Validate()
			invalidateRepoCache()
			m.screen = ScreenMainMenu
			m.menuIndex = 0
			return m, nil
		}

		if path == "" {
			return m, nil
		}
		m.firstRun.scanning = true
		return m, checkFirstRunPathCmd(m.config, path)

	case tea.KeyBackspace:
		m.firstRun.value = trimLastRune(m.firstRun.value)
		m.firstRun.preview = firstRunPreview{}
	case tea.KeySpace:
		m.firstRun.value += " "
		m.firstRun.preview = firstRunPreview{}
	case tea.KeyRunes:
		m.firstRun.value += typedText(msg.Runes)
		m.firstRun.preview = firstRunPreview{}
	case tea.KeyEsc:
		// Skip setup. The config is still written so the app does not ask
		// again on every launch; the settings screen can fix it later.
		// Best-effort: the user asked to leave, and saveConfig has already put
		// any failure in the status bar.
		_ = m.saveConfig()
		m.screen = ScreenMainMenu
		m.menuIndex = 0
	}
	return m, nil
}

func (m Model) handleFirstRunPreview(msg firstRunPreviewResult) (tea.Model, tea.Cmd) {
	m.firstRun.scanning = false
	m.firstRun.preview = msg.preview
	return m, nil
}

func (m Model) renderFirstRun() string {
	lines := []string{
		"",
		ui.White.Render("  Point prflow at the directory that holds your repositories."),
		ui.Dim.Render("  It will look for git repos inside it, one or two levels deep."),
		"",
		fmt.Sprintf("  %s %s%s",
			ui.Green.Render("Directory:"),
			ui.WhiteBold.Render(m.firstRun.value),
			ui.Cyan.Render("█")),
		"",
	}

	switch {
	case m.firstRun.scanning:
		lines = append(lines, fmt.Sprintf("  %s Looking...", ui.Cyan.Render(ui.Spinner(m.spinnerFrame))))

	case !m.firstRun.preview.Ran:
		lines = append(lines, ui.Dim.Render("  Press Enter to see what it finds."))

	case m.firstRun.preview.Err != nil:
		lines = append(lines,
			ui.Red.Render("  ✗ "+m.firstRun.preview.Err.Error()),
			"",
			ui.Dim.Render("  Edit the path and press Enter to try again."))

	case len(m.firstRun.preview.Repos) == 0:
		lines = append(lines,
			ui.Yellow.Render("  ⚠ No git repositories found there."),
			ui.Dim.Render("  Check the path, or point at the directory one level up."),
			"",
			ui.Dim.Render("  Edit the path and press Enter to try again."))

	default:
		lines = append(lines, m.renderFirstRunFound()...)
	}

	return panel(ui.CyanBold, "Welcome", lines)
}

// renderFirstRunFound lists what the scan turned up, grouped, so the user can
// see the grouping is right before committing to it.
func (m Model) renderFirstRunFound() []string {
	repos := m.firstRun.preview.Repos

	byGroup := map[string][]string{}
	for _, r := range repos {
		byGroup[r.Group] = append(byGroup[r.Group], r.ShortName())
	}
	groups := make([]string, 0, len(byGroup))
	for g := range byGroup {
		groups = append(groups, g)
	}
	sort.Strings(groups)

	lines := []string{
		ui.Green.Render(fmt.Sprintf("  ✓ Found %d repositor%s", len(repos), plural(len(repos)))),
		"",
	}

	const maxPerGroup = 6
	for _, g := range groups {
		names := byGroup[g]
		label := g
		if label == "" {
			label = "(ungrouped)"
		}
		lines = append(lines, fmt.Sprintf("    %s", ui.YellowBold.Render(label)))

		shown := names
		if len(shown) > maxPerGroup {
			shown = shown[:maxPerGroup]
		}
		for _, n := range shown {
			lines = append(lines, fmt.Sprintf("      %s", ui.White.Render(n)))
		}
		if len(names) > maxPerGroup {
			lines = append(lines, fmt.Sprintf("      %s",
				ui.Dim.Render(fmt.Sprintf("… and %d more", len(names)-maxPerGroup))))
		}
	}

	// Under --dry-run nothing is written, so promising to save would be the
	// kind of untrue line this screen exists to stop showing.
	prompt := "  Press Enter again to save this and continue."
	if m.dryRun {
		prompt = "  Press Enter again to continue (dry run — nothing is saved)."
	}
	return append(lines, "", ui.Green.Render(prompt))
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
