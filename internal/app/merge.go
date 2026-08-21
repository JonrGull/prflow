package app

import (
	"fmt"
	"strings"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/git"
	"github.com/JonrGull/prflow/internal/github"
	"github.com/JonrGull/prflow/internal/models"
	"github.com/JonrGull/prflow/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Viewing and merging the open release PRs — the column-per-step selector, the
// confirmation, the merge run and its summary.

// mergeState is everything the open-PRs and merge screens own.
type mergeState struct {
	repoErrors []string // per-repo fetch errors; these repos are absent from the list
	openPRs    []OpenPREntry

	prs      []models.MergePrEntry
	selected []bool

	// column indexes the configured release steps, and cursors holds one
	// cursor per column. This used to be a column flag with a devIndex and a
	// mainIndex beside it, which is what pinned the screen to exactly two
	// steps.
	column  int
	cursors []int

	results []models.MergeResult
	current int
	total   int
}

// OpenPREntry holds repo info with its PR status
type OpenPREntry struct {
	Repo   models.RepoInfo
	Status models.RepoPrStatus
}

type openPRsFetchedResult struct {
	entries []OpenPREntry
	// repoErrors holds per-repo failures. A repo that fails here is omitted
	// from entries, so these are surfaced rather than silently swallowed.
	repoErrors []string
	err        error
}

type mergeCompleteResult struct {
	result models.MergeResult
}

func fetchOpenPRsCmd(cfg *config.Config, dryRun bool) tea.Cmd {
	return func() tea.Msg {
		flows := cfg.FlowEntries()

		if dryRun {
			return dryRunOpenPRs(flows)
		}

		// Find all repos
		repos, err := discoverRepos(cfg)
		if err != nil {
			return openPRsFetchedResult{err: err}
		}

		// Fetch open PRs in parallel
		type result struct {
			entry  OpenPREntry
			hasAny bool
			err    error
		}

		results := parallelMap(repos, func(r models.RepoInfo) result {
			status, err := github.GetOpenReleasePRs(r.Path, flows, r.MainBranch)
			if err != nil {
				return result{err: fmt.Errorf("%s: %w", r.DisplayName, err)}
			}
			return result{
				entry:  OpenPREntry{Repo: r, Status: *status},
				hasAny: status.HasAny(),
			}
		})

		// Keep only repos with open PRs. A repo that errors (e.g. no remotes)
		// is skipped rather than failing the whole fetch, but its error is
		// collected so the UI can say so — otherwise the repo just disappears
		// from the list with no signal.
		var entries []OpenPREntry
		var repoErrors []string
		for _, res := range results {
			if res.err != nil {
				repoErrors = append(repoErrors, res.err.Error())
				continue
			}
			if res.hasAny {
				entries = append(entries, res.entry)
			}
		}

		return openPRsFetchedResult{entries: entries, repoErrors: repoErrors}
	}
}

func startMergingCmd(m *Model, prIndex int) tea.Cmd {
	return func() tea.Msg {
		if prIndex >= len(m.merge.prs) {
			return nil
		}

		pr := m.merge.prs[prIndex]
		if prIndex >= len(m.merge.selected) || !m.merge.selected[prIndex] {
			// Skip unselected PRs
			return nil
		}

		base := models.MergeResult{
			RepoName:   pr.Repo.DisplayName,
			PrNumber:   pr.PrNumber,
			PrTitle:    pr.PrTitle,
			PrBody:     pr.PrBody,
			Flow:       pr.Flow,
			MainBranch: pr.Repo.MainBranch,
			URL:        pr.URL,
		}

		if m.dryRun {
			return dryRunMergeResult(base)
		}

		// Merge the PR
		if err := github.MergePR(pr.Repo.Path, pr.PrNumber); err != nil {
			errStr := err.Error()
			base.Error = &errStr
			return mergeCompleteResult{result: base}
		}

		base.Success = true
		return mergeCompleteResult{result: base}
	}
}

func (m Model) handleOpenPRsFetchedResult(msg openPRsFetchedResult) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.errorMessage = msg.err.Error()
		m.screen = ScreenError
		return m, nil
	}

	m.screen = ScreenViewOpenPrs
	m.merge.openPRs = msg.entries
	m.merge.repoErrors = msg.repoErrors

	// Build merge PR list: one entry per open PR, tagged with the step it
	// belongs to so the columns can be filtered by step.
	m.merge.prs = nil
	for _, entry := range m.merge.openPRs {
		for _, fp := range entry.Status.Flows {
			if fp.PR == nil {
				continue
			}
			m.merge.prs = append(m.merge.prs, models.MergePrEntry{
				Repo:     entry.Repo,
				PrNumber: fp.PR.Number,
				PrTitle:  fp.PR.Title,
				URL:      fp.PR.URL,
				PrBody:   fp.PR.Body,
				Flow:     fp.Flow,
			})
		}
	}

	m.merge.selected = make([]bool, len(m.merge.prs))
	m.merge.column = 0
	m.merge.cursors = make([]int, len(m.flows()))

	return m, nil
}

func (m Model) handleMergeCompleteResult(msg mergeCompleteResult) (tea.Model, tea.Cmd) {
	m.merge.results = append(m.merge.results, msg.result)
	m.merge.current++

	if msg.result.Success {
		m.recordSessionPR(msg.result.RepoName, msg.result.URL, msg.result.Flow.Display(msg.result.MainBranch), "merged", msg.result.PrNumber)
	}

	if m.merge.current >= m.merge.total {
		return m.finishMerging()
	}

	// Find next selected PR to merge
	for i := m.merge.current; i < len(m.merge.prs); i++ {
		if i < len(m.merge.selected) && m.merge.selected[i] {
			return m, startMergingCmd(&m, i)
		}
		m.merge.current++
	}

	// No more PRs to merge
	return m.finishMerging()
}

func (m Model) finishMerging() (tea.Model, tea.Cmd) {
	// Extract tickets from successfully merged PRs (title + body)
	ticketRegex := m.config.TicketRegex()
	seen := map[string]bool{}
	var tickets []string
	for _, result := range m.merge.results {
		if !result.Success {
			continue
		}
		text := result.PrTitle + "\n" + result.PrBody
		for _, t := range git.ExtractTickets(text, ticketRegex) {
			if !seen[t] {
				seen[t] = true
				tickets = append(tickets, t)
			}
		}
	}
	m.tickets = tickets

	if m.shouldShowQaTagScreen() {
		cmd := m.initQaTagState()
		m.screen = ScreenQaTagSelect
		return m, cmd
	}

	m.screen = ScreenMergeSummary
	m.menuIndex = 0
	return m, nil
}

func (m Model) handleViewOpenPrsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyUp:
		m.navigateMergeColumn(true)
	case tea.KeyDown:
		m.navigateMergeColumn(false)
	case tea.KeyLeft:
		m.stepMergeColumn(-1)
	case tea.KeyRight:
		m.stepMergeColumn(1)
	case tea.KeySpace:
		m.toggleMergeSelection()
	case tea.KeyTab, tea.KeyEnter:
		// Proceed to merge confirmation if any selected
		count := 0
		for _, selected := range m.merge.selected {
			if selected {
				count++
			}
		}
		if count > 0 {
			m.screen = ScreenMergeConfirmation
			m.confirmSelection = 0
		}
	case tea.KeyEsc:
		m.merge.openPRs = nil
		m.merge.prs = nil
		m.merge.selected = nil
		m.screen = ScreenMainMenu
		m.menuIndex = 0
	case tea.KeyCtrlC:
		m.shouldQuit = true
		return m, tea.Quit
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "q":
			m.shouldQuit = true
			return m, tea.Quit
		case "a":
			m.selectAllInColumn()
		case "r":
			// An explicit refresh should also pick up repos added on disk.
			invalidateRepoCache()
			m.screen = ScreenLoading
			m.loadingMessage = "Fetching open PRs..."
			return m, fetchOpenPRsCmd(m.config, m.dryRun)
		case "o":
			openURLs(urls(m.openPRLinks()))
		case "c":
			m.copyLinks(m.openPRLinks(), "Copied URLs!")
			return m, nil
		}
	}
	return m, nil
}

// getFilteredMergePRs returns the indices of the PRs belonging to one column,
// where a column is one configured release step.
func (m *Model) getFilteredMergePRs(column int) []int {
	flows := m.flows()
	if column < 0 || column >= len(flows) {
		return nil
	}

	var indices []int
	for i, pr := range m.merge.prs {
		if pr.Flow == flows[column] {
			indices = append(indices, i)
		}
	}

	return indices
}

// mergeCursor reads a column's cursor, tolerating a cursors slice that is
// shorter than the column count so a partially built state still renders.
func (m Model) mergeCursor(column int) int {
	if column < 0 || column >= len(m.merge.cursors) {
		return 0
	}
	return m.merge.cursors[column]
}

// setMergeCursor writes a column's cursor, growing the slice to fit.
func (m *Model) setMergeCursor(column, value int) {
	if column < 0 {
		return
	}
	for len(m.merge.cursors) <= column {
		m.merge.cursors = append(m.merge.cursors, 0)
	}
	m.merge.cursors[column] = value
}

// stepMergeColumn moves one column left or right, skipping empty columns and
// stopping at either end.
func (m *Model) stepMergeColumn(delta int) {
	flows := m.flows()
	for next := m.merge.column + delta; next >= 0 && next < len(flows); next += delta {
		filtered := m.getFilteredMergePRs(next)
		if len(filtered) == 0 {
			continue
		}
		m.merge.column = next
		if m.mergeCursor(next) >= len(filtered) {
			m.setMergeCursor(next, len(filtered)-1)
		}
		return
	}
}

func (m *Model) navigateMergeColumn(up bool) {
	filtered := m.getFilteredMergePRs(m.merge.column)
	cursor := m.mergeCursor(m.merge.column)
	navigateColumnIndex(&cursor, len(filtered), up)
	m.setMergeCursor(m.merge.column, cursor)
}

func (m *Model) toggleMergeSelection() {
	filtered := m.getFilteredMergePRs(m.merge.column)
	toggleSelection(m.merge.selected, filtered, m.mergeCursor(m.merge.column))
}

func (m *Model) selectAllInColumn() {
	filtered := m.getFilteredMergePRs(m.merge.column)
	if len(filtered) == 0 {
		return
	}

	// Check if all in column are selected
	allSelected := true
	for _, prIdx := range filtered {
		if prIdx < len(m.merge.selected) && !m.merge.selected[prIdx] {
			allSelected = false
			break
		}
	}

	// Toggle: if all selected, deselect all; otherwise select all
	newState := !allSelected
	for _, prIdx := range filtered {
		if prIdx < len(m.merge.selected) {
			m.merge.selected[prIdx] = newState
		}
	}
}

func (m Model) handleMergeSummaryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.shouldQuit = true
		return m, tea.Quit
	case "o":
		openURLs(urls(m.mergedPRLinks()))
	case "c":
		m.copyLinks(m.mergedPRLinks(), "Copied URLs!")
		return m, nil
	case "enter", "esc":
		return m.reset()
	}
	return m, nil
}

func (m Model) navigateToMergePRs() (tea.Model, tea.Cmd) {
	mode := ModeBatch
	m.mode = &mode
	m.screen = ScreenLoading
	m.loadingMessage = "Fetching open PRs..."
	return m, fetchOpenPRsCmd(m.config, m.dryRun)
}

// renderRepoFetchErrors renders the repos that failed to fetch, if any.
// Without this a repo with a broken remote just vanishes from the list.
func (m Model) renderRepoFetchErrors(centeredStyle lipgloss.Style) []string {
	if len(m.merge.repoErrors) == 0 {
		return nil
	}
	errorStyle := ui.Red
	dimStyle := ui.Dim

	lines := []string{
		"",
		centeredStyle.Render(errorStyle.Render(fmt.Sprintf("⚠ %d repo(s) could not be checked:", len(m.merge.repoErrors)))),
	}
	for _, e := range m.merge.repoErrors {
		lines = append(lines, centeredStyle.Render(dimStyle.Render(truncateString(e, m.contentWidth()-4))))
	}
	return lines
}

func (m Model) renderViewOpenPrsWithHeight(availableHeight int) string {
	if len(m.merge.prs) == 0 {
		successStyle := ui.Green
		dimStyle := ui.Dim

		successText := fmt.Sprintf("%s No open release PRs", successStyle.Render("✓"))
		subText := dimStyle.Render("All repositories are up to date!")

		// Center the text
		centeredStyle := lipgloss.NewStyle().Width(m.contentWidth()).Align(lipgloss.Center)

		var lines []string
		lines = append(lines, "")
		lines = append(lines, "")
		lines = append(lines, "")
		lines = append(lines, centeredStyle.Render(successText))
		lines = append(lines, centeredStyle.Render(subText))
		lines = append(lines, m.renderRepoFetchErrors(centeredStyle)...)

		return strings.Join(lines, "\n")
	}

	flows := m.flows()
	gap := 2

	// One column per configured step — but only as many as fit. A long chain
	// shows a window of columns around the active step instead of overflowing
	// the frame, the same way each column scrolls its own rows.
	visible := len(flows)
	for visible > 1 && columnWidthFor(m.contentWidth(), visible, gap) < minMergeColumnWidth {
		visible--
	}
	first := 0
	if visible < len(flows) {
		first = m.merge.column - visible/2
		if first < 0 {
			first = 0
		}
		if first+visible > len(flows) {
			first = len(flows) - visible
		}
	}

	// The width formula reduces to the old (contentWidth-6)/2 at two columns,
	// which is what keeps the default layout pixel-identical.
	columnWidth := columnWidthFor(m.contentWidth(), visible, gap)

	// Column height calculation
	columnHeight := availableHeight - 8
	if columnHeight < 5 {
		columnHeight = 5
	}

	// Title bar width matches the columns plus the gaps between them
	titleWidth := columnWidth*visible + gap*(visible-1)

	// Count selected
	selectedCount := 0
	for _, s := range m.merge.selected {
		if s {
			selectedCount++
		}
	}

	// Title bar (similar to batch select filter box)
	title := fmt.Sprintf("Open Release PRs (%d selected)", selectedCount)
	if visible < len(flows) {
		// Say which slice of the chain is on screen; without this the hidden
		// columns just look like missing PRs.
		title += fmt.Sprintf("  ·  steps %d-%d of %d", first+1, first+visible, len(flows))
	}
	titleBox := ui.TitleBar(title, ui.ColorYellow, titleWidth)

	// Build one column per step
	headerLines := 2
	visibleContentLines := columnHeight - headerLines
	if visibleContentLines < 1 {
		visibleContentLines = 1
	}

	columns := make([]string, 0, visible)
	for c := first; c < first+visible; c++ {
		f := flows[c]
		color := flowColumnColor(c)
		header := fmt.Sprintf("%s %s", flowColumnMarker(c), strings.ToUpper(f.Display(m.mainBranch())))

		lines := []string{ui.SectionHeader(header, color), ""}
		highlightedLine := -1

		count := 0
		for _, i := range m.getFilteredMergePRs(c) {
			pr := m.merge.prs[i]
			selected := false
			if i < len(m.merge.selected) {
				selected = m.merge.selected[i]
			}
			highlighted := m.merge.column == c && m.mergeCursor(c) == count
			if highlighted {
				highlightedLine = len(lines)
			}
			lines = append(lines, ui.PRListItem(pr.Repo.ShortName(), pr.PrNumber, selected, highlighted, color))
			count++
		}
		if count == 0 {
			lines = append(lines, ui.Dim.Render("  No open PRs"))
		}

		// Apply viewport scrolling to keep highlighted item visible
		content := applyViewportScroll(lines, headerLines, highlightedLine, visibleContentLines)
		columns = append(columns, ui.ColumnBox(content, "", color, m.merge.column == c, columnWidth, columnHeight))
	}

	out := titleBox + "\n" + ui.JoinColumns(columns, gap)

	// Note any repos that failed to fetch, so they aren't silently missing.
	if n := len(m.merge.repoErrors); n > 0 {
		warnStyle := ui.Red
		centeredStyle := lipgloss.NewStyle().Width(m.contentWidth()).Align(lipgloss.Center)
		out += "\n" + centeredStyle.Render(warnStyle.Render(
			fmt.Sprintf("⚠ %d repo(s) could not be checked and are not listed", n)))
	}

	return out
}

// minMergeColumnWidth is the narrowest a step's column can usefully be. A row
// is a checkbox, a shortened repo name and a PR number, and the header is the
// step itself — below about this width both start losing their tails, which is
// worse than showing fewer steps at once.
const minMergeColumnWidth = 26

// columnWidthFor divides the content width between n columns and the gaps
// between them.
func columnWidthFor(contentWidth, n, gap int) int {
	if n < 1 {
		return contentWidth
	}
	return (contentWidth - 2 - gap*n) / n
}

func (m Model) renderMergeConfirmation() string {
	var lines []string

	lines = append(lines, ui.SectionHeader("Confirm Merge", ui.ColorMagenta))
	lines = append(lines, "")

	selected := 0
	for _, s := range m.merge.selected {
		if s {
			selected++
		}
	}

	lines = append(lines, fmt.Sprintf("   PRs to merge: %d", selected))
	lines = append(lines, "")

	if m.dryRun {
		warningStyle := ui.YellowBold
		lines = append(lines, warningStyle.Render("   ⚠ DRY RUN: No actual changes will be made"))
		lines = append(lines, "")
	}

	lines = append(lines, ui.YesNoButtons(m.confirmSelection))

	return strings.Join(lines, "\n")
}

func (m Model) renderMerging() string {
	var lines []string

	lines = append(lines, ui.SectionHeader("Merging PRs", ui.ColorMagenta))
	lines = append(lines, "")

	spinner := ui.Spinner(m.spinnerFrame)
	spinnerStyle := ui.Yellow
	statusStyle := ui.Magenta

	lines = append(lines, fmt.Sprintf("   %s %s",
		spinnerStyle.Render(spinner),
		statusStyle.Render("Merging PRs..."),
	))

	return strings.Join(lines, "\n")
}

func (m Model) renderMergeSummaryWithHeight(availableHeight int) string {
	var lines []string

	// Count successes and failures
	successCount := 0
	failCount := 0
	for _, result := range m.merge.results {
		if result.Success {
			successCount++
		} else {
			failCount++
		}
	}

	// Header color based on overall result
	headerColor := ui.ColorGreen
	if failCount > 0 {
		headerColor = ui.ColorYellow
	}

	lines = append(lines, ui.SectionHeader("Merge Results", headerColor))
	lines = append(lines, "")

	// Summary counts
	successStyle := ui.Green
	failStyle := ui.Red
	lines = append(lines, fmt.Sprintf("   %s %d succeeded  %s %d failed",
		successStyle.Render("✓"),
		successCount,
		failStyle.Render("✗"),
		failCount,
	))
	lines = append(lines, "")

	// Individual results
	for _, result := range m.merge.results {
		var icon string
		var iconStyle lipgloss.Style
		if result.Success {
			icon = "✓"
			iconStyle = ui.Green
		} else {
			icon = "✗"
			iconStyle = ui.Red
		}

		repoStyle := ui.WhiteBold
		dimStyle := ui.Dim

		lines = append(lines, fmt.Sprintf("   %s %s %s",
			iconStyle.Render(icon),
			repoStyle.Render(result.RepoName),
			dimStyle.Render(fmt.Sprintf("#%d", result.PrNumber)),
		))
	}

	// QA tag results
	if len(m.qa.results) > 0 {
		lines = append(lines, "")
		lines = append(lines, m.renderQaTagResults()...)
	}

	content := strings.Join(lines, "\n")

	// Fixed box width for stable layout
	boxWidth := m.contentWidth() - 10

	return ui.ColumnBox(content, " Merge Summary ", headerColor, true, boxWidth, availableHeight)
}
