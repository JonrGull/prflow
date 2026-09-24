package app

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/JonrGull/prflow/internal/models"

	tea "github.com/charmbracelet/bubbletea"
)

func actionsRun(id uint64, workflow string, repo models.RepoInfo) actionsEntry {
	return actionsEntry{Repo: repo, Run: models.WorkflowRun{DatabaseID: id, WorkflowName: workflow,
		Status: "completed", Conclusion: "success", HeadBranch: "main", UpdatedAt: time.Now()}}
}

// The list was sorted by time but grouped by repo, so a header was drawn each
// time the repo changed: 24 runs from three repos drew 20 headers, and a
// 40-row terminal showed about eight runs. One line per run shows them all.
func TestActionsListIsOneLinePerRun(t *testing.T) {
	m := sized(staleModel(ScreenActionsOverview))
	repos := testRepos()
	for i := 0; i < 24; i++ {
		m.actions.entries = append(m.actions.entries, actionsRun(uint64(i+1), fmt.Sprintf("wf-%02d", i), repos[i%3]))
	}
	view := m.View()
	for i := 0; i < 24; i++ {
		if want := fmt.Sprintf("wf-%02d", i); !strings.Contains(view, want) {
			t.Fatalf("%s is not on screen: the list shows fewer runs than fit", want)
		}
	}
}

// Repos in one org share a prefix, so cutting the end of the name left every
// row reading "attuned.marketi…" or "attuned.resonan…".
func TestActionsRepoNamesKeepTheirEnd(t *testing.T) {
	if got := truncateStart("attuned.marketing_site", 15); got != "…marketing_site" {
		t.Errorf("got %q", got)
	}
	if got := truncateStart("web", 16); got != "web" {
		t.Errorf("a name that fits was cut: %q", got)
	}
}

// A run's jobs only showed once it was pinned and the pinned column entered.
// The detail pane follows the cursor, fetching once the cursor rests.
func TestActionsDetailFollowsTheCursor(t *testing.T) {
	m := sized(staleModel(ScreenActionsOverview))
	repos := testRepos()
	m.actions.entries = []actionsEntry{actionsRun(1, "ci", repos[0]), actionsRun(2, "deploy", repos[1])}

	next, cmd := m.Update(keyDown)
	m = next.(Model)
	if cmd == nil {
		t.Fatal("moving the cursor scheduled no preview")
	}
	if len(m.actions.jobs) != 0 {
		t.Error("a fetch started before the cursor rested, so holding ↓ fetches every run")
	}

	m, cmd = deliver(t, m, actionsPreviewMsg{runID: 1})
	if cmd != nil {
		t.Error("a preview for a run the cursor has left started a fetch")
	}
	m, cmd = deliver(t, m, actionsPreviewMsg{runID: 2})
	if cmd == nil || !m.actions.jobs[2].loading {
		t.Fatal("the highlighted run's preview did not fetch its jobs")
	}

	m = send(t, m, actionsJobsFetchedResult{runID: 2, of: m.actions.entries[1].Run.UpdatedAt,
		jobs: []models.WorkflowJob{{Name: "publish-images", Status: "completed", Conclusion: "success"}}})
	if !strings.Contains(m.View(), "publish-images") {
		t.Error("the fetched jobs are not in the detail pane")
	}
}

// With the filter open there was no Enter, Space typed a space and Esc
// cleared it, so a filtered run could not be pinned. Enter keeps it now.
func TestActionsFilterCanBeKept(t *testing.T) {
	m := sized(staleModel(ScreenActionsOverview))
	repos := testRepos()
	m.actions.entries = []actionsEntry{actionsRun(1, "ci", repos[0]), actionsRun(2, "deploy", repos[1]), actionsRun(3, "ci", repos[2])}

	m = send(t, m, key("/"))
	m = send(t, m, key("deploy"))
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.actions.filterTyping || m.actions.filter != "deploy" || len(m.getFilteredActions()) != 1 {
		t.Fatalf("after Enter: typing %v, filter %q, %d runs", m.actions.filterTyping, m.actions.filter, len(m.getFilteredActions()))
	}
	m = send(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if !m.isWatched(2) {
		t.Fatal("Space did not watch the filtered run")
	}

	m = send(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if e, _ := m.highlightedRun(); m.actions.filter != "" || e.Run.DatabaseID != 2 {
		t.Errorf("Esc: filter %q, cursor on run %d, want the filter gone and the cursor still on run 2", m.actions.filter, e.Run.DatabaseID)
	}
	// Esc called reset(), which threw the watched runs away; the tab keys kept them.
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.screen != ScreenMainMenu || !m.isWatched(2) {
		t.Errorf("second Esc: screen %v, watching run 2 %v", m.screen, m.isWatched(2))
	}
}

// Typing in the Actions filter: Space pinned the highlighted run and → moved
// to the pinned column mid-word.
func TestActionsFilterKeepsSpaceAndArrows(t *testing.T) {
	m := populatedModel()
	m.screen = ScreenActionsOverview
	m.actions.filterTyping = true
	m.actions.filter = "ci"
	before := len(m.actions.watched)

	m = send(t, m, tea.KeyMsg{Type: tea.KeySpace})
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRight})
	m = send(t, m, key("o"))

	if m.actions.filter != "ci o" || len(m.actions.watched) != before {
		t.Errorf("filter %q, watched %d: want the text typed and nothing else", m.actions.filter, len(m.actions.watched))
	}
}

// New runs arrive at the top of the list, so on every refresh the highlight
// slid to whichever run now had its row.
func TestActionsRefreshKeepsTheCursorOnItsRun(t *testing.T) {
	m := sized(staleModel(ScreenActionsOverview))
	repos := testRepos()
	m.actions.entries = []actionsEntry{actionsRun(1, "a", repos[0]), actionsRun(2, "b", repos[0]), actionsRun(3, "c", repos[0])}
	m.actions.index = 1

	fresh := append([]actionsEntry{actionsRun(9, "new", repos[1])}, m.actions.entries...)
	m, _ = deliver(t, m, actionsRunsFetchedResult{entries: fresh})
	if e, _ := m.highlightedRun(); e.Run.DatabaseID != 2 {
		t.Errorf("cursor on run %d after the refresh, want run 2", e.Run.DatabaseID)
	}
}

// A watched run that finished behind a newer run of the same workflow was
// dropped by the one-finished-run-per-workflow rule, so it never updated
// again.
func TestWatchedRunSurvivesTheWorkflowFilter(t *testing.T) {
	now := time.Now()
	runs := []models.WorkflowRun{
		{DatabaseID: 2, WorkflowName: "ci", Status: "completed", UpdatedAt: now},
		{DatabaseID: 1, WorkflowName: "ci", Status: "completed", UpdatedAt: now.Add(-time.Minute)},
	}
	var ids []uint64
	for _, r := range selectActionsRuns(runs, now.Add(-48*time.Hour), map[uint64]bool{1: true}) {
		ids = append(ids, r.DatabaseID)
	}
	if len(ids) != 2 {
		t.Errorf("kept runs %v, want the newest ci run and the watched one", ids)
	}
	if got := selectActionsRuns(runs, now.Add(-48*time.Hour), nil); len(got) != 1 || got[0].DatabaseID != 2 {
		t.Errorf("unwatched: kept %+v, want only the newest finished ci run", got)
	}
}

// A failed jobs fetch is shown, and the next refresh asks again rather than
// leaving the pane on the error.
func TestFailedJobsFetchIsAskedAgain(t *testing.T) {
	m := sized(staleModel(ScreenActionsOverview))
	e := actionsRun(1, "ci", testRepos()[0])
	m.actions.entries = []actionsEntry{e}
	_ = m.fetchRunJobs(e)

	m = send(t, m, actionsJobsFetchedResult{runID: 1, of: e.Run.UpdatedAt, err: errors.New("HTTP 502")})
	if !strings.Contains(m.View(), "Couldn't load jobs: HTTP 502") {
		t.Error("the failed fetch is not shown")
	}
	if _, cmd := deliver(t, m, actionsRunsFetchedResult{entries: m.actions.entries}); cmd == nil {
		t.Error("the refresh did not fetch the missing jobs again")
	}

	// A finished run's jobs, once fetched, are final.
	m = send(t, m, actionsJobsFetchedResult{runID: 1, of: e.Run.UpdatedAt, done: true, jobs: []models.WorkflowJob{{Name: "build"}}})
	if _, cmd := deliver(t, m, actionsRunsFetchedResult{entries: m.actions.entries}); cmd != nil {
		t.Error("a finished run's jobs were fetched again")
	}
}

// A rerun keeps the run's ID, and its jobs were cached as final, so the pane
// showed the previous attempt's failure beside a run that now passed.
func TestActionsRerunFetchesNewJobs(t *testing.T) {
	m := sized(staleModel(ScreenActionsOverview))
	e := actionsRun(1, "ci", testRepos()[0])
	m.actions.entries = []actionsEntry{e}
	_ = m.fetchRunJobs(e)
	m = send(t, m, actionsJobsFetchedResult{runID: 1, of: e.Run.UpdatedAt, done: true, jobs: []models.WorkflowJob{{Name: "build"}}})

	rerun := e
	rerun.Run.UpdatedAt = e.Run.UpdatedAt.Add(time.Minute)
	if _, cmd := deliver(t, m, actionsRunsFetchedResult{entries: []actionsEntry{rerun}}); cmd == nil {
		t.Error("the rerun's jobs were not fetched")
	}
}

// Answers can arrive out of order. An older request's jobs must not replace
// a newer one's, or the newer run's state is lost.
func TestActionsOlderJobsAnswerIsDropped(t *testing.T) {
	m := staleModel(ScreenActionsOverview)
	e := actionsRun(1, "ci", testRepos()[0])
	_ = m.fetchRunJobs(e)
	m.actions.jobs[1] = runJobs{loading: true, asked: e.Run.UpdatedAt.Add(time.Minute)}

	m = send(t, m, actionsJobsFetchedResult{runID: 1, of: e.Run.UpdatedAt, jobs: []models.WorkflowJob{{Name: "old"}}})
	if c := m.actions.jobs[1]; c.jobs != nil || !c.loading {
		t.Errorf("jobs = %+v, want the older answer dropped and the newer request still out", c)
	}
}

// A watched run pushed out of its repo's latest runs is asked for by ID.
func TestUnfetchedWatchedRuns(t *testing.T) {
	repo := testRepos()[0]
	watched := []actionsEntry{actionsRun(1, "deploy", repo), actionsRun(2, "ci", repo)}
	got := unfetchedWatched(watched, map[uint64]bool{2: true, 3: true})
	if len(got) != 1 || got[0].Run.DatabaseID != 1 {
		t.Errorf("unfetched = %+v, want only run 1", got)
	}
}

func lineCount(s string) int { return strings.Count(s, "\n") + 1 }
