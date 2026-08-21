package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
}

// checkFirstRunPathCmd scans a candidate directory. It runs as a command
// because globbing a large tree — especially over a network mount — can take a
// moment, and blocking the UI on filesystem work is what the rest of this
// session was spent removing.
func checkFirstRunPathCmd(cfg *config.Config, path string) tea.Cmd {
	return func() tea.Msg {
		repos, err := git.FindRepos(expandTilde(path), cfg.GlobEntries(), cfg.ExplicitRepos())
		return firstRunPreviewResult{preview: firstRunPreview{
			Path:  path,
			Repos: repos,
			Err:   err,
			Ran:   true,
		}}
	}
}

// expandTilde mirrors the expansion the config does, so the preview scans the
// same directory that will actually be used once saved.
func expandTilde(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
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
		m.firstRun.value += string(msg.Runes)
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

	return panel(ui.CyanBold, "👋  Welcome", lines)
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
