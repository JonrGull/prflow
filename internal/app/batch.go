package app

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/git"
	"github.com/JonrGull/prflow/internal/github"
	"github.com/JonrGull/prflow/internal/models"
	"github.com/JonrGull/prflow/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Batch mode: pick repos across two columns, confirm, then create or update a
// release PR in each.
//
// The concurrency lives here too. Repo discovery and commit-loading run in
// parallel and stream results back, but the PRs themselves are created one at a
// time — GitHub rate-limits, and a half-finished parallel batch is much harder
// to reason about than a slow sequential one.

// batchState is everything batch mode owns.
type batchState struct {
	repos       []models.RepoInfo
	repoCommits []*[]models.CommitInfo // per repo: nil=loading, empty=nothing to merge
	repoErrs    []string               // per repo: why its fetch failed, "" if it didn't
	selected    []bool
	results     []models.BatchResult

	// Background commit fetching. It runs until the flow ends, not until you
	// leave the repo list: Esc from the title input comes back to it.
	fetchCancel  func()
	resultsChan  chan batchRepoCommitResult
	fetchPending int  // repos still fetching
	waiting      bool // Enter pressed with selected repos still fetching

	filter  string
	column  int // 0=left, 1=right
	feIndex int
	beIndex int

	confirmScroll    int // scroll offset in the confirmation's right column
	existingPRs      int // repos whose PR already exists, so it is updated
	reposWithCommits int

	// Progress while the batch runs.
	current      int
	currentRepo  string
	currentStep  string // e.g. "Fetching branches..."
	total        int
	progressChan chan string
}

// scrollBatchConfirm moves the Changes column by delta, up to the last line.
// It measures with the renderer's own builders: a separate line count and a
// height guessed from the terminal stopped it about nine lines short, so the
// last tickets could never be seen.
func (m *Model) scrollBatchConfirm(delta int) {
	_, _, available := m.chrome()
	_, leftHeight := m.batchConfirmLeft(available)
	maxScroll := max(len(m.batchConfirmRightLines())-batchConfirmContentHeight(leftHeight), 0)
	m.batch.confirmScroll = min(max(m.batch.confirmScroll+delta, 0), maxScroll)
}

type batchRepoResult struct {
	result models.BatchResult
}

func (batchRepoResult) flowResult() {}

type batchCommitsResult struct {
	tickets          []string
	existingPRs      int // Count of repos with existing PRs
	reposWithCommits int // Count of repos that have commits to merge
	err              error
}

func (batchCommitsResult) flowResult() {}

// batchProgressMsg is sent for real-time progress updates during batch processing
type batchProgressMsg struct {
	step string
}

func (batchProgressMsg) flowResult() {}

// listenForProgress creates a subscription that listens to the progress channel
func listenForProgress(ch chan string) tea.Cmd {
	return func() tea.Msg {
		if ch == nil {
			return nil
		}
		step, ok := <-ch
		if !ok {
			return nil
		}
		return batchProgressMsg{step: step}
	}
}

func fetchBatchCommitsCmd(repos []models.RepoInfo, selected []bool, cachedCommits []*[]models.CommitInfo, flow *models.Flow, dryRun bool) tea.Cmd {
	return func() tea.Msg {
		if dryRun {
			return dryRunBatchCommits(repos, selected)
		}

		if flow == nil {
			return batchCommitsResult{err: nil}
		}

		// Collect selected repos with their cached commits
		type selectedRepo struct {
			repo    models.RepoInfo
			commits []models.CommitInfo
		}
		var selectedRepos []selectedRepo
		for i, repo := range repos {
			if i < len(selected) && selected[i] {
				var commits []models.CommitInfo
				if i < len(cachedCommits) && cachedCommits[i] != nil {
					commits = *cachedCommits[i]
				}
				selectedRepos = append(selectedRepos, selectedRepo{repo: repo, commits: commits})
			}
		}

		if len(selectedRepos) == 0 {
			return batchCommitsResult{tickets: nil}
		}

		// Only check for existing PRs in parallel (commits already cached)
		type repoResult struct {
			hasExisting bool
		}
		results := make(chan repoResult, len(selectedRepos))

		var wg sync.WaitGroup
		for _, sr := range selectedRepos {
			wg.Add(1)
			go func(r models.RepoInfo) {
				defer wg.Done()

				headBranch := flow.HeadBranch()
				baseBranch := flow.BaseBranch(r.MainBranch)

				// Check for existing PR (no need to re-fetch commits)
				existingPR, _ := github.GetExistingPR(r.Path, headBranch, baseBranch)

				results <- repoResult{hasExisting: existingPR != nil}
			}(sr.repo)
		}

		// Close channel when done
		go func() {
			wg.Wait()
			close(results)
		}()

		// Aggregate tickets from cached commits and count existing PRs
		ticketSet := make(map[string]bool)
		withCommitsCount := 0
		for _, sr := range selectedRepos {
			tickets := git.GetAllTickets(sr.commits)
			for _, t := range tickets {
				ticketSet[t] = true
			}
			if len(sr.commits) > 0 {
				withCommitsCount++
			}
		}

		existingCount := 0
		for res := range results {
			if res.hasExisting {
				existingCount++
			}
		}

		var allTickets []string
		for t := range ticketSet {
			allTickets = append(allTickets, t)
		}

		return batchCommitsResult{tickets: allTickets, existingPRs: existingCount, reposWithCommits: withCommitsCount}
	}
}

func startBatchProcessingCmd(m *Model, repoIndex int) tea.Cmd {
	// Everything the command needs is read here, on the UI goroutine. It runs
	// on another one, where reading m raced with Update; startMergingCmd was
	// changed to take values for the same reason.
	if repoIndex >= len(m.batch.repos) {
		return nil
	}
	repo := m.batch.repos[repoIndex]
	selected := repoIndex < len(m.batch.selected) && m.batch.selected[repoIndex]
	progressCh := m.batch.progressChan
	dryRun := m.dryRun
	var flow *models.Flow
	if m.flow != nil {
		f := *m.flow
		flow = &f
	}
	title := m.prTitle
	ticketRegex := m.config.TicketRegex()
	linearOrg := m.config.Tickets.LinearOrg

	return func() tea.Msg {
		if !selected {
			// Skip unselected repos
			return batchRepoResult{result: models.BatchResult{
				Repo:   repo,
				Status: models.Skipped("Not selected"),
			}}
		}

		if dryRun {
			sendProgress(progressCh, "Simulating PR creation...")
			return dryRunBatchRepoResult(repo)
		}

		// Use the selected PR type
		if flow == nil {
			return batchRepoResult{result: models.BatchResult{
				Repo:   repo,
				Status: models.Failed("No PR type selected"),
			}}
		}
		flow := *flow
		headBranch := flow.HeadBranch()
		baseBranch := flow.BaseBranch(repo.MainBranch)

		// Fetch branches
		sendProgress(progressCh, "Fetching branches...")
		if err := git.FetchBranches(repo.Path, []string{headBranch, baseBranch}); err != nil {
			return batchRepoResult{result: models.BatchResult{
				Repo:   repo,
				Status: models.Failed(err.Error()),
			}}
		}

		// Get commits
		sendProgress(progressCh, "Getting commits...")
		commits, err := git.GetCommitsBetween(repo.Path, baseBranch, headBranch, ticketRegex)
		if err != nil {
			return batchRepoResult{result: models.BatchResult{
				Repo:   repo,
				Status: models.Failed(err.Error()),
			}}
		}

		if len(commits) == 0 {
			return batchRepoResult{result: models.BatchResult{
				Repo:   repo,
				Status: models.Skipped("No commits to merge"),
			}}
		}

		tickets := git.GetAllTickets(commits)

		// Create or update PR
		sendProgress(progressCh, "Creating PR...")
		pr, updated, err := github.CreateOrUpdatePR(repo.Path, headBranch, baseBranch, title, tickets, linearOrg)
		if err != nil {
			return batchRepoResult{result: models.BatchResult{
				Repo:   repo,
				Status: models.Failed(err.Error()),
			}}
		}

		status := models.Created
		if updated {
			status = models.Updated
		}

		return batchRepoResult{result: models.BatchResult{
			Repo:     repo,
			Status:   status,
			PrURL:    &pr.URL,
			PrNumber: pr.Number,
			Tickets:  tickets,
		}}
	}
}

// Loading repos and their commits happens in parallel and streams back, so it
// has its own result types rather than one batch-shaped message.
type batchReposLoadedResult struct {
	repos      []models.RepoInfo
	cancelFunc func() // Cancel function for background fetch
	err        error
}

func (batchReposLoadedResult) flowResult() {}

// abandon stops the background fetch this result started: dropped as stale,
// it is the only thing holding the cancel func.
func (r batchReposLoadedResult) abandon() {
	if r.cancelFunc != nil {
		r.cancelFunc()
	}
}

// Single repo commit fetch result (sent incrementally from background)
type batchRepoCommitResult struct {
	index   int
	commits []models.CommitInfo
	err     error
}

func (batchRepoCommitResult) flowResult() {}

// loadBatchReposCmd loads repos and starts background commit fetching
func loadBatchReposCmd(cfg *config.Config, flow *models.Flow, dryRun bool, resultsChan chan batchRepoCommitResult) tea.Cmd {
	return func() tea.Msg {
		repos, err := discoverRepos(cfg)
		if err != nil {
			return batchReposLoadedResult{err: err}
		}

		if len(repos) == 0 {
			return batchReposLoadedResult{repos: repos}
		}

		// Create cancellation context
		ctx, cancel := context.WithCancel(context.Background())

		// Start background fetches for all repos
		go func() {
			defer close(resultsChan) // Close channel when all workers done or cancelled

			var wg sync.WaitGroup
			for i, repo := range repos {
				wg.Add(1)
				go func(idx int, r models.RepoInfo) {
					defer wg.Done()

					// Check for cancellation
					select {
					case <-ctx.Done():
						return
					default:
					}

					var commits []models.CommitInfo
					var err error

					if dryRun {
						commits = dryRunRepoCommits(idx)
					} else if flow != nil {
						headBranch := flow.HeadBranch()
						baseBranch := flow.BaseBranch(r.MainBranch)

						// Fetch from remote (network call). A failure is
						// reported, not shown as "nothing to merge".
						err = git.FetchBranches(r.Path, []string{headBranch, baseBranch})
						if err == nil {
							commits, err = git.GetCommitsBetween(r.Path, baseBranch, headBranch, cfg.TicketRegex())
						}
					}

					// Send result (check cancellation again)
					select {
					case <-ctx.Done():
						return
					case resultsChan <- batchRepoCommitResult{index: idx, commits: commits, err: err}:
					}
				}(i, repo)
			}
			wg.Wait()
		}()

		return batchReposLoadedResult{repos: repos, cancelFunc: cancel}
	}
}

// listenForBatchCommits creates a command that listens for commit results
func listenForBatchCommits(resultsChan chan batchRepoCommitResult) tea.Cmd {
	if resultsChan == nil {
		return nil // receiving from nil blocks forever
	}
	return func() tea.Msg {
		result, ok := <-resultsChan
		if !ok {
			return nil // Channel closed
		}
		return result
	}
}

func (m Model) handleBatchReposLoaded(msg batchReposLoadedResult) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.errorMessage = msg.err.Error()
		m.screen = ScreenError
		return m, nil
	}

	m.batch.repos = msg.repos
	m.batch.repoCommits = make([]*[]models.CommitInfo, len(msg.repos)) // All nil = loading
	m.batch.repoErrs = make([]string, len(msg.repos))
	m.batch.selected = make([]bool, len(msg.repos))
	m.batch.fetchCancel = msg.cancelFunc
	m.batch.fetchPending = len(msg.repos)
	m.screen = ScreenBatchRepoSelect
	m.batch.column = 0
	m.batch.feIndex = 0
	m.batch.beIndex = 0

	// Start listening for commit results
	if len(msg.repos) > 0 && m.batch.resultsChan != nil {
		return m, listenForBatchCommits(m.batch.resultsChan)
	}
	return m, nil
}

func (m Model) handleBatchRepoCommitResult(msg batchRepoCommitResult) (tea.Model, tea.Cmd) {
	// Update commits for this repo
	if msg.index >= 0 && msg.index < len(m.batch.repoCommits) {
		commits := msg.commits // Make a copy to get a stable pointer
		m.batch.repoCommits[msg.index] = &commits
		if msg.err != nil && msg.index < len(m.batch.repoErrs) {
			m.batch.repoErrs[msg.index] = msg.err.Error()
		}
	}

	m.batch.fetchPending--

	// Exactly one listener is out while fetches are, on every screen of the
	// flow. Listening only on the repo list used to strand a repo that was
	// still loading when you moved on: back on the list it spun forever, and
	// selecting it hung on "Waiting for 1 repo(s)".
	listen := tea.Cmd(nil)
	if m.batch.fetchPending > 0 {
		listen = listenForBatchCommits(m.batch.resultsChan)
	}

	if m.batch.waiting && m.screen == ScreenLoading {
		if n := m.selectedStillLoading(); n > 0 {
			m.loadingMessage = fmt.Sprintf("Waiting for %d repo(s) to finish...", n)
			return m, listen
		}
		m.batch.waiting = false
		m.loadingMessage = "Checking for existing PRs..."
		return m, tea.Batch(listen, fetchBatchCommitsCmd(m.batch.repos, m.batch.selected, m.batch.repoCommits, m.flow, m.dryRun))
	}
	return m, listen
}

// selectedStillLoading counts selected repos whose commits haven't arrived.
func (m Model) selectedStillLoading() int {
	n := 0
	for i, selected := range m.batch.selected {
		if selected && i < len(m.batch.repoCommits) && m.batch.repoCommits[i] == nil {
			n++
		}
	}
	return n
}

func (m Model) handleBatchCommitsResult(msg batchCommitsResult) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.errorMessage = msg.err.Error()
		m.screen = ScreenError
		return m, nil
	}

	m.tickets = msg.tickets
	m.batch.existingPRs = msg.existingPRs
	m.batch.reposWithCommits = msg.reposWithCommits
	m.screen = ScreenTitleInput
	return m, nil
}

func (m Model) handleBatchRepoResult(msg batchRepoResult) (tea.Model, tea.Cmd) {
	// Only add non-skipped "not selected" results to keep summary clean
	if !models.IsStatusSkipped(msg.result.Status) || models.GetStatusReason(msg.result.Status) != "Not selected" {
		m.batch.results = append(m.batch.results, msg.result)
	}

	// Add successful PRs to session history
	if models.IsStatusSuccess(msg.result.Status) && msg.result.PrURL != nil && m.flow != nil {
		action := "created"
		if models.IsStatusUpdated(msg.result.Status) {
			action = "updated"
		}
		m.recordSessionPR(msg.result.Repo.DisplayName, *msg.result.PrURL, m.flow.Display(msg.result.Repo.MainBranch), action, msg.result.PrNumber)
	}
	m.batch.current++

	// Process all repos, not just selected count
	if m.batch.current >= len(m.batch.repos) {
		m.batch.currentRepo = ""
		m.batch.currentStep = ""
		// Close progress channel
		if m.batch.progressChan != nil {
			close(m.batch.progressChan)
			m.batch.progressChan = nil
		}
		m.screen = ScreenBatchSummary
		m.menuIndex = 0
		// Spawn confetti if any successes
		for _, result := range m.batch.results {
			if models.IsStatusSuccess(result.Status) {
				m.spawnConfetti()
				break
			}
		}
		return m, nil
	}

	// Set next repo name, clear step, and start processing
	m.batch.currentRepo = m.batch.repos[m.batch.current].DisplayName
	m.batch.currentStep = ""
	return m, tea.Batch(
		startBatchProcessingCmd(&m, m.batch.current),
		listenForProgress(m.batch.progressChan),
	)
}

// batchDefaultBranch names the default branch for a batch's title and
// diagram: the selected repos' own, when they share one. It was always "main",
// so a batch of master repos was titled and drawn as merging into main.
func (m Model) batchDefaultBranch() string {
	branch := ""
	for i, repo := range m.batch.repos {
		if i >= len(m.batch.selected) || !m.batch.selected[i] {
			continue
		}
		if branch != "" && repo.MainBranch != branch {
			return "default branch"
		}
		branch = repo.MainBranch
	}
	if branch == "" {
		return "default branch"
	}
	return branch
}

// cancelBatchFetch cancels background fetches (channel is closed by sender goroutine)
func (m *Model) cancelBatchFetch() {
	if m.batch.fetchCancel != nil {
		m.batch.fetchCancel()
		m.batch.fetchCancel = nil
	}
	m.batch.resultsChan = nil
	m.batch.fetchPending = 0
	m.batch.waiting = false
}

func (m Model) startBatchProcessing() (tea.Model, tea.Cmd) {
	m.batch.total = 0
	for _, selected := range m.batch.selected {
		if selected {
			m.batch.total++
		}
	}
	m.batch.current = 0
	m.batch.results = nil
	m.batch.currentRepo = m.batch.repos[0].DisplayName
	m.batch.currentStep = ""
	m.batch.progressChan = make(chan string, 1)
	m.screen = ScreenBatchProcessing
	return m, tea.Batch(
		startBatchProcessingCmd(&m, 0),
		listenForProgress(m.batch.progressChan),
	)
}

func (m Model) handleBatchRepoSelectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyUp:
		m.navigateBatchColumn(true)
	case tea.KeyDown:
		m.navigateBatchColumn(false)
	case tea.KeyLeft:
		if m.batch.column != 0 {
			filtered := m.getFilteredBatchRepos(0)
			if len(filtered) > 0 {
				m.batch.column = 0
				// Clamp index to valid range
				if m.batch.feIndex >= len(filtered) {
					m.batch.feIndex = len(filtered) - 1
				}
			}
		}
	case tea.KeyRight:
		if m.batch.column != 1 {
			filtered := m.getFilteredBatchRepos(1)
			if len(filtered) > 0 {
				m.batch.column = 1
				// Clamp index to valid range
				if m.batch.beIndex >= len(filtered) {
					m.batch.beIndex = len(filtered) - 1
				}
			}
		}
	case tea.KeySpace:
		m.toggleBatchSelection()
	case tea.KeyTab, tea.KeyEnter:
		// Count selected - do nothing if none selected
		count := 0
		for _, selected := range m.batch.selected {
			if selected {
				count++
			}
		}
		if count == 0 {
			return m, nil
		}
		if m.flow != nil {
			m.prTitle = m.flow.DefaultTitle(m.batchDefaultBranch())
		}
		// The fetch keeps running either way; its result handler moves on
		// once the last selected repo arrives.
		if n := m.selectedStillLoading(); n > 0 {
			m.batch.waiting = true
			m.screen = ScreenLoading
			m.loadingMessage = fmt.Sprintf("Waiting for %d repo(s) to finish...", n)
			return m, nil
		}
		// Go to loading screen to check for existing PRs (commits already cached)
		m.screen = ScreenLoading
		m.loadingMessage = "Checking for existing PRs..."
		return m, fetchBatchCommitsCmd(m.batch.repos, m.batch.selected, m.batch.repoCommits, m.flow, m.dryRun)
	case tea.KeyEsc:
		// Cancel background fetches and close channel
		m.cancelBatchFetch()
		m.screen = ScreenPrTypeSelect
		m.flow = nil
		m.menuIndex = 0
	case tea.KeyBackspace:
		if len(m.batch.filter) > 0 {
			m.batch.filter = trimLastRune(m.batch.filter)
			m.batch.feIndex = 0
			m.batch.beIndex = 0
		}
	case tea.KeyCtrlC:
		m.shouldQuit = true
		return m, tea.Quit
	case tea.KeyRunes:
		// Type to filter - all printable characters go to filter
		m.batch.filter += typedText(msg.Runes)
		m.batch.feIndex = 0
		m.batch.beIndex = 0
	}
	return m, nil
}

// getFilteredBatchRepos returns indices of repos matching the current filter for the given column (0=frontend, 1=backend)
func (m *Model) getFilteredBatchRepos(column int) []int {
	var indices []int
	filter := strings.ToLower(m.batch.filter)

	for i, repo := range m.batch.repos {
		if !repo.InColumn(column, m.leftGroups()) {
			continue
		}
		if filter != "" && !strings.Contains(strings.ToLower(repo.DisplayName), filter) {
			continue
		}
		indices = append(indices, i)
	}

	return indices
}

func (m *Model) navigateBatchColumn(up bool) {
	filtered := m.getFilteredBatchRepos(m.batch.column)
	if m.batch.column == 0 {
		navigateColumnIndex(&m.batch.feIndex, len(filtered), up)
	} else {
		navigateColumnIndex(&m.batch.beIndex, len(filtered), up)
	}
}

func (m *Model) toggleBatchSelection() {
	filtered := m.getFilteredBatchRepos(m.batch.column)
	currentIdx := m.batch.feIndex
	if m.batch.column == 1 {
		currentIdx = m.batch.beIndex
	}
	toggleSelection(m.batch.selected, filtered, currentIdx)
}

func (m Model) handleBatchSummaryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.shouldQuit = true
		return m, tea.Quit
	case "m":
		return m.navigateToMergePRs()
	case "up":
		if m.menuIndex > 0 {
			m.menuIndex--
		}
	case "down":
		if m.menuIndex < len(m.batch.results)-1 {
			m.menuIndex++
		}
	case "o":
		m.openInBrowser(urls(m.batchPRLinks())...)
	case "c":
		m.copyLinks(m.batchPRLinks(), "Copied URLs!")
		return m, nil
	case "enter", "esc":
		return m.reset()
	}
	return m, nil
}

// batchColumnStyle holds the per-column presentation that the repo list varies.
var batchColumnStyle = [2]struct {
	Icon  string
	Color lipgloss.TerminalColor
}{
	0: {"●", ui.ColorCyan},
	1: {"◆", ui.ColorMagenta},
}

// renderBatchRepoColumn builds one column of the batch repo selector, and
// reports which line is highlighted and which repo that is.
//
// The two columns were written out longhand, 48 lines each, differing only in
// which index and colour they used — so a fix to the group-header or
// nested-repo logic had to be made twice, correctly, both times.
func (m Model) renderBatchRepoColumn(col int, filtered []int) (lines []string, highlightedLine, highlightedRepoIdx int) {
	highlightedLine, highlightedRepoIdx = -1, -1

	style := batchColumnStyle[col]
	groups := m.config.Columns.Left
	cursor := m.batch.feIndex
	if col == 1 {
		groups = m.config.Columns.Right
		cursor = m.batch.beIndex
	}

	lines = append(lines,
		ui.SectionHeader(fmt.Sprintf("%s  %s (%d)",
			style.Icon, strings.ToUpper(m.config.ColumnName(col)), len(filtered)), style.Color),
		"")

	if len(filtered) == 0 {
		return append(lines, ui.Dim.Render("  No repos found")), highlightedLine, highlightedRepoIdx
	}

	// Group sub-headers only make sense when a column holds several groups.
	multiGroup := len(groups) > 1
	var currentGroup string
	var currentParent *string

	for i, repoIdx := range filtered {
		repo := m.batch.repos[repoIdx]

		// Show group sub-header when the group changes.
		if m.batch.filter == "" && multiGroup && repo.Group != currentGroup {
			if currentGroup != "" {
				lines = append(lines, "") // gap between groups
			}
			lines = append(lines, ui.ParentHeader(repo.Group))
			currentGroup = repo.Group
			currentParent = nil // reset parent tracking for the new group
		}

		// Show parent header when the parent changes (not while filtering).
		if m.batch.filter == "" && !ptrEqual(repo.ParentRepo, currentParent) {
			if repo.ParentRepo != nil {
				lines = append(lines, ui.ParentHeader(*repo.ParentRepo))
			}
			currentParent = repo.ParentRepo
		}

		selected := repoIdx < len(m.batch.selected) && m.batch.selected[repoIdx]
		highlighted := m.batch.column == col && cursor == i
		if highlighted {
			highlightedLine = len(lines)
			highlightedRepoIdx = repoIdx
		}

		commitCount := ui.CommitsLoading
		if m.batchRepoErr(repoIdx) != "" {
			commitCount = ui.CommitsFailed
		} else if repoIdx < len(m.batch.repoCommits) && m.batch.repoCommits[repoIdx] != nil {
			commitCount = len(*m.batch.repoCommits[repoIdx])
		}

		indent := ""
		if repo.ParentRepo != nil {
			indent = "│ "
		}
		lines = append(lines, ui.RepoListItemWithCommits(
			repo.ShortName(), selected, highlighted, style.Color, indent, commitCount, m.spinnerFrame))
	}

	return lines, highlightedLine, highlightedRepoIdx
}

func (m Model) renderBatchRepoSelectWithHeight(availableHeight int) string {
	selectedCount := 0
	for _, s := range m.batch.selected {
		if s {
			selectedCount++
		}
	}

	// Fixed column width for stable layout
	columnWidth := (m.contentWidth() - 6) / 2

	// Reserve space for commits panel (5 lines) + filter box (4 lines) + gaps (4)
	commitsHeight := 5
	columnHeight := availableHeight - commitsHeight - 8
	if columnHeight < 5 {
		columnHeight = 5
	}

	// Filter width matches the two columns + gap
	filterWidth := columnWidth*2 + 2

	// Filter input at top
	title := fmt.Sprintf("Select Repositories (%d/%d)", selectedCount, len(m.batch.repos))
	filterBox := ui.FilterInput(m.batch.filter, title, ui.ColorWhite, filterWidth)

	// Get filtered repos for each column
	feFiltered := m.getFilteredBatchRepos(0)
	beFiltered := m.getFilteredBatchRepos(1)

	// Track highlighted repo index for commits panel
	var highlightedRepoIdx int = -1

	// Build both columns. The two used to be written out longhand, 48 lines
	// each, differing only in which column index they read.
	feLines, feHighlightedLine, feRepoIdx := m.renderBatchRepoColumn(0, feFiltered)
	beLines, beHighlightedLine, beRepoIdx := m.renderBatchRepoColumn(1, beFiltered)

	highlightedRepoIdx = feRepoIdx
	if beRepoIdx >= 0 {
		highlightedRepoIdx = beRepoIdx
	}

	// Apply viewport scrolling to keep highlighted item visible
	// Keep 2-line header, scroll the rest
	headerLines := 2
	visibleContentLines := columnHeight - headerLines
	if visibleContentLines < 1 {
		visibleContentLines = 1
	}

	feContent := applyViewportScroll(feLines, headerLines, feHighlightedLine, visibleContentLines)
	beContent := applyViewportScroll(beLines, headerLines, beHighlightedLine, visibleContentLines)

	feColumn := ui.ColumnBox(feContent, "", ui.ColorCyan, m.batch.column == 0, columnWidth, columnHeight)
	beColumn := ui.ColumnBox(beContent, "", ui.ColorMagenta, m.batch.column == 1, columnWidth, columnHeight)

	columns := ui.TwoColumns(feColumn, beColumn, 2)

	// Build commits preview panel for highlighted repo
	commitsPanel := m.renderCommitsPreview(highlightedRepoIdx, filterWidth)

	return filterBox + "\n\n" + columns + "\n" + commitsPanel
}

// renderCommitsPreview renders a preview of commits for the given repo index
// maxPreviewErrLines caps a fetch error in the preview at the height of its
// commit list: git can print a screenful.
const maxPreviewErrLines = 4

// batchRepoErr is why repo i's fetch failed, or "" if it didn't.
func (m Model) batchRepoErr(i int) string {
	if i >= 0 && i < len(m.batch.repoErrs) {
		return m.batch.repoErrs[i]
	}
	return ""
}

func (m Model) renderCommitsPreview(repoIdx int, width int) string {
	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.ColorDarkGray).
		Width(width).
		Padding(0, 1)

	if repoIdx < 0 || repoIdx >= len(m.batch.repos) {
		dimStyle := ui.Dim
		return borderStyle.Render(dimStyle.Render("No repo selected"))
	}

	repo := m.batch.repos[repoIdx]

	// Check if still loading
	isLoading := repoIdx >= len(m.batch.repoCommits) || m.batch.repoCommits[repoIdx] == nil

	// Build content
	var lines []string

	// Header with repo name
	repoName := repo.DisplayName
	if idx := strings.LastIndex(repoName, "/"); idx != -1 {
		repoName = repoName[idx+1:]
	}
	headerStyle := ui.YellowBold

	if isLoading {
		// Show loading state
		spinner := ui.Spinner(m.spinnerFrame)
		spinnerStyle := ui.Yellow
		lines = append(lines, headerStyle.Render(repoName)+" "+spinnerStyle.Render(spinner+" fetching..."))
		dimStyle := ui.Dim
		lines = append(lines, dimStyle.Render("  Checking for commits..."))
	} else if errText := m.batchRepoErr(repoIdx); errText != "" {
		lines = append(lines, headerStyle.Render(repoName)+" "+ui.Red.Render("✗ fetch failed"))
		errLines := wrapToWidth(errText, width-4, "  ")
		if len(errLines) > maxPreviewErrLines {
			errLines = append(errLines[:maxPreviewErrLines-1], "  …")
		}
		for _, l := range errLines {
			lines = append(lines, ui.Dim.Render(l))
		}
	} else {
		commits := *m.batch.repoCommits[repoIdx]
		countStyle := ui.Cyan
		lines = append(lines, headerStyle.Render(repoName)+" "+countStyle.Render(fmt.Sprintf("(%d commits)", len(commits))))

		if len(commits) == 0 {
			dimStyle := ui.Dim
			lines = append(lines, dimStyle.Render("  No commits to merge - branches are up to date"))
		} else {
			// Show first 3 commits
			hashStyle := ui.Magenta
			msgStyle := ui.White
			ticketStyle := ui.Yellow
			ticketRegex := m.config.TicketRegex()

			maxCommits := 3
			for i, commit := range commits {
				if i >= maxCommits {
					remaining := len(commits) - maxCommits
					dimStyle := ui.Dim
					lines = append(lines, dimStyle.Render(fmt.Sprintf("  ... and %d more", remaining)))
					break
				}
				// Truncate message to fit
				msg := commit.Message
				maxMsgLen := width - 15 // room for hash and padding
				if len(msg) > maxMsgLen {
					msg = msg[:maxMsgLen-3] + "..."
				}
				// Highlight tickets in message
				styledMsg := git.HighlightTickets(msg, ticketRegex, func(t string) string { return ticketStyle.Render(t) })
				lines = append(lines, fmt.Sprintf("  %s %s", hashStyle.Render(commit.Hash), msgStyle.Render(styledMsg)))
			}
		}
	}

	return borderStyle.Render(strings.Join(lines, "\n"))
}

func (m Model) renderBatchConfirmationWithHeight(availableHeight int) string {
	leftContent, leftHeight := m.batchConfirmLeft(availableHeight)
	rightLines := m.batchConfirmRightLines()
	dimStyle := ui.Dim

	// Apply scrolling to right column - constrain to left column height
	contentHeight := batchConfirmContentHeight(leftHeight)
	totalLines := len(rightLines)
	maxScroll := max(totalLines-contentHeight, 0)

	// Clamp scroll offset
	scrollOffset := m.batch.confirmScroll
	if scrollOffset > maxScroll {
		scrollOffset = maxScroll
	}
	if scrollOffset < 0 {
		scrollOffset = 0
	}

	// Get visible window of lines with consistent height
	var visibleLines []string
	visibleLines = append(visibleLines, "") // Top padding

	// Always reserve space for scroll up indicator
	if scrollOffset > 0 {
		visibleLines = append(visibleLines, dimStyle.Render("  ↑ more above"))
	} else {
		visibleLines = append(visibleLines, "") // Empty line to maintain height
	}

	endIdx := scrollOffset + contentHeight
	if endIdx > totalLines {
		endIdx = totalLines
	}
	if scrollOffset < totalLines {
		visibleLines = append(visibleLines, rightLines[scrollOffset:endIdx]...)
	}

	// Pad to consistent height
	for len(visibleLines) < contentHeight+2 {
		visibleLines = append(visibleLines, "")
	}

	// Always reserve space for scroll down indicator
	if endIdx < totalLines {
		visibleLines = append(visibleLines, dimStyle.Render("  ↓ more below"))
	} else {
		visibleLines = append(visibleLines, "") // Empty line to maintain height
	}

	rightTitleStyle := ui.MagentaBold
	rightContent := panel(rightTitleStyle, "Changes", visibleLines)

	return ui.UnifiedPanel(leftContent, rightContent, 50, 45, ui.ColorCyan)
}

// batchConfirmContentHeight is how many Changes lines fit beside a left
// column of leftHeight: less the title, the padding row and the two rows kept
// for the "more above/below" indicators.
func batchConfirmContentHeight(leftHeight int) int {
	return max(max(leftHeight-2, 5)-2, 3)
}

// batchConfirmLeft builds the confirmation's left column, and its height.
func (m Model) batchConfirmLeft(availableHeight int) (content string, height int) {
	selectedCount := 0
	for _, s := range m.batch.selected {
		if s {
			selectedCount++
		}
	}

	// Calculate dynamic limit for left column repos based on available height
	maxReposLeft := (availableHeight - 12) / 1
	if maxReposLeft < 3 {
		maxReposLeft = 3
	} else if maxReposLeft > 10 {
		maxReposLeft = 10
	}

	// Get selected repo names
	var selectedRepos []string
	for i, repo := range m.batch.repos {
		if i < len(m.batch.selected) && m.batch.selected[i] {
			selectedRepos = append(selectedRepos, repo.ShortName())
		}
	}

	// Build left column (PR details & repos)
	var leftLines []string
	leftLines = append(leftLines, "")

	// Branch flow diagram
	if m.flow != nil {
		base := m.flow.BaseBranch(m.batchDefaultBranch())
		leftLines = append(leftLines, ui.BranchFlowDiagram(m.flow.HeadBranch(), base, m.branchColor(m.flow.HeadBranch()), m.branchColor(base)))
		leftLines = append(leftLines, "")
	}

	leftLines = append(leftLines, ui.SectionHeader("PR DETAILS", ui.ColorCyan))
	leftLines = append(leftLines, "")

	labelStyle := ui.White
	titleStyle := ui.WhiteBold
	leftLines = append(leftLines, fmt.Sprintf("  %s %s", labelStyle.Render("Title:"), titleStyle.Render(m.prTitle)))
	leftLines = append(leftLines, "")

	// Repos section
	leftLines = append(leftLines, ui.SectionHeader(fmt.Sprintf("REPOSITORIES (%d)", selectedCount), ui.ColorMagenta))
	leftLines = append(leftLines, "")

	// List selected repos (dynamic limit based on height)
	for i, name := range selectedRepos {
		if i >= maxReposLeft {
			remaining := len(selectedRepos) - maxReposLeft
			leftLines = append(leftLines, fmt.Sprintf("    ... and %d more", remaining))
			break
		}
		leftLines = append(leftLines, fmt.Sprintf("  %s", name))
	}
	leftLines = append(leftLines, "")

	// Confirm section
	leftLines = append(leftLines, ui.SectionHeader("CONFIRM", ui.ColorGreen))
	leftLines = append(leftLines, "")

	// Calculate repos to skip (no commits). A failed fetch also has none, but
	// it isn't up to date: creating retries it.
	failed := 0
	for i, sel := range m.batch.selected {
		if sel && m.batchRepoErr(i) != "" {
			failed++
		}
	}
	reposToSkip := selectedCount - m.batch.reposWithCommits - failed

	if failed > 0 {
		msg := fmt.Sprintf("  ✗ %d repo(s) failed to fetch", failed)
		if m.batch.reposWithCommits > 0 {
			msg += " - will retry"
		}
		leftLines = append(leftLines, ui.RedBold.Render(msg), "")
	}

	// Show warning if ALL repos will be skipped
	if m.batch.reposWithCommits == 0 {
		if failed == 0 {
			warningStyle := ui.YellowBold
			leftLines = append(leftLines, warningStyle.Render("  ⊘ All repos already up to date"))
			leftLines = append(leftLines, "")
		}
		dimStyle := ui.Dim
		leftLines = append(leftLines, dimStyle.Render("  Nothing to merge"))
	} else {
		// Show warning if some repos will be skipped
		if reposToSkip > 0 {
			warningStyle := ui.YellowBold
			leftLines = append(leftLines, warningStyle.Render(fmt.Sprintf("  ⊘ %d repo(s) will be skipped - already up to date", reposToSkip)))
			leftLines = append(leftLines, "")
		}

		// Show warning if some PRs already exist
		if m.batch.existingPRs > 0 {
			warningStyle := ui.YellowBold
			leftLines = append(leftLines, warningStyle.Render(fmt.Sprintf("  ⚠ %d PR(s) already exist - will update", m.batch.existingPRs)))
			leftLines = append(leftLines, "")
		}

		newPRs := m.batch.reposWithCommits - m.batch.existingPRs
		if newPRs > 0 && m.batch.existingPRs > 0 {
			leftLines = append(leftLines, fmt.Sprintf("  Create %d, update %d PRs?", newPRs, m.batch.existingPRs))
		} else if m.batch.existingPRs > 0 {
			leftLines = append(leftLines, fmt.Sprintf("  Update %d PRs?", m.batch.existingPRs))
		} else {
			leftLines = append(leftLines, fmt.Sprintf("  Create %d PRs?", m.batch.reposWithCommits))
		}
		leftLines = append(leftLines, "")
		leftLines = append(leftLines, ui.YesNoButtons(m.confirmSelection))
	}

	leftTitleStyle := ui.CyanBold
	panelTitle := " Batch PRs "
	if m.batch.existingPRs == selectedCount {
		panelTitle = " Update PRs "
	}

	leftContent := leftTitleStyle.Render(panelTitle) + "\n" + strings.Join(leftLines, "\n")

	return leftContent, len(leftLines) + 1 // +1 for title
}

// batchConfirmRightLines builds every line of the Changes column; the
// renderer shows a window of them.
func (m Model) batchConfirmRightLines() []string {
	// Build right column (commits & tickets per repo) - build ALL content first
	var rightLines []string

	repoNameStyle := ui.CyanBold
	ticketStyle := ui.Yellow
	hashStyle := ui.Magenta
	commitStyle := ui.White
	dimStyle := ui.Dim

	// Show commits and tickets per selected repo (no limit - we'll scroll)
	for i, repo := range m.batch.repos {
		if i >= len(m.batch.selected) || !m.batch.selected[i] {
			continue
		}

		// Get commits for this repo
		var commits []models.CommitInfo
		if i < len(m.batch.repoCommits) && m.batch.repoCommits[i] != nil {
			commits = *m.batch.repoCommits[i]
		}

		// Skip repos with no commits
		if len(commits) == 0 {
			continue
		}

		// Repo name header
		rightLines = append(rightLines, fmt.Sprintf("  %s", repoNameStyle.Render(repo.ShortName())))

		// Show commits with tickets (limit to 3 per repo for readability)
		maxCommits := 3
		for j, commit := range commits {
			if j >= maxCommits {
				if len(commits) > maxCommits {
					rightLines = append(rightLines, dimStyle.Render(fmt.Sprintf("      +%d more commits", len(commits)-maxCommits)))
				}
				break
			}

			// Format: hash message (with ticket highlighted if present)
			msg := commit.Message
			maxMsgLen := 55
			if len(msg) > maxMsgLen {
				msg = msg[:maxMsgLen-3] + "..."
			}

			// Highlight ticket in message if present
			if len(commit.Tickets) > 0 {
				for _, ticket := range commit.Tickets {
					msg = strings.Replace(msg, ticket, ticketStyle.Render(ticket), 1)
				}
			}

			rightLines = append(rightLines, fmt.Sprintf("    %s %s", hashStyle.Render(commit.Hash), commitStyle.Render(msg)))
		}
		rightLines = append(rightLines, "")
	}

	// Tickets summary at bottom
	if len(m.tickets) > 0 {
		rightLines = append(rightLines, ui.SectionHeader("TICKETS", ui.ColorYellow))
		// List all tickets (scrollable now)
		for _, ticket := range m.tickets {
			rightLines = append(rightLines, fmt.Sprintf("  • %s", ticketStyle.Render(ticket)))
		}
	}

	if m.dryRun {
		rightLines = append(rightLines, "")
		warningStyle := ui.YellowBold
		rightLines = append(rightLines, warningStyle.Render("  ⚠ DRY RUN MODE"))
	}

	return rightLines
}

func (m Model) renderBatchProcessing() string {
	var lines []string

	// Header with count - use selected count, not total repos
	// len(batchResults) = completed, +1 if currently processing one
	processedCount := len(m.batch.results)
	if m.batch.currentRepo != "" {
		processedCount++
	}
	countStyle := ui.White
	header := fmt.Sprintf("Processing Repositories %s", countStyle.Render(fmt.Sprintf("(%d/%d)", processedCount, m.batch.total)))
	lines = append(lines, ui.SectionHeader(header, ui.ColorMagenta))
	lines = append(lines, "")

	// Current repo being processed
	spinner := ui.Spinner(m.spinnerFrame)
	spinnerStyle := ui.Cyan
	repoStyle := ui.YellowBold
	stepStyle := ui.White

	if m.batch.currentRepo != "" {
		lines = append(lines, fmt.Sprintf("   %s Processing %s...",
			spinnerStyle.Render(spinner),
			repoStyle.Render(m.batch.currentRepo),
		))
		// Show current step if available
		if m.batch.currentStep != "" {
			lines = append(lines, fmt.Sprintf("      → %s", stepStyle.Render(m.batch.currentStep)))
		}
	}
	lines = append(lines, "")

	// Completed results log
	if len(m.batch.results) > 0 {
		lines = append(lines, ui.SectionHeader("Completed", ui.ColorWhite))
		lines = append(lines, "")

		for _, result := range m.batch.results {
			var icon string
			var statusText string
			var color lipgloss.TerminalColor

			if models.IsStatusCreated(result.Status) {
				icon = "✓"
				statusText = "PR created"
				color = ui.ColorGreen
			} else if models.IsStatusUpdated(result.Status) {
				icon = "✓"
				statusText = "PR updated"
				color = ui.ColorGreen
			} else if models.IsStatusSkipped(result.Status) {
				icon = "⊘"
				statusText = models.GetStatusReason(result.Status)
				color = ui.ColorYellow
			} else if models.IsStatusFailed(result.Status) {
				icon = "✗"
				statusText = models.GetStatusReason(result.Status)
				color = ui.ColorRed
			}

			iconStyle := lipgloss.NewStyle().Foreground(color)
			repoNameStyle := ui.Cyan
			statusStyle := lipgloss.NewStyle().Foreground(color)

			lines = append(lines, fmt.Sprintf("   %s %s: %s",
				iconStyle.Render(icon),
				repoNameStyle.Render(result.Repo.DisplayName),
				statusStyle.Render(statusText),
			))
		}
		lines = append(lines, "")
	}

	return strings.Join(lines, "\n")
}

func (m Model) renderBatchSummaryWithHeight(availableHeight int) string {
	var lines []string

	// Count results by status
	successCount := 0
	skipCount := 0
	failCount := 0
	for _, result := range m.batch.results {
		if models.IsStatusSuccess(result.Status) {
			successCount++
		} else if models.IsStatusSkipped(result.Status) {
			skipCount++
		} else if models.IsStatusFailed(result.Status) {
			failCount++
		}
	}

	// Determine header message and colors based on results
	var headerMsg string
	var headerColor lipgloss.TerminalColor
	var icon string

	if successCount > 0 {
		headerMsg = fmt.Sprintf("%d PRs processed successfully!", successCount)
		headerColor = ui.ColorGreen
		icon = "✓"
	} else if skipCount > 0 && failCount == 0 {
		headerMsg = fmt.Sprintf("All %d repos skipped - branches already up to date", skipCount)
		headerColor = ui.ColorYellow
		icon = "⊘"
	} else if failCount > 0 {
		headerMsg = fmt.Sprintf("%d repos failed to process", failCount)
		headerColor = ui.ColorRed
		icon = "✗"
	} else {
		headerMsg = "No repositories processed"
		headerColor = ui.ColorYellow
		icon = "⊘"
	}

	// Typewriter effect for header message
	revealedText := revealRunes(headerMsg, m.typewriterPos)

	// Pulsing icon (only pulse green for success)
	iconColor := headerColor
	if successCount > 0 {
		pulseIntensity := (math.Sin(m.pulsePhase) + 1.0) / 2.0
		if pulseIntensity > 0.5 {
			iconColor = ui.ColorGreen
		} else {
			iconColor = ui.ColorLightGreen
		}
	}

	iconStyle := lipgloss.NewStyle().Foreground(iconColor).Bold(true)
	headerStyle := lipgloss.NewStyle().Foreground(headerColor).Bold(true)

	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("   %s %s", iconStyle.Render(icon), headerStyle.Render(revealedText)))
	lines = append(lines, "")

	// Results list
	for i, result := range m.batch.results {
		var statusStr string
		var statusColor lipgloss.TerminalColor

		if models.IsStatusCreated(result.Status) {
			statusStr = "✓ Created"
			statusColor = ui.ColorGreen
		} else if models.IsStatusUpdated(result.Status) {
			statusStr = "↻ Updated"
			statusColor = ui.ColorCyan
		} else if models.IsStatusSkipped(result.Status) {
			statusStr = "⊘ Skipped"
			statusColor = ui.ColorYellow
		} else if models.IsStatusFailed(result.Status) {
			statusStr = "✗ Failed"
			statusColor = ui.ColorRed
		}

		statusStyle := lipgloss.NewStyle().Foreground(statusColor)
		repoStyle := ui.White

		// Highlight selected row
		prefix := "  "
		if i == m.menuIndex {
			prefix = "▶ "
			repoStyle = repoStyle.Bold(true)
		}

		lines = append(lines, fmt.Sprintf("   %s%s %s",
			prefix,
			statusStyle.Render(fmt.Sprintf("%-12s", statusStr)),
			repoStyle.Render(result.Repo.DisplayName),
		))

		// Show URL if available
		if result.PrURL != nil {
			urlStyle := ui.Cyan
			lines = append(lines, fmt.Sprintf("              %s", urlStyle.Render(*result.PrURL)))
		}

		// Show skip/fail reason
		reason := models.GetStatusReason(result.Status)
		if reason != "" {
			reasonStyle := lipgloss.NewStyle().Foreground(statusColor)
			lines = append(lines, fmt.Sprintf("              %s", reasonStyle.Render(reason)))
		}

		// Show tickets if any
		if len(result.Tickets) > 0 {
			ticketStyle := ui.Yellow
			lines = append(lines, fmt.Sprintf("              %s", ticketStyle.Render(strings.Join(result.Tickets, ", "))))
		}
	}

	lines = append(lines, "")

	// Summary footer
	dimStyle := ui.Dim
	lines = append(lines, dimStyle.Render(fmt.Sprintf("   Total: %d success, %d skipped, %d failed",
		successCount, skipCount, failCount)))

	// Render confetti if there were successes
	if successCount > 0 {
		lines = append(lines, "")
		lines = append(lines, m.renderConfetti())
	}

	content := strings.Join(lines, "\n")

	// Fixed box width for stable layout
	boxWidth := m.contentWidth() - 10

	return ui.ColumnBox(content, " Batch Summary ", ui.ColorGreen, true, boxWidth, availableHeight)
}

// sendProgress sends a progress update without blocking. A send on a closed
// channel still panics; the channel is only closed once the run's last
// command has returned, and the tab keys are off while it runs.
func sendProgress(ch chan string, step string) {
	if ch != nil {
		select {
		case ch <- step:
		default:
			// Channel full, skip
		}
	}
}
