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

// The GitHub Actions overview: every recent run across the repos in one list,
// newest first, with the highlighted run's jobs beside it and the runs being
// watched below those. It refreshes itself every few seconds.

// actionsState is everything this screen owns. Keeping it in one struct means
// reset() clears the screen by assigning a zero value.
type actionsState struct {
	entries     []actionsEntry // newest first
	repoErrors  []string       // per-repo fetch errors
	index       int            // cursor into the filtered entries
	scroll      int            // first filtered entry on screen
	loading     bool
	autoRefresh bool      // auto-refresh every 5s (default on)
	refreshGen  int       // the live refresh chain; older ticks are ignored
	nextRefresh time.Time // when that chain's next tick fires

	filter       string
	filterTyping bool // the keyboard goes to the filter; Enter keeps it, Esc clears it

	jobs    map[uint64]runJobs // by run ID
	watched []actionsEntry     // in the order they were watched
}

// actionsEntry holds a single workflow run with its repo
type actionsEntry struct {
	Repo models.RepoInfo
	Run  models.WorkflowRun
}

// runJobs is one run's jobs as last fetched. A run is identified by its
// updated_at as well as its ID, since a rerun keeps the ID.
type runJobs struct {
	jobs    []models.WorkflowJob // nil until a fetch has succeeded
	of      time.Time            // the run's UpdatedAt the jobs were fetched for
	done    bool                 // the run had completed then, so they are final
	asked   time.Time            // the newest request's UpdatedAt; older answers are dropped
	loading bool
	err     error
}

// current reports jobs fetched for the run as it is now.
func (c runJobs) current(run models.WorkflowRun) bool {
	return c.jobs != nil && c.err == nil && c.of.Equal(run.UpdatedAt)
}

func (m Model) isWatched(runID uint64) bool {
	for _, w := range m.actions.watched {
		if w.Run.DatabaseID == runID {
			return true
		}
	}
	return false
}

func (m *Model) unwatch(runID uint64) bool {
	for i, w := range m.actions.watched {
		if w.Run.DatabaseID == runID {
			m.actions.watched = append(m.actions.watched[:i:i], m.actions.watched[i+1:]...)
			return true
		}
	}
	return false
}

// highlightedRun is the run under the cursor, if the list has one.
func (m Model) highlightedRun() (actionsEntry, bool) {
	filtered := m.getFilteredActions()
	if m.actions.index < 0 || m.actions.index >= len(filtered) {
		return actionsEntry{}, false
	}
	return m.actions.entries[filtered[m.actions.index]], true
}

// actionsPanelHeight is the list and detail boxes' height inside their borders,
// title row included: the title bar above takes three rows and the borders two.
func actionsPanelHeight(height int) int {
	return max(height-5, 4)
}

// actionsListRows is how many runs the list shows, from the same height the
// renderer is given, so the scroll and the drawing cannot disagree.
func (m Model) actionsListRows() int {
	return actionsPanelHeight(m.unboxedHeight()) - 1
}

// actionsScroll is the first row to draw so that the cursor is on screen,
// moving no further than it has to from where the list was.
func actionsScroll(index, scroll, rows, total int) int {
	if index < scroll {
		scroll = index
	}
	if index >= scroll+rows {
		scroll = index - rows + 1
	}
	return max(min(scroll, total-rows), 0)
}

func (m *Model) keepActionsCursorVisible() {
	m.actions.scroll = actionsScroll(m.actions.index, m.actions.scroll, m.actionsListRows(), len(m.getFilteredActions()))
}

type actionsRunsFetchedResult struct {
	entries    []actionsEntry
	repoErrors []string // per-repo errors (non-fatal)
	err        error
}

func (actionsRunsFetchedResult) flowResult() {}

type actionsRefreshTickMsg struct{ gen int }

func (actionsRefreshTickMsg) flowResult() {}

// actionsPreviewMsg fires once the cursor has rested on a run for
// actionsPreviewDelay, so holding ↓ through the list starts no fetch per run.
type actionsPreviewMsg struct{ runID uint64 }

func (actionsPreviewMsg) flowResult() {}

const actionsPreviewDelay = 150 * time.Millisecond

type actionsJobsFetchedResult struct {
	runID uint64
	of    time.Time // the run's UpdatedAt when they were asked for
	done  bool      // the run had completed then
	jobs  []models.WorkflowJob
	err   error
}

// actionsJobsFetchedResult is deliberately not a flowResult. It only fills the
// cache under its run ID and never moves the screen, so a late one is
// harmless, and a finished run's jobs are not asked for twice. One older than
// the newest request for its run is dropped instead.

// selectActionsRuns picks the runs the list shows from one repo's recent runs:
// every queued or running one, the newest finished one per workflow, and any
// run that is watched. Watched runs used to fall to the per-workflow rule once
// a newer run of the same workflow finished, and stopped updating.
func selectActionsRuns(runs []models.WorkflowRun, cutoff time.Time, watched map[uint64]bool) []models.WorkflowRun {
	var kept []models.WorkflowRun
	latestCompleted := map[string]bool{} // workflowName -> already added
	for _, run := range runs {
		switch {
		case watched[run.DatabaseID]:
			kept = append(kept, run)
			if run.Status == "completed" {
				latestCompleted[run.WorkflowName] = true
			}
		case run.UpdatedAt.Before(cutoff):
		case run.Status == "in_progress" || run.Status == "queued":
			kept = append(kept, run)
		case run.Status == "completed" && !latestCompleted[run.WorkflowName]:
			kept = append(kept, run)
			latestCompleted[run.WorkflowName] = true
		}
	}
	return kept
}

func fetchActionsRunsCmd(cfg *config.Config, dryRun bool, watched []actionsEntry) tea.Cmd {
	watched = append([]actionsEntry(nil), watched...) // the model updates its own in place
	return func() tea.Msg {
		if dryRun {
			res := dryRunActionsRuns()
			newestFirst(res.entries)
			return res
		}

		repos, err := discoverRepos(cfg)
		if err != nil {
			return actionsRunsFetchedResult{err: err}
		}

		type repoResult struct {
			repo models.RepoInfo
			runs []models.WorkflowRun
			err  error
		}
		withNWO := githubRepos(repos)
		results := parallelMap(withNWO, func(r repoNWO) repoResult {
			runs, err := github.ListWorkflowRunsByNWO(r.NWO, 10)
			return repoResult{repo: r.Repo, runs: runs, err: err}
		})

		ids := map[uint64]bool{}
		for _, w := range watched {
			ids[w.Run.DatabaseID] = true
		}
		cutoff := timeNow().Add(-48 * time.Hour)
		var entries []actionsEntry
		var repoErrors []string
		fetched := map[uint64]bool{}
		for _, res := range results {
			if res.err != nil {
				repoErrors = append(repoErrors, fmt.Sprintf("%s: %v", res.repo.DisplayName, res.err))
				continue
			}
			for _, run := range res.runs {
				fetched[run.DatabaseID] = true
			}
			for _, run := range selectActionsRuns(res.runs, cutoff, ids) {
				entries = append(entries, actionsEntry{Repo: res.repo, Run: run})
			}
		}
		// Ten newer runs push a watched one out of its repo's page, and it
		// would stay as last seen, running for good. Those are asked for by ID.
		nwoOf := map[string]string{}
		for _, r := range withNWO {
			nwoOf[r.Repo.Path] = r.NWO
		}
		for _, e := range parallelMap(unfetchedWatched(watched, fetched), func(w actionsEntry) actionsEntry {
			run, err := github.GetWorkflowRunByNWO(nwoOf[w.Repo.Path], w.Run.DatabaseID)
			if err != nil {
				return actionsEntry{}
			}
			return actionsEntry{Repo: w.Repo, Run: run}
		}) {
			if e.Run.DatabaseID != 0 {
				entries = append(entries, e)
			}
		}
		newestFirst(entries)
		return actionsRunsFetchedResult{entries: entries, repoErrors: repoErrors}
	}
}

// unfetchedWatched are the watched runs missing from what was fetched.
func unfetchedWatched(watched []actionsEntry, fetched map[uint64]bool) []actionsEntry {
	var out []actionsEntry
	for _, w := range watched {
		if !fetched[w.Run.DatabaseID] {
			out = append(out, w)
		}
	}
	return out
}

func newestFirst(entries []actionsEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Run.UpdatedAt.After(entries[j].Run.UpdatedAt)
	})
}

// A var so tests can run a tick without waiting for it.
var actionsRefreshEvery = 5 * time.Second

// startActionsRefresh starts a refresh chain and ends any other: ticks carry
// the generation they were started with, and only the newest one runs. Every
// fetch result used to schedule a tick, as did the toggle and re-entering the
// tab, so each of those added a chain and the gh calls multiplied.
//
// Call it as its own statement before returning m: it changes m.
func (m *Model) startActionsRefresh() tea.Cmd {
	m.actions.refreshGen++
	return m.nextActionsTick()
}

// nextActionsTick schedules the live chain's next tick. Only a tick does this.
func (m *Model) nextActionsTick() tea.Cmd {
	gen := m.actions.refreshGen
	m.actions.nextRefresh = timeNow().Add(actionsRefreshEvery)
	return tea.Tick(actionsRefreshEvery, func(_ time.Time) tea.Msg {
		return actionsRefreshTickMsg{gen: gen}
	})
}

func fetchActionsJobsCmd(e actionsEntry, dryRun bool) tea.Cmd {
	id, of, done := e.Run.DatabaseID, e.Run.UpdatedAt, e.Run.Status == "completed"
	return func() tea.Msg {
		res := actionsJobsFetchedResult{runID: id}
		if dryRun {
			res = dryRunActionsJobs(id)
		} else {
			res.jobs, res.err = github.GetWorkflowRunJobs(e.Repo.Path, id)
		}
		res.of, res.done = of, done
		return res
	}
}

// fetchRunJobs asks for a run's jobs unless a fetch is out or they are final:
// jobs fetched once the run had completed cannot change until it is rerun. It
// changes m, so call it as its own statement.
func (m *Model) fetchRunJobs(e actionsEntry) tea.Cmd {
	c := m.actions.jobs[e.Run.DatabaseID]
	if c.loading || c.done && c.current(e.Run) {
		return nil
	}
	if m.actions.jobs == nil {
		m.actions.jobs = map[uint64]runJobs{}
	}
	c.loading, c.asked = true, e.Run.UpdatedAt
	m.actions.jobs[e.Run.DatabaseID] = c
	return fetchActionsJobsCmd(e, m.dryRun)
}

// previewActionsRun schedules the highlighted run's jobs, unless they are
// already there; a running run's are kept current by the refresh.
func (m Model) previewActionsRun() tea.Cmd {
	e, ok := m.highlightedRun()
	if !ok {
		return nil
	}
	if c := m.actions.jobs[e.Run.DatabaseID]; c.loading || c.current(e.Run) {
		return nil
	}
	id := e.Run.DatabaseID
	return tea.Tick(actionsPreviewDelay, func(time.Time) tea.Msg { return actionsPreviewMsg{runID: id} })
}

func (m Model) handleActionsPreview(msg actionsPreviewMsg) (tea.Model, tea.Cmd) {
	e, ok := m.highlightedRun()
	if !ok || e.Run.DatabaseID != msg.runID {
		return m, nil // the cursor has moved on
	}
	cmd := m.fetchRunJobs(e)
	return m, cmd
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
	prev, hadPrev := m.highlightedRun()
	m.actions.entries = msg.entries
	m.actions.repoErrors = msg.repoErrors
	var cmds []tea.Cmd
	if m.screen == ScreenLoading {
		m.screen = ScreenActionsOverview
		m.actions.autoRefresh = true // default on for actions
		cmds = append(cmds, m.startActionsRefresh())
	}

	// New runs arrive at the top, so the cursor follows its run rather than
	// its row, or the highlight slides to another run on every refresh.
	filtered := m.getFilteredActions()
	m.actions.index = min(m.actions.index, max(len(filtered)-1, 0))
	for i, idx := range filtered {
		if hadPrev && m.actions.entries[idx].Run.DatabaseID == prev.Run.DatabaseID {
			m.actions.index = i
			break
		}
	}
	m.keepActionsCursorVisible()

	for i, w := range m.actions.watched {
		for _, e := range msg.entries {
			if e.Run.DatabaseID == w.Run.DatabaseID {
				m.actions.watched[i] = e
				break
			}
		}
	}
	if e, ok := m.highlightedRun(); ok {
		cmds = append(cmds, m.fetchRunJobs(e))
	}
	for _, w := range m.actions.watched {
		cmds = append(cmds, m.fetchRunJobs(w))
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handleActionsRefreshTick(msg actionsRefreshTickMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.actions.refreshGen || m.screen != ScreenActionsOverview || !m.actions.autoRefresh {
		return m, nil // Stop tick chain
	}
	next := m.nextActionsTick()
	if m.actions.loading {
		return m, next // the last fetch is still out; skip this round
	}
	m.actions.loading = true
	return m, tea.Batch(next, fetchActionsRunsCmd(m.config, m.dryRun, m.actions.watched))
}

func (m Model) handleActionsJobsFetched(msg actionsJobsFetchedResult) (tea.Model, tea.Cmd) {
	if m.actions.jobs == nil {
		m.actions.jobs = map[uint64]runJobs{}
	}
	c := m.actions.jobs[msg.runID]
	if msg.of.Before(c.asked) {
		return m, nil // a newer request is out, or has answered
	}
	c.loading = false
	if msg.err != nil {
		c.err = msg.err // shown, and asked again on the next refresh
	} else {
		c.jobs, c.err, c.of, c.done = msg.jobs, nil, msg.of, msg.done
		if c.jobs == nil {
			c.jobs = []models.WorkflowJob{}
		}
	}
	m.actions.jobs[msg.runID] = c
	return m, nil
}

func (m Model) handleActionsOverviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.actions.filterTyping {
		return m.handleActionsFilterKey(msg)
	}
	filtered := m.getFilteredActions()
	page := max(m.actionsListRows()-1, 1)
	switch msg.String() {
	case "up", "k":
		navigateColumnIndex(&m.actions.index, len(filtered), true)
	case "down", "j":
		navigateColumnIndex(&m.actions.index, len(filtered), false)
	case "pgup":
		m.actions.index = max(m.actions.index-page, 0)
	case "pgdown":
		m.actions.index = max(min(m.actions.index+page, len(filtered)-1), 0)
	case "home", "g":
		m.actions.index = 0
	case "end", "G":
		m.actions.index = max(len(filtered)-1, 0)
	case " ":
		e, ok := m.highlightedRun()
		if !ok || m.unwatch(e.Run.DatabaseID) {
			return m, nil
		}
		m.actions.watched = append(m.actions.watched, e)
		cmd := m.fetchRunJobs(e)
		return m, cmd
	case "n":
		m.actions.watched = nil
	case "o":
		if e, ok := m.highlightedRun(); ok && e.Run.URL != "" {
			m.openInBrowser(e.Run.URL)
		}
	case "r":
		if m.actions.loading {
			return m, nil
		}
		// An explicit refresh should also pick up repos added on disk.
		invalidateRepoCache()
		m.actions.loading = true
		return m, fetchActionsRunsCmd(m.config, m.dryRun, m.actions.watched)
	case "a":
		m.actions.autoRefresh = !m.actions.autoRefresh
		if m.actions.autoRefresh {
			cmd := m.startActionsRefresh()
			return m, cmd
		}
		return m, nil
	case "/":
		m.actions.filterTyping = true
		return m, nil
	case "esc":
		if m.actions.filter != "" {
			m.clearActionsFilter()
			break
		}
		// Home as the tab keys go there, keeping the list and the watched runs.
		return m.navigateToTab(ui.HomeTab)
	case "q", "ctrl+c":
		m.shouldQuit = true
		return m, tea.Quit
	default:
		return m, nil
	}
	m.keepActionsCursorVisible()
	return m, m.previewActionsRun()
}

// handleActionsFilterKey takes every key while the filter is being typed.
// Space and the letters are text here, not the watch, open and refresh keys.
func (m Model) handleActionsFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	filtered := m.getFilteredActions()
	switch msg.Type {
	case tea.KeyEnter:
		m.actions.filterTyping = false
		return m, nil
	case tea.KeyEsc:
		m.actions.filterTyping = false
		m.clearActionsFilter()
	case tea.KeyUp:
		navigateColumnIndex(&m.actions.index, len(filtered), true)
	case tea.KeyDown:
		navigateColumnIndex(&m.actions.index, len(filtered), false)
	case tea.KeyBackspace:
		if m.actions.filter == "" {
			return m, nil
		}
		m.setActionsFilter(trimLastRune(m.actions.filter))
	case tea.KeySpace:
		m.setActionsFilter(m.actions.filter + " ")
	case tea.KeyRunes:
		m.setActionsFilter(m.actions.filter + typedText(msg.Runes))
	case tea.KeyCtrlC:
		m.shouldQuit = true
		return m, tea.Quit
	default:
		return m, nil
	}
	m.keepActionsCursorVisible()
	return m, m.previewActionsRun()
}

func (m *Model) setActionsFilter(filter string) {
	m.actions.filter = filter
	m.actions.index, m.actions.scroll = 0, 0
}

// clearActionsFilter drops the filter, leaving the cursor on the run it was on.
func (m *Model) clearActionsFilter() {
	e, ok := m.highlightedRun()
	m.setActionsFilter("")
	for i, entry := range m.actions.entries {
		if ok && entry.Run.DatabaseID == e.Run.DatabaseID {
			m.actions.index = i
		}
	}
}

// getFilteredActions returns indices of entries matching the text filter
func (m Model) getFilteredActions() []int {
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

// --- Rendering ---------------------------------------------------------------

func (m Model) renderActionsOverviewWithHeight(height int) string {
	if len(m.actions.entries) == 0 && !m.actions.loading {
		return m.renderActionsEmpty()
	}
	width := m.frameWidth()
	panel := actionsPanelHeight(height)
	listW := (width - 5) * 58 / 100 // two borders each and the gap
	detailW := width - 5 - listW

	filtered := m.getFilteredActions()
	title := fmt.Sprintf("RUNS (%d)", len(filtered))
	if len(filtered) != len(m.actions.entries) {
		title = fmt.Sprintf("RUNS (%d of %d)", len(filtered), len(m.actions.entries))
	}
	list := ui.ColumnBox(strings.Join(m.renderActionsRunList(filtered, listW, panel-1), "\n"),
		title, ui.ColorOrange, true, listW, panel)
	return m.renderActionsTitleBar(width) + "\n" + ui.TwoColumns(list, m.renderActionsDetailColumn(detailW, panel), 1)
}

func (m Model) renderActionsEmpty() string {
	centered := lipgloss.NewStyle().Width(m.contentWidth()).Align(lipgloss.Center)
	lines := []string{
		"",
		"",
		centered.Render(ui.Green.Render("✓") + " No active workflow runs"),
		centered.Render(ui.Dim.Render("(showing last 48 hours)")),
	}
	if len(m.actions.repoErrors) > 0 {
		lines = append(lines, "", centered.Render(ui.Red.Render(fmt.Sprintf("Failed to fetch %d repo(s):", len(m.actions.repoErrors)))))
		for _, e := range m.actions.repoErrors {
			lines = append(lines, centered.Render(ui.Dim.Render(e)))
		}
	}
	return strings.Join(lines, "\n")
}

// renderActionsTitleBar is the screen's title, the run counts, the filter and
// the refresh state on one bordered line.
func (m Model) renderActionsTitleBar(width int) string {
	inner := width - 4 // the border and a space of padding either side
	passed, failed, running, queued := 0, 0, 0, 0
	for _, e := range m.actions.entries {
		switch {
		case e.Run.Status == "in_progress":
			running++
		case e.Run.Status == "queued":
			queued++
		case e.Run.Conclusion == "success":
			passed++
		case runFailed(e.Run.Conclusion):
			failed++
		}
	}
	left := lipgloss.NewStyle().Foreground(ui.ColorOrange).Bold(true).Render("GitHub Actions") +
		ui.Dim.Render(" · last 48h   ") + ui.Green.Render(fmt.Sprintf("✓%d", passed))
	if failed > 0 {
		left += " " + ui.Red.Render(fmt.Sprintf("✗%d", failed))
	}
	if running > 0 {
		left += " " + ui.Yellow.Render(fmt.Sprintf("%s%d", ui.Spinner(m.spinnerFrame), running))
	}
	if queued > 0 {
		left += " " + ui.Dim.Render(fmt.Sprintf("◌%d", queued))
	}
	if m.actions.filterTyping || m.actions.filter != "" {
		left += "   " + ui.Cyan.Render("/ ") + ui.Yellow.Render(m.actions.filter)
		if m.actions.filterTyping {
			left += ui.Yellow.Render("█")
		}
	}

	var right string
	switch {
	case m.actions.loading:
		right = ui.Yellow.Render(ui.Spinner(m.spinnerFrame)) + ui.Dim.Render(" refreshing")
	case m.actions.autoRefresh:
		remaining := max(int(m.actions.nextRefresh.Sub(timeNow()).Round(time.Second).Seconds()), 0)
		right = ui.Dim.Render(fmt.Sprintf("refresh in %ds", remaining))
	default:
		right = ui.Dim.Render("auto-refresh off")
	}
	if n := len(m.actions.repoErrors); n > 0 {
		right = ui.Red.Render("✗ ") + ui.Dim.Render(fmt.Sprintf("%d repo%s unread · ", n, pluralS(n))) + right
	}
	line := left
	if gap := inner - lipgloss.Width(left) - lipgloss.Width(right); gap >= 2 {
		line += strings.Repeat(" ", gap) + right
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ui.ColorOrange).
		Padding(0, 1).Width(width - 2).Render(lipgloss.NewStyle().MaxWidth(inner).Render(line))
}

// actionsColumns are the run list's column widths for a list this wide. The
// branch goes first when it is narrow: the detail pane shows it.
type actionsColumns struct{ workflow, repo, branch int }

func actionsColumnsFor(width int) actionsColumns {
	// Space, icon, space, watch mark, space, then a space after each column
	// and four for the age.
	if room := width - 13; room >= 34 {
		return actionsColumns{workflow: room * 40 / 100, repo: room * 33 / 100, branch: room - room*40/100 - room*33/100}
	}
	room := max(width-12, 10)
	return actionsColumns{workflow: room * 55 / 100, repo: room - room*55/100}
}

func (m Model) renderActionsRunList(filtered []int, width, rows int) []string {
	if len(filtered) == 0 {
		return []string{"", ui.Dim.Render(fmt.Sprintf(" No runs match %q", m.actions.filter))}
	}
	cols := actionsColumnsFor(width)
	start := actionsScroll(m.actions.index, m.actions.scroll, rows, len(filtered))
	var lines []string
	for fi := start; fi < min(start+rows, len(filtered)); fi++ {
		lines = append(lines, m.renderActionsRow(m.actions.entries[filtered[fi]], cols, width, fi == m.actions.index))
	}
	return lines
}

func (m Model) renderActionsRow(e actionsEntry, cols actionsColumns, width int, highlighted bool) string {
	icon, iconColor := ui.WorkflowStatusIcon(e.Run.Status, e.Run.Conclusion, m.spinnerFrame)
	mark := " "
	if m.isWatched(e.Run.DatabaseID) {
		mark = ui.Cyan.Render("●")
	}
	name := ui.White
	if highlighted {
		name = ui.WhiteBold
	}
	row := " " + lipgloss.NewStyle().Foreground(iconColor).Render(icon) + " " + mark + " " +
		visPad(name.Render(truncateString(e.Run.WorkflowName, cols.workflow)), cols.workflow) + " " +
		visPad(lipgloss.NewStyle().Foreground(m.repoColor(e.Repo)).Render(truncateStart(e.Repo.ShortName(), cols.repo)), cols.repo) + " "
	if cols.branch > 0 {
		row += visPad(lipgloss.NewStyle().Foreground(m.branchColor(e.Run.HeadBranch)).
			Render(truncateString(e.Run.HeadBranch, cols.branch)), cols.branch) + " "
	}
	row += visRightAlign(ui.Dim.Render(shortAge(e.Run.UpdatedAt)), 4)
	if highlighted {
		row = ui.OnBackground(visPad(row, width), ui.ColorSelection)
	}
	return row
}

// truncateStart cuts s from the front: repos that share a prefix, like
// acme.charts and acme.core, differ at the end.
func truncateStart(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > width {
		r = r[1:]
	}
	return "…" + string(r)
}

// repoColor is the colour of the column a repo is shown in elsewhere.
func (m Model) repoColor(r models.RepoInfo) lipgloss.TerminalColor {
	if r.InColumn(0, m.leftGroups()) {
		return ui.ColorCyan
	}
	return ui.ColorMagenta
}

// renderActionsDetailColumn is the highlighted run's detail, with the watched
// runs in a box below it when there are any.
func (m Model) renderActionsDetailColumn(width, height int) string {
	watchRows := min(len(m.actions.watched), max(height/3, 1), max(height-7, 0))
	detailH := height
	if watchRows > 0 {
		detailH = height - (watchRows + 1) - 2 // its title row and borders
	}

	title := "RUN"
	lines := []string{"", ui.Dim.Render(" No run selected")}
	if e, ok := m.highlightedRun(); ok {
		title = e.Run.WorkflowName
		lines = m.renderRunDetail(e, detailH-1)
	}
	detail := ui.ColumnBox(strings.Join(lines, "\n"), title, ui.ColorOrange, false, width, detailH)
	if watchRows == 0 {
		return detail
	}
	watch := ui.ColumnBox(strings.Join(m.renderWatchList(width, watchRows), "\n"),
		fmt.Sprintf("WATCHING (%d)", len(m.actions.watched)), ui.ColorCyan, false, width, watchRows+1)
	return detail + "\n" + watch
}

// renderRunDetail describes a run and lists its jobs, with the steps that
// failed or are running under their job, in no more than rows lines.
func (m Model) renderRunDetail(e actionsEntry, rows int) []string {
	run := e.Run
	icon, iconColor := ui.WorkflowStatusIcon(run.Status, run.Conclusion, m.spinnerFrame)
	lines := []string{
		" " + lipgloss.NewStyle().Foreground(m.repoColor(e.Repo)).Render(e.Repo.ShortName()) +
			ui.Dim.Render(" · ") + lipgloss.NewStyle().Foreground(m.branchColor(run.HeadBranch)).Render(run.HeadBranch) +
			ui.Dim.Render(" · "+run.Event),
		" " + ui.White.Render(run.DisplayTitle),
		" " + lipgloss.NewStyle().Foreground(iconColor).Render(icon+" "+runStateText(run)) +
			ui.Dim.Render(fmt.Sprintf(" · %s · #%d", relativeTime(run.UpdatedAt), run.DatabaseID)),
		"",
	}
	c := m.actions.jobs[run.DatabaseID]
	switch {
	case c.jobs == nil && c.err != nil:
		msg, _, _ := strings.Cut(c.err.Error(), "\n")
		lines = append(lines, " "+ui.Red.Render("✗ ")+ui.Dim.Render("Couldn't load jobs: "+msg))
	case c.jobs == nil:
		lines = append(lines, " "+ui.Yellow.Render(ui.Spinner(m.spinnerFrame))+ui.Dim.Render(" Loading jobs..."))
	case len(c.jobs) == 0:
		lines = append(lines, ui.Dim.Render(" No jobs"))
	}
	for _, job := range c.jobs {
		jobIcon, jobColor := ui.WorkflowStatusIcon(job.Status, job.Conclusion, m.spinnerFrame)
		lines = append(lines, " "+lipgloss.NewStyle().Foreground(jobColor).Render(jobIcon)+" "+ui.White.Render(job.Name))
		if job.Conclusion != "failure" && job.Status != "in_progress" {
			continue
		}
		for _, step := range job.Steps {
			if step.Conclusion == "failure" || step.Status == "in_progress" {
				stepIcon, stepColor := ui.WorkflowStatusIcon(step.Status, step.Conclusion, m.spinnerFrame)
				lines = append(lines, "     "+lipgloss.NewStyle().Foreground(stepColor).Render(stepIcon)+" "+
					ui.Dim.Render(fmt.Sprintf("%d. %s", step.Number, step.Name)))
			}
		}
	}
	if len(lines) > rows && rows > 0 {
		lines = append(lines[:rows-1], ui.Dim.Render(fmt.Sprintf(" +%d more · o opens the run", len(lines)-rows+1)))
	}
	return lines
}

// runStateText is a run's state in a word: running, queued, or how it ended.
func runStateText(r models.WorkflowRun) string {
	switch {
	case r.Status == "in_progress":
		return "running"
	case r.Status == "completed" && r.Conclusion != "":
		return r.Conclusion
	}
	return r.Status
}

// renderWatchList is one line per watched run, with how far a running one has
// got or which job failed.
func (m Model) renderWatchList(width, rows int) []string {
	shown := m.actions.watched
	if len(shown) > rows {
		shown = shown[:rows-1]
	}
	room := max(width-9, 12) // space, icon, space, the gaps and the age
	wfW, repoW := room*38/100, room*30/100
	noteW := room - wfW - repoW - 2
	var lines []string
	for _, e := range shown {
		icon, iconColor := ui.WorkflowStatusIcon(e.Run.Status, e.Run.Conclusion, m.spinnerFrame)
		lines = append(lines, " "+lipgloss.NewStyle().Foreground(iconColor).Render(icon)+" "+
			visPad(ui.White.Render(truncateString(e.Run.WorkflowName, wfW)), wfW)+" "+
			visPad(lipgloss.NewStyle().Foreground(m.repoColor(e.Repo)).Render(truncateStart(e.Repo.ShortName(), repoW)), repoW)+" "+
			visPad(m.watchNote(e, noteW), noteW)+" "+
			visRightAlign(ui.Dim.Render(shortAge(e.Run.UpdatedAt)), 4))
	}
	if more := len(m.actions.watched) - len(shown); more > 0 {
		lines = append(lines, ui.Dim.Render(fmt.Sprintf(" +%d more", more)))
	}
	return lines
}

// watchNote is a watched run's progress: jobs finished while it runs, the
// first failed job once it has failed.
func (m Model) watchNote(e actionsEntry, width int) string {
	jobs := m.actions.jobs[e.Run.DatabaseID].jobs
	if len(jobs) == 0 {
		return ""
	}
	if e.Run.Status != "completed" {
		done := 0
		for _, j := range jobs {
			if j.Status == "completed" {
				done++
			}
		}
		return ui.Dim.Render(truncateString(fmt.Sprintf("%d/%d jobs", done, len(jobs)), width))
	}
	for _, j := range jobs {
		if runFailed(j.Conclusion) {
			return ui.Red.Render(truncateString(j.Name, width))
		}
	}
	return ""
}
