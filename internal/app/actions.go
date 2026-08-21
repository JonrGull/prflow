package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/github"
	"github.com/JonrGull/prflow/internal/models"
	"github.com/JonrGull/prflow/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The GitHub Actions overview: a run list on the left, pinned run details on
// the right, refreshing itself every few seconds.
//
// Its state, its polling, its key handling and its rendering used to sit in
// four different files, so the one caveat that actually matters here — that
// adjustActionsPinnedScroll estimates the height renderPinnedPanel produces, and
// silently mis-scrolls when the two drift — was invisible unless you already
// knew to look for it. They are now next to each other.

// actionsState is everything this screen owns. Keeping it in one struct means
// reset() clears the screen by assigning a zero value, rather than by listing
// thirteen fields and quietly missing some.
type actionsState struct {
	entries      []actionsEntry
	repoErrors   []string // per-repo fetch errors
	index        int      // flat index into filtered entries
	loading      bool
	lastRefresh  time.Time
	autoRefresh  bool // auto-refresh every 5s (default on)
	filter       string
	filterActive bool

	// Pinned runs, shown in the right panel.
	pinned       []actionsPanel
	column       int // 0=left (runs), 1=right (pinned)
	runScroll    int // scroll offset in lines for the left run list
	pinnedIndex  int // focused pinned panel index
	pinnedScroll int // scroll offset in lines for the right panel
}

// actionsEntry holds a single workflow run with its repo
type actionsEntry struct {
	Repo models.RepoInfo
	Run  models.WorkflowRun
}

// actionsPanel holds a pinned workflow run with its fetched job details
type actionsPanel struct {
	Run  models.WorkflowRun
	Repo models.RepoInfo
	Jobs []models.WorkflowJob // nil = loading
}

func (m *Model) isPinned(runID uint64) bool {
	for _, p := range m.actions.pinned {
		if p.Run.DatabaseID == runID {
			return true
		}
	}
	return false
}

func (m *Model) unpinRun(runID uint64) bool {
	for i, p := range m.actions.pinned {
		if p.Run.DatabaseID == runID {
			m.actions.pinned = append(m.actions.pinned[:i], m.actions.pinned[i+1:]...)
			if m.actions.pinnedIndex >= len(m.actions.pinned) && m.actions.pinnedIndex > 0 {
				m.actions.pinnedIndex--
			}
			return true
		}
	}
	return false
}

// pinnedPanelLines is how tall renderPinnedPanel will draw this panel.
//
// Scrolling works in lines, so it has to know the height before the panel is
// rendered — which makes this a second, independent statement of what the
// renderer does, and a silent mis-scroll the moment the two disagree. It is a
// named function rather than inline arithmetic so TestPinnedPanelLinesMatchRender
// can hold it against the real output instead of against a copy of itself.
func pinnedPanelLines(p actionsPanel) int {
	lines := 2 + 1 + 1 // ColumnBox border top/bottom + title + the run info line
	if p.Jobs == nil {
		return lines + 1 // "Loading jobs..."
	}
	lines += len(p.Jobs)
	for _, j := range p.Jobs {
		// Steps are only listed for jobs that failed or are still running.
		if j.Conclusion != "failure" && j.Status != "in_progress" {
			continue
		}
		for _, s := range j.Steps {
			if s.Conclusion == "failure" || s.Status == "in_progress" {
				lines++
			}
		}
	}
	return lines
}

func (m *Model) adjustActionsPinnedScroll() {
	if len(m.actions.pinned) == 0 {
		m.actions.pinnedScroll = 0
		return
	}
	panelStart := 0
	focusStart := 0
	focusEnd := 0
	for i, p := range m.actions.pinned {
		lines := pinnedPanelLines(p)
		if i == m.actions.pinnedIndex {
			focusStart = panelStart
			focusEnd = panelStart + lines
		}
		panelStart += lines
	}

	visibleLines := m.actionsVisibleLines()
	// Edge-only: scroll down if focused panel's bottom is below viewport
	if focusEnd > m.actions.pinnedScroll+visibleLines {
		m.actions.pinnedScroll = focusEnd - visibleLines
	}
	// Scroll up if focused panel's top is above viewport
	if focusStart < m.actions.pinnedScroll {
		m.actions.pinnedScroll = focusStart
	}
	if m.actions.pinnedScroll < 0 {
		m.actions.pinnedScroll = 0
	}
}

func (m *Model) adjustActionsRunScroll(filtered []int) {
	// Keep highlight visible: 1 line reserved for ColumnBox title
	visibleLines := m.actionsVisibleLines()
	highlightLine := m.actionsRunListHighlightLine(filtered)
	if highlightLine < m.actions.runScroll {
		m.actions.runScroll = highlightLine
	} else if highlightLine >= m.actions.runScroll+visibleLines {
		m.actions.runScroll = highlightLine - visibleLines + 1
	}
	if m.actions.runScroll < 0 {
		m.actions.runScroll = 0
	}
}

func (m *Model) actionsVisibleLines() int {
	bannerLines := 5
	if m.dryRun {
		bannerLines += 2
	}
	panelHeight := m.height - bannerLines - 3 - 3 - 6 // banner, gaps, status, title+filter
	if panelHeight < 5 {
		panelHeight = 5
	}
	return panelHeight - 1 // -1 for ColumnBox title
}

type actionsRunsFetchedResult struct {
	entries    []actionsEntry
	repoErrors []string // per-repo errors (non-fatal)
	err        error
}

type actionsRefreshTickMsg struct{}

type actionsJobsFetchedResult struct {
	runID uint64
	jobs  []models.WorkflowJob
	err   error
}

func fetchActionsRunsCmd(cfg *config.Config, dryRun bool) tea.Cmd {
	return func() tea.Msg {
		if dryRun {
			return dryRunActionsRuns()
		}

		repos, err := discoverRepos(cfg)
		if err != nil {
			return actionsRunsFetchedResult{err: err}
		}

		// Build NWO → repo mapping (git only, no API calls)
		type repoWithNWO struct {
			repo models.RepoInfo
			nwo  string
		}
		var reposWithNWO []repoWithNWO
		for _, r := range repos {
			if nwo, err := github.GetRepoNWO(r.Path); err == nil {
				reposWithNWO = append(reposWithNWO, repoWithNWO{repo: r, nwo: nwo})
			}
		}

		type repoResult struct {
			repo models.RepoInfo
			runs []models.WorkflowRun
			err  error
		}

		results := parallelMap(reposWithNWO, func(rn repoWithNWO) repoResult {
			runs, err := github.ListWorkflowRunsByNWO(rn.nwo, 10)
			return repoResult{repo: rn.repo, runs: runs, err: err}
		})

		cutoff := time.Now().Add(-48 * time.Hour)
		var entries []actionsEntry
		var repoErrors []string
		for _, res := range results {
			if res.err != nil {
				repoErrors = append(repoErrors, fmt.Sprintf("%s: %v", res.repo.DisplayName, res.err))
				continue
			}
			// Keep in-progress/queued runs + latest completed per workflow (within 48h)
			latestCompleted := map[string]bool{} // workflowName -> already added
			for _, run := range res.runs {
				if run.UpdatedAt.Before(cutoff) {
					continue
				}
				if run.Status == "in_progress" || run.Status == "queued" {
					entries = append(entries, actionsEntry{Repo: res.repo, Run: run})
				} else if run.Status == "completed" && !latestCompleted[run.WorkflowName] {
					entries = append(entries, actionsEntry{Repo: res.repo, Run: run})
					latestCompleted[run.WorkflowName] = true
				}
			}
		}

		// Sort: newest first (by UpdatedAt descending)
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Run.UpdatedAt.After(entries[j].Run.UpdatedAt)
		})

		return actionsRunsFetchedResult{entries: entries, repoErrors: repoErrors}
	}
}

func actionsRefreshTickCmd() tea.Cmd {
	return tea.Tick(5*time.Second, func(_ time.Time) tea.Msg {
		return actionsRefreshTickMsg{}
	})
}

func fetchActionsJobsCmd(repoPath string, runID uint64, dryRun bool) tea.Cmd {
	return func() tea.Msg {
		if dryRun {
			return dryRunActionsJobs(runID)
		}

		jobs, err := github.GetWorkflowRunJobs(repoPath, runID)
		if err != nil {
			return actionsJobsFetchedResult{runID: runID, err: err}
		}
		return actionsJobsFetchedResult{runID: runID, jobs: jobs}
	}
}

func (m Model) handleActionsRunsFetched(msg actionsRunsFetchedResult) (tea.Model, tea.Cmd) {
	m.actions.loading = false
	if msg.err != nil {
		if m.screen == ScreenActionsOverview || m.screen == ScreenLoading {
			m.errorMessage = msg.err.Error()
			m.screen = ScreenError
		}
		return m, nil
	}
	m.actions.entries = msg.entries
	m.actions.repoErrors = msg.repoErrors
	m.actions.lastRefresh = time.Now()
	if m.screen == ScreenLoading {
		m.screen = ScreenActionsOverview
		m.actions.autoRefresh = true // default on for actions
	}

	// Clamp index and scroll to new filtered list size
	filtered := m.getFilteredActions()
	if m.actions.index >= len(filtered) {
		m.actions.index = max(len(filtered)-1, 0)
	}
	m.adjustActionsRunScroll(filtered)

	// Update pinned panels with fresh run data and re-fetch jobs if status changed or still active
	var refreshCmds []tea.Cmd
	for i, panel := range m.actions.pinned {
		for _, entry := range msg.entries {
			if entry.Run.DatabaseID == panel.Run.DatabaseID {
				if entry.Run.Status != panel.Run.Status || entry.Run.Status == "in_progress" || entry.Run.Status == "queued" {
					refreshCmds = append(refreshCmds, fetchActionsJobsCmd(entry.Repo.Path, entry.Run.DatabaseID, m.dryRun))
				}
				m.actions.pinned[i].Run = entry.Run
				break
			}
		}
	}

	var cmds []tea.Cmd
	if m.screen == ScreenActionsOverview && m.actions.autoRefresh {
		cmds = append(cmds, actionsRefreshTickCmd())
	}
	cmds = append(cmds, refreshCmds...)
	return m, tea.Batch(cmds...)
}

func (m Model) handleActionsRefreshTick() (tea.Model, tea.Cmd) {
	if m.screen != ScreenActionsOverview || !m.actions.autoRefresh {
		return m, nil // Stop tick chain
	}
	m.actions.loading = true
	return m, fetchActionsRunsCmd(m.config, m.dryRun)
}

func (m Model) handleActionsJobsFetched(msg actionsJobsFetchedResult) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.unpinRun(msg.runID)
		return m, nil
	}
	for i, p := range m.actions.pinned {
		if p.Run.DatabaseID == msg.runID {
			m.actions.pinned[i].Jobs = msg.jobs
			break
		}
	}
	return m, nil
}

func (m Model) handleActionsOverviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	filtered := m.getFilteredActions()

	// "R" toggles auto-refresh regardless of column
	if msg.Type == tea.KeyRunes && string(msg.Runes) == "R" && !m.actions.filterActive {
		m.actions.autoRefresh = !m.actions.autoRefresh
		if m.actions.autoRefresh {
			return m, actionsRefreshTickCmd()
		}
		return m, nil
	}

	// Right column: up/down navigates pinned panels
	if m.actions.column == 1 {
		switch msg.Type {
		case tea.KeyUp:
			if m.actions.pinnedIndex > 0 {
				m.actions.pinnedIndex--
				m.adjustActionsPinnedScroll()
			}
		case tea.KeyDown:
			if m.actions.pinnedIndex < len(m.actions.pinned)-1 {
				m.actions.pinnedIndex++
				m.adjustActionsPinnedScroll()
			}
		case tea.KeyLeft:
			m.actions.column = 0
		case tea.KeyEsc:
			m.actions.column = 0
		case tea.KeyCtrlC:
			m.shouldQuit = true
			return m, tea.Quit
		case tea.KeyRunes:
			switch string(msg.Runes) {
			case "o":
				if m.actions.pinnedIndex < len(m.actions.pinned) {
					panel := m.actions.pinned[m.actions.pinnedIndex]
					if panel.Run.URL != "" {
						_ = openURL(panel.Run.URL)
					}
				}
			case "q":
				m.shouldQuit = true
				return m, tea.Quit
			}
		}
		return m, nil
	}

	// Left column
	switch msg.Type {
	case tea.KeyUp:
		navigateColumnIndex(&m.actions.index, len(filtered), true)
		m.adjustActionsRunScroll(filtered)
	case tea.KeyDown:
		navigateColumnIndex(&m.actions.index, len(filtered), false)
		m.adjustActionsRunScroll(filtered)
	case tea.KeyLeft:
		// no-op, already in left column
	case tea.KeyRight:
		if len(m.actions.pinned) > 0 {
			m.actions.column = 1
			m.actions.pinnedIndex = 0
		}
	case tea.KeySpace:
		if m.actions.index >= len(filtered) {
			return m, nil
		}
		entry := m.actions.entries[filtered[m.actions.index]]
		if m.unpinRun(entry.Run.DatabaseID) {
			return m, nil
		}
		m.actions.pinned = append(m.actions.pinned, actionsPanel{
			Run:  entry.Run,
			Repo: entry.Repo,
		})
		return m, fetchActionsJobsCmd(entry.Repo.Path, entry.Run.DatabaseID, m.dryRun)
	case tea.KeyEsc:
		if m.actions.filterActive {
			m.actions.filterActive = false
			m.actions.filter = ""
			m.actions.index = 0
			m.actions.runScroll = 0
		} else {
			return m.reset()
		}
	case tea.KeyBackspace:
		if m.actions.filterActive && len(m.actions.filter) > 0 {
			m.actions.filter = trimLastRune(m.actions.filter)
			m.actions.index = 0
			m.actions.runScroll = 0
		}
	case tea.KeyCtrlC:
		m.shouldQuit = true
		return m, tea.Quit
	case tea.KeyRunes:
		key := string(msg.Runes)
		if m.actions.filterActive {
			m.actions.filter += key
			m.actions.index = 0
			m.actions.runScroll = 0
			return m, nil
		}
		switch key {
		case "q":
			m.shouldQuit = true
			return m, tea.Quit
		case "/":
			m.actions.filterActive = true
		case "a":
			var cmds []tea.Cmd
			for _, idx := range filtered {
				entry := m.actions.entries[idx]
				if m.isPinned(entry.Run.DatabaseID) {
					continue
				}
				m.actions.pinned = append(m.actions.pinned, actionsPanel{
					Run:  entry.Run,
					Repo: entry.Repo,
				})
				cmds = append(cmds, fetchActionsJobsCmd(entry.Repo.Path, entry.Run.DatabaseID, m.dryRun))
			}
			if len(cmds) > 0 {
				return m, tea.Batch(cmds...)
			}
		case "n":
			m.actions.pinned = nil
			m.actions.pinnedIndex = 0
		case "o":
			if m.actions.index < len(filtered) {
				entry := m.actions.entries[filtered[m.actions.index]]
				if entry.Run.URL != "" {
					_ = openURL(entry.Run.URL)
				}
			}
		}
	}
	return m, nil
}

// getFilteredActions returns indices of entries matching the text filter (flat, no columns)
func (m *Model) getFilteredActions() []int {
	filter := strings.ToLower(m.actions.filter)
	var indices []int
	for i, entry := range m.actions.entries {
		if filter == "" || matchesActionsFilter(entry, filter) {
			indices = append(indices, i)
		}
	}
	return indices
}

func matchesActionsFilter(entry actionsEntry, filter string) bool {
	return strings.Contains(strings.ToLower(entry.Repo.DisplayName), filter) ||
		strings.Contains(strings.ToLower(entry.Run.WorkflowName), filter) ||
		strings.Contains(strings.ToLower(entry.Run.HeadBranch), filter) ||
		strings.Contains(strings.ToLower(entry.Run.DisplayTitle), filter)
}

func (m Model) renderActionsOverviewWithHeight(availableHeight int) string {
	filtered := m.getFilteredActions()
	contentWidth := m.contentWidth()

	if len(filtered) == 0 && !m.actions.loading {
		dimStyle := ui.Dim
		successStyle := ui.Green
		centeredStyle := lipgloss.NewStyle().Width(contentWidth).Align(lipgloss.Center)

		lines := []string{
			"",
			"",
			centeredStyle.Render(successStyle.Render("✓") + " No active workflow runs"),
			centeredStyle.Render(dimStyle.Render("(showing last 48 hours)")),
		}

		if len(m.actions.repoErrors) > 0 {
			errorStyle := ui.Red
			lines = append(lines, "")
			lines = append(lines, centeredStyle.Render(errorStyle.Render(fmt.Sprintf("Failed to fetch %d repo(s):", len(m.actions.repoErrors)))))
			for _, e := range m.actions.repoErrors {
				lines = append(lines, centeredStyle.Render(dimStyle.Render(e)))
			}
		}

		return strings.Join(lines, "\n")
	}

	// Title bar with live countdown
	var titleText string
	if m.actions.loading {
		spinner := ui.Spinner(m.spinnerFrame)
		spinnerStyle := ui.Yellow
		titleText = "GitHub Actions (last 48h) " + spinnerStyle.Render(spinner+" refreshing...")
	} else {
		remaining := max(5-int(timeNow().Sub(m.actions.lastRefresh).Seconds()), 0)
		refreshStyle := ui.Dim
		titleText = "GitHub Actions (last 48h) " + refreshStyle.Render(fmt.Sprintf("(refresh in %ds)", remaining))
	}
	titleBox := ui.FilterInput(m.actions.filter, titleText, ui.ColorOrange, contentWidth-2)

	// Column widths
	leftWidth := contentWidth * 2 / 5
	rightWidth := contentWidth - leftWidth - 2

	// Build left panel: repo-grouped run list
	leftLines := m.renderActionsRunList(filtered, leftWidth)

	panelHeight := availableHeight - 6
	if panelHeight < 5 {
		panelHeight = 5
	}

	// Scroll is tracked in the key handler (adjustActionsRunScroll)
	visibleLines := panelHeight - 1 // -1 for ColumnBox title
	start := m.actions.runScroll
	end := start + visibleLines
	if end > len(leftLines) {
		end = len(leftLines)
	}
	if start > len(leftLines) {
		start = len(leftLines)
	}
	leftContent := strings.Join(leftLines[start:end], "\n")

	leftActive := m.actions.column == 0
	rightActive := m.actions.column == 1

	leftBox := ui.ColumnBox(leftContent, "RUNS", ui.ColorOrange, leftActive, leftWidth, panelHeight)

	// Build right panel — add 2 for ColumnBox borders so content isn't clipped
	rightContent := m.renderActionsPinnedPanels(rightWidth, panelHeight+2, rightActive)

	// Fix right panel to a consistent width so the left column doesn't shift when pins change
	rightBox := lipgloss.NewStyle().Width(rightWidth + 2).Render(rightContent)

	return titleBox + "\n" + ui.TwoColumns(leftBox, rightBox, 1)
}

// renderActionsRunList builds the left panel lines showing runs grouped by repo
func (m Model) renderActionsRunList(filtered []int, width int) []string {
	var lines []string
	dimStyle := ui.Dim

	// Group entries by repo
	currentRepo := ""
	var successCount, failCount, runningCount, queuedCount int

	for fi, entryIdx := range filtered {
		entry := m.actions.entries[entryIdx]
		highlighted := fi == m.actions.index

		// Repo header when repo changes
		if entry.Repo.DisplayName != currentRepo {
			if currentRepo != "" {
				lines = append(lines, "") // gap between groups
			}
			currentRepo = entry.Repo.DisplayName
			repoColor := ui.ColorMagenta
			if entry.Repo.InColumn(0, m.leftGroups()) {
				repoColor = ui.ColorCyan
			}
			repoStyle := lipgloss.NewStyle().Foreground(repoColor).Bold(true)
			lines = append(lines, repoStyle.Render(currentRepo))
		}

		pinned := m.isPinned(entry.Run.DatabaseID)

		// Status icon
		icon, iconColor := ui.WorkflowStatusIcon(entry.Run.Status, entry.Run.Conclusion, m.spinnerFrame)
		iconStyle := lipgloss.NewStyle().Foreground(iconColor)

		// Count statuses for summary
		switch {
		case entry.Run.Status == "in_progress":
			runningCount++
		case entry.Run.Status == "queued":
			queuedCount++
		case entry.Run.Conclusion == "success":
			successCount++
		case entry.Run.Conclusion == "failure":
			failCount++
		}

		// Arrow
		arrow := "  "
		if highlighted {
			arrowStyle := ui.Cyan
			arrow = arrowStyle.Render("▶ ")
		}

		// Checkbox
		checkStyle := lipgloss.NewStyle().Foreground(iconColor)
		checkbox := checkStyle.Render(ui.Checkbox(pinned))

		// Workflow + branch + time + run ID (truncate to fit)
		nameStyle := ui.White
		if highlighted {
			nameStyle = nameStyle.Bold(true)
		}
		branchStyle := lipgloss.NewStyle().Foreground(m.branchColor(entry.Run.HeadBranch))
		timeStr := relativeTime(entry.Run.UpdatedAt)
		runIDStr := fmt.Sprintf("#%d", entry.Run.DatabaseID)
		suffixLen := len(timeStr) + 1 + len(runIDStr) + 1 // time + space + id + space

		maxNameLen := width - 14 - suffixLen // arrow(2) + checkbox(3) + icon(2) + spaces(4) + margin(3) + suffix
		if maxNameLen < 10 {
			maxNameLen = 10
		}
		nameAndBranch := entry.Run.WorkflowName + " " + entry.Run.HeadBranch
		truncated := truncateString(nameAndBranch, maxNameLen)
		if truncated != nameAndBranch {
			lines = append(lines, fmt.Sprintf(" %s%s %s %s %s %s",
				arrow, checkbox, iconStyle.Render(icon), nameStyle.Render(truncated),
				dimStyle.Render(timeStr), dimStyle.Render(runIDStr)))
		} else {
			lines = append(lines, fmt.Sprintf(" %s%s %s %s %s %s %s",
				arrow, checkbox, iconStyle.Render(icon),
				nameStyle.Render(entry.Run.WorkflowName),
				branchStyle.Render(entry.Run.HeadBranch),
				dimStyle.Render(timeStr), dimStyle.Render(runIDStr)))
		}
	}

	// Status summary line
	if len(filtered) > 0 {
		lines = append(lines, "")
		var parts []string
		if successCount > 0 {
			parts = append(parts, ui.Green.Render(fmt.Sprintf("✓%d", successCount)))
		}
		if failCount > 0 {
			parts = append(parts, ui.Red.Render(fmt.Sprintf("✗%d", failCount)))
		}
		if runningCount > 0 {
			parts = append(parts, ui.Yellow.Render(fmt.Sprintf("%s%d", ui.Spinner(m.spinnerFrame), runningCount)))
		}
		if queuedCount > 0 {
			parts = append(parts, dimStyle.Render(fmt.Sprintf("◌%d", queuedCount)))
		}
		lines = append(lines, " "+strings.Join(parts, " "))
	}

	return lines
}

// actionsRunListHighlightLine returns the line index of the highlighted run in the left panel
func (m Model) actionsRunListHighlightLine(filtered []int) int {
	line := 0
	currentRepo := ""
	for fi, entryIdx := range filtered {
		entry := m.actions.entries[entryIdx]
		if entry.Repo.DisplayName != currentRepo {
			if currentRepo != "" {
				line++ // gap
			}
			currentRepo = entry.Repo.DisplayName
			line++ // repo header
		}
		if fi == m.actions.index {
			return line
		}
		line++ // run line
	}
	return 0
}

// renderActionsPinnedPanels builds the right panel with stacked pinned run details
func (m Model) renderActionsPinnedPanels(width, maxHeight int, active bool) string {
	if len(m.actions.pinned) == 0 {
		dimStyle := ui.Dim.
			Width(width).Align(lipgloss.Center)
		padding := strings.Repeat("\n", maxHeight/3)
		return padding + dimStyle.Render("Space to pin runs")
	}

	var blocks []string
	for i, panel := range m.actions.pinned {
		borderColor := ui.ColorMagenta
		if panel.Repo.InColumn(0, m.leftGroups()) {
			borderColor = ui.ColorCyan
		}
		highlighted := active && i == m.actions.pinnedIndex
		blocks = append(blocks, m.renderPinnedPanel(panel, borderColor, width, highlighted))
	}

	joined := lipgloss.JoinVertical(lipgloss.Left, blocks...)
	lines := strings.Split(joined, "\n")

	// Scroll offset is set by adjustActionsPinnedScroll() in the key handler
	scrollOffset := 0
	if active {
		scrollOffset = m.actions.pinnedScroll
	}

	end := scrollOffset + maxHeight
	if end > len(lines) {
		end = len(lines)
	}
	if scrollOffset >= len(lines) {
		return ""
	}
	return strings.Join(lines[scrollOffset:end], "\n")
}

// renderPinnedPanel renders a single pinned run's detail box
func (m Model) renderPinnedPanel(panel actionsPanel, borderColor lipgloss.Color, width int, highlighted bool) string {
	// Run info line
	statusIcon, statusColor := ui.WorkflowStatusIcon(panel.Run.Status, panel.Run.Conclusion, m.spinnerFrame)
	statusStyle := lipgloss.NewStyle().Foreground(statusColor)
	nameStyle := ui.WhiteBold
	branchStyle := lipgloss.NewStyle().Foreground(m.branchColor(panel.Run.HeadBranch))
	timeStyle := ui.Dim

	infoLine := fmt.Sprintf(" %s %s  %s  %s  %s  %s",
		statusStyle.Render(statusIcon),
		nameStyle.Render(panel.Run.WorkflowName),
		branchStyle.Render(panel.Run.HeadBranch),
		ui.White.Render(truncateString(panel.Run.DisplayTitle, 30)),
		timeStyle.Render(relativeTime(panel.Run.UpdatedAt)),
		timeStyle.Render(fmt.Sprintf("#%d", panel.Run.DatabaseID)),
	)

	if panel.Jobs == nil {
		// Still loading
		spinnerStyle := ui.Yellow
		content := infoLine + "\n " + spinnerStyle.Render(ui.Spinner(m.spinnerFrame)+" Loading jobs...")
		return ui.ColumnBox(content, panel.Repo.DisplayName, borderColor, highlighted, width, 0)
	}

	var lines []string
	lines = append(lines, infoLine)

	// Jobs with inline status
	for _, job := range panel.Jobs {
		jobIcon, jobIconColor := ui.WorkflowStatusIcon(job.Status, job.Conclusion, m.spinnerFrame)
		jobIconStyle := lipgloss.NewStyle().Foreground(jobIconColor)
		jobNameStyle := ui.White

		lines = append(lines, fmt.Sprintf("   %s %s", jobNameStyle.Render(job.Name), jobIconStyle.Render(jobIcon)))

		// Show steps for failed or in-progress jobs
		if job.Conclusion == "failure" || job.Status == "in_progress" {
			stepNameStyle := ui.Dim
			for _, step := range job.Steps {
				if step.Conclusion == "failure" || step.Status == "in_progress" {
					stepIcon, stepColor := ui.WorkflowStatusIcon(step.Status, step.Conclusion, m.spinnerFrame)
					stepIconStyle := lipgloss.NewStyle().Foreground(stepColor)
					lines = append(lines, fmt.Sprintf("      %d. %s %s",
						step.Number,
						stepNameStyle.Render(step.Name),
						stepIconStyle.Render(stepIcon),
					))
				}
			}
		}
	}

	content := strings.Join(lines, "\n")
	return ui.ColumnBox(content, panel.Repo.DisplayName, borderColor, highlighted, width, 0)
}
