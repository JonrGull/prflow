package app

import (
	"fmt"
	"strings"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/git"
	"github.com/JonrGull/prflow/internal/models"
	"github.com/JonrGull/prflow/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Pulling a branch across every repo: pick a branch, watch the progress, read
// the summary.

// pullState is everything the pull-all screens own.
type pullState struct {
	// branch is the chain branch chosen, as shown. useDefault marks the chain's
	// final base when it is the @default token, so each repo pulls its own
	// default branch — the command used to infer that from branch == "main",
	// which only worked while "main" was hardcoded into the menu.
	branch     string
	useDefault bool
	repos      []models.RepoInfo
	results    []models.PullResult
	currentIdx int
}

type pullRepoResult struct {
	result models.PullResult
}

type pullReposLoadedResult struct {
	repos []models.RepoInfo
	err   error
}

// makePullResult creates a pullRepoResult with the given status
func makePullResult(repo models.RepoInfo, status models.PullStatus, commits int, errMsg string) pullRepoResult {
	return pullRepoResult{result: models.PullResult{
		Repo:        repo,
		Status:      status,
		CommitCount: commits,
		Error:       errMsg,
	}}
}

// loadPullReposCmd loads all repos for pull operation
func loadPullReposCmd(cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		repos, err := discoverRepos(cfg)
		if err != nil {
			return pullReposLoadedResult{err: err}
		}
		return pullReposLoadedResult{repos: repos}
	}
}

// pullNextRepoCmd pulls the next repo in the list
func pullNextRepoCmd(repo models.RepoInfo, branch string, useDefault, dryRun bool) tea.Cmd {
	return func() tea.Msg {
		targetBranch := branch
		if useDefault {
			// Repos disagree about main versus master.
			targetBranch = repo.MainBranch
		}

		if dryRun {
			return dryRunPullResult(repo)
		}

		// Check if branch exists
		if !git.HasBranch(repo.Path, targetBranch) {
			return makePullResult(repo, models.PullSkippedNoBranch, 0, "")
		}

		// Check for dirty working tree
		dirty, err := git.IsDirty(repo.Path)
		if err != nil {
			return makePullResult(repo, models.PullFailed, 0, err.Error())
		}
		if dirty {
			return makePullResult(repo, models.PullSkippedDirty, 0, "")
		}

		// Fetch first to get remote changes
		if err := git.FetchBranches(repo.Path, []string{targetBranch}); err != nil {
			// If fetch fails due to branch not found, skip
			if _, ok := err.(*git.BranchNotFoundError); ok {
				return makePullResult(repo, models.PullSkippedNoBranch, 0, "")
			}
			return makePullResult(repo, models.PullFailed, 0, err.Error())
		}

		// Checkout and pull
		commits, err := git.CheckoutAndPull(repo.Path, targetBranch)
		if err != nil {
			return makePullResult(repo, models.PullFailed, 0, err.Error())
		}

		if commits == 0 {
			return makePullResult(repo, models.PullUpToDate, 0, "")
		}
		return makePullResult(repo, models.PullUpdated, commits, "")
	}
}

// handlePullReposLoaded handles the result of loading repos for pull
func (m Model) handlePullReposLoaded(msg pullReposLoadedResult) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.errorMessage = msg.err.Error()
		m.screen = ScreenError
		return m, nil
	}

	m.pull.repos = msg.repos
	m.pull.results = nil
	m.pull.currentIdx = 0
	m.screen = ScreenPullProgress

	if len(m.pull.repos) == 0 {
		m.screen = ScreenPullSummary
		return m, nil
	}

	// Start pulling first repo
	return m, pullNextRepoCmd(m.pull.repos[0], m.pull.branch, m.pull.useDefault, m.dryRun)
}

// handlePullRepoResult handles the result of pulling a single repo
func (m Model) handlePullRepoResult(msg pullRepoResult) (tea.Model, tea.Cmd) {
	m.pull.results = append(m.pull.results, msg.result)
	m.pull.currentIdx++

	if m.pull.currentIdx >= len(m.pull.repos) {
		m.screen = ScreenPullSummary
		return m, nil
	}

	// Pull next repo
	return m, pullNextRepoCmd(m.pull.repos[m.pull.currentIdx], m.pull.branch, m.pull.useDefault, m.dryRun)
}

func (m Model) handlePullBranchSelectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.shouldQuit = true
		return m, tea.Quit
	case "up", "k":
		navigateColumnIndex(&m.menuIndex, len(m.chainBranches()), true)
	case "down", "j":
		navigateColumnIndex(&m.menuIndex, len(m.chainBranches()), false)
	case "enter", "1", "2", "3", "4", "5", "6", "7", "8", "9":
		if idx, ok := numKeyIndex(msg.String(), len(m.chainBranches())); ok {
			m.menuIndex = idx
		}
		return m.selectPullBranch()
	case "esc":
		m.screen = ScreenMainMenu
		m.menuIndex = 0
	}
	return m, nil
}

func (m Model) selectPullBranch() (tea.Model, tea.Cmd) {
	branches := m.chainBranches()
	if m.menuIndex >= len(branches) {
		return m, nil
	}
	m.pull.branch = branches[m.menuIndex]
	flows := m.flows()
	m.pull.useDefault = m.menuIndex == len(branches)-1 &&
		len(flows) > 0 && flows[len(flows)-1].Base == models.DefaultBranchToken
	m.screen = ScreenLoading
	m.loadingMessage = fmt.Sprintf("Scanning repositories for %s...", m.pull.branch)
	return m, loadPullReposCmd(m.config)
}

func (m Model) handlePullSummaryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.shouldQuit = true
		return m, tea.Quit
	case "enter", "esc":
		return m.reset()
	}
	return m, nil
}

func (m Model) renderPullBranchSelect() string {
	branches := m.chainBranches()

	// Build left column (menu) content
	var menuLines []string
	menuLines = append(menuLines, "")

	for i, name := range branches {
		isSelected := i == m.menuIndex

		nameStyle := lipgloss.NewStyle().Foreground(flowChainColor(i)).Bold(true)
		gap := " "
		if isSelected {
			nameStyle = nameStyle.Background(ui.ColorDarkGray)
			gap = lipgloss.NewStyle().Background(ui.ColorDarkGray).Render(" ")
		}
		desc := fmt.Sprintf("Pull the %s branch everywhere", name)
		if i == len(branches)-1 {
			desc = "Pull each repo's default branch"
		}
		row := numberedMenuRow(fmt.Sprintf("%d.", i+1), gap+nameStyle.Render(name), desc, isSelected)
		menuLines = append(menuLines, row[0], row[1], "")
	}

	if len(branches) == 0 {
		menuLines = append(menuLines, ui.Red.Render(" No release steps configured."))
		menuLines = append(menuLines, ui.Dim.Render(" Add [[flows]] entries in settings."))
		menuLines = append(menuLines, "")
	}

	menuTitleStyle := ui.CyanBold
	menuContent := panel(menuTitleStyle, "Select Branch to Pull", menuLines)

	// Build right column (info panel)
	var infoLines []string
	infoLines = append(infoLines, "")

	if m.menuIndex < len(branches) {
		name := branches[m.menuIndex]
		titleStyle := lipgloss.NewStyle().Foreground(flowChainColor(m.menuIndex)).Bold(true)
		infoLines = append(infoLines, "  "+titleStyle.Render(name))
		infoLines = append(infoLines, "")
		infoLines = append(infoLines, wrapToWidth(
			fmt.Sprintf("Pull the %s branch across all configured repos.", name), 28, "  ")...)
		infoLines = append(infoLines, "")
		if m.menuIndex == len(branches)-1 {
			infoLines = append(infoLines, wrapToWidth(
				"This is the end of the release chain, so it uses each repo's own default branch.", 28, "  ")...)
		} else {
			infoLines = append(infoLines, wrapToWidth(
				"Repos without the branch are skipped rather than failed.", 28, "  ")...)
		}
	}

	infoTitleStyle := ui.WhiteBold
	infoContent := panel(infoTitleStyle, "Branch Info", infoLines)

	return ui.UnifiedPanel(menuContent, infoContent, 48, 48, ui.ColorCyan)
}

func (m Model) renderPullProgress() string {
	contentWidth := m.contentWidth()

	var lines []string
	lines = append(lines, "")

	// Header
	branchStyle := lipgloss.NewStyle().Foreground(m.branchColor(m.pull.branch)).Bold(true)
	headerStyle := ui.White
	lines = append(lines, headerStyle.Render("  Pulling ")+branchStyle.Render(m.pull.branch)+headerStyle.Render(" across all repos..."))
	lines = append(lines, "")

	// Show progress for each repo
	checkStyle := ui.Green
	warnStyle := ui.Yellow
	spinnerStyle := ui.Cyan
	dimStyle := ui.Dim
	repoStyle := ui.Cyan

	maxVisible := 15
	startIdx := 0
	if m.pull.currentIdx > maxVisible-3 {
		startIdx = m.pull.currentIdx - (maxVisible - 3)
	}

	for i := startIdx; i < len(m.pull.repos) && i < startIdx+maxVisible; i++ {
		repo := m.pull.repos[i]
		name := truncateString(repo.DisplayName, 30)

		if i < len(m.pull.results) {
			// Completed
			result := m.pull.results[i]
			var status string
			switch result.Status {
			case models.PullUpdated:
				status = checkStyle.Render("✓") + " " + repoStyle.Render(name) + dimStyle.Render(fmt.Sprintf(" updated (%d commits)", result.CommitCount))
			case models.PullUpToDate:
				status = checkStyle.Render("✓") + " " + repoStyle.Render(name) + dimStyle.Render(" already up to date")
			case models.PullSkippedNoBranch:
				status = warnStyle.Render("⚠") + " " + repoStyle.Render(name) + dimStyle.Render(fmt.Sprintf(" skipped (no %s branch)", m.pull.branch))
			case models.PullSkippedDirty:
				status = warnStyle.Render("⚠") + " " + repoStyle.Render(name) + dimStyle.Render(" skipped (uncommitted changes)")
			case models.PullFailed:
				errStyle := ui.Red
				status = errStyle.Render("✗") + " " + repoStyle.Render(name) + dimStyle.Render(" failed")
			}
			lines = append(lines, "  "+status)
		} else if i == m.pull.currentIdx {
			// Currently processing
			spinner := ui.Spinner(m.spinnerFrame)
			lines = append(lines, "  "+spinnerStyle.Render(spinner)+" "+repoStyle.Render(name)+dimStyle.Render(" pulling..."))
		} else {
			// Waiting
			lines = append(lines, "  "+dimStyle.Render("  "+name+" waiting..."))
		}
	}

	if len(m.pull.repos) > startIdx+maxVisible {
		remaining := len(m.pull.repos) - (startIdx + maxVisible)
		lines = append(lines, dimStyle.Render(fmt.Sprintf("  ... and %d more", remaining)))
	}

	lines = append(lines, "")

	// Progress bar.
	// Guard the empty case: 0/0 is NaN, and converting NaN to int yields an
	// unspecified (in practice hugely negative) value, which then panics
	// strings.Repeat. Reachable whenever discovery finds no repos — a glob
	// matching nothing, for instance.
	barWidth := 40
	filled := 0
	if total := len(m.pull.repos); total > 0 {
		filled = int(float64(m.pull.currentIdx) / float64(total) * float64(barWidth))
	}
	if filled > barWidth {
		filled = barWidth
	}
	if filled < 0 {
		filled = 0
	}

	progressStyle := ui.Cyan
	emptyStyle := ui.Dim
	bar := progressStyle.Render(strings.Repeat("█", filled)) + emptyStyle.Render(strings.Repeat("░", barWidth-filled))
	lines = append(lines, fmt.Sprintf("  %s %d/%d", bar, m.pull.currentIdx, len(m.pull.repos)))

	content := strings.Join(lines, "\n")

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.ColorPurple).
		Width(contentWidth).
		Padding(1, 2)

	return boxStyle.Render(content)
}

func (m Model) renderPullSummaryWithHeight(availableHeight int) string {
	var lines []string

	// Header
	branchStyle := lipgloss.NewStyle().Foreground(m.branchColor(m.pull.branch)).Bold(true)
	titleStyle := ui.GreenBold
	lines = append(lines, titleStyle.Render("Pull Complete: ")+branchStyle.Render(m.pull.branch))
	lines = append(lines, "")

	// Group results by status
	resultsByStatus := map[models.PullStatus][]models.PullResult{}
	for _, r := range m.pull.results {
		resultsByStatus[r.Status] = append(resultsByStatus[r.Status], r)
	}

	repoStyle := ui.Cyan
	dimStyle := ui.Dim
	greenStyle := ui.GreenBold
	yellowStyle := ui.YellowBold
	redStyle := ui.RedBold

	// Updated
	if updated := resultsByStatus[models.PullUpdated]; len(updated) > 0 {
		lines = append(lines, greenStyle.Render(fmt.Sprintf("Updated (%d):", len(updated))))
		for _, r := range updated {
			lines = append(lines, "  "+repoStyle.Render(r.Repo.DisplayName)+dimStyle.Render(fmt.Sprintf(" (%d commits)", r.CommitCount)))
		}
		lines = append(lines, "")
	}

	// Already up to date
	if upToDate := resultsByStatus[models.PullUpToDate]; len(upToDate) > 0 {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("Already up to date (%d):", len(upToDate))))
		for _, r := range upToDate {
			lines = append(lines, "  "+dimStyle.Render(r.Repo.DisplayName))
		}
		lines = append(lines, "")
	}

	// Skipped - no branch
	if noBranch := resultsByStatus[models.PullSkippedNoBranch]; len(noBranch) > 0 {
		lines = append(lines, yellowStyle.Render(fmt.Sprintf("Skipped - no branch (%d):", len(noBranch))))
		for _, r := range noBranch {
			lines = append(lines, "  "+dimStyle.Render(r.Repo.DisplayName))
		}
		lines = append(lines, "")
	}

	// Skipped - dirty
	if dirty := resultsByStatus[models.PullSkippedDirty]; len(dirty) > 0 {
		lines = append(lines, yellowStyle.Render(fmt.Sprintf("Skipped - local changes (%d):", len(dirty))))
		for _, r := range dirty {
			lines = append(lines, "  "+dimStyle.Render(r.Repo.DisplayName))
		}
		lines = append(lines, "")
	}

	// Failed
	if failed := resultsByStatus[models.PullFailed]; len(failed) > 0 {
		lines = append(lines, redStyle.Render(fmt.Sprintf("Failed (%d):", len(failed))))
		for _, r := range failed {
			lines = append(lines, "  "+repoStyle.Render(r.Repo.DisplayName)+dimStyle.Render(" - "+r.Error))
		}
		lines = append(lines, "")
	}

	content := strings.Join(lines, "\n")
	boxWidth := m.contentWidth() - 10

	// Determine header color based on results
	headerColor := ui.ColorGreen
	if len(resultsByStatus[models.PullFailed]) > 0 {
		headerColor = ui.ColorYellow
	}

	return ui.ColumnBox(content, " Pull Summary ", headerColor, true, boxWidth, availableHeight)
}
