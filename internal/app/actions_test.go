package app

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/JonrGull/prflow/internal/models"
	"github.com/JonrGull/prflow/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The pinned panels scroll by line offset, so adjustActionsPinnedScroll has to
// know how tall renderPinnedPanel will draw a panel before it draws it. That
// makes pinnedPanelLines a second, independent statement of the renderer's
// layout, and the failure mode when they drift is silent: the right-hand column
// scrolls to the wrong place, with nothing crashing and no golden moving.
//
// This is the check that makes the two agree by test rather than by memory.
func TestPinnedPanelLinesMatchRender(t *testing.T) {
	m := populatedModel()
	m.width, m.height = 120, 40

	step := func(name string, n int, status, conclusion string) models.WorkflowStep {
		return models.WorkflowStep{Name: name, Number: n, Status: status, Conclusion: conclusion}
	}
	job := func(name, status, conclusion string, steps ...models.WorkflowStep) models.WorkflowJob {
		return models.WorkflowJob{Name: name, Status: status, Conclusion: conclusion, Steps: steps}
	}
	panelWith := func(jobs []models.WorkflowJob) actionsPanel {
		return actionsPanel{
			Run:  models.WorkflowRun{DatabaseID: 900, DisplayTitle: "Deploy", WorkflowName: "deploy", HeadBranch: "dev", UpdatedAt: ago(90 * time.Second)},
			Repo: testRepos()[0],
			Jobs: jobs,
		}
	}

	cases := []struct {
		name  string
		panel actionsPanel
	}{
		// nil Jobs is the loading branch, which renders a spinner line instead
		// of the job list — a different arm of the arithmetic.
		{"loading", panelWith(nil)},
		{"no jobs", panelWith([]models.WorkflowJob{})},

		// A passing job contributes one line and hides its steps. Counting
		// those steps is the mistake this case exists to catch.
		{"success hides steps", panelWith([]models.WorkflowJob{
			job("build", "completed", "success",
				step("checkout", 1, "completed", "success"),
				step("test", 2, "completed", "success")),
		})},

		// A failed job expands, but only its failed steps.
		{"failure expands failed steps", panelWith([]models.WorkflowJob{
			job("build", "completed", "failure",
				step("checkout", 1, "completed", "success"),
				step("test", 2, "completed", "failure"),
				step("lint", 3, "completed", "failure")),
		})},

		// In-progress expands too, on the step's status rather than conclusion.
		{"in progress expands running steps", panelWith([]models.WorkflowJob{
			job("build", "in_progress", "",
				step("checkout", 1, "completed", "success"),
				step("test", 2, "in_progress", "")),
		})},

		{"several jobs", panelWith([]models.WorkflowJob{
			job("build", "completed", "success"),
			job("test", "completed", "failure", step("unit", 1, "completed", "failure")),
			job("deploy", "queued", ""),
		})},
	}

	// Long names at narrow widths: ColumnBox wrapped anything wider than the
	// panel, so at 80 columns a realistic run drew 7 lines against an
	// estimate of 5.
	long := panelWith([]models.WorkflowJob{
		job("integration-tests-against-staging-database", "completed", "failure",
			step("run the complete end-to-end browser suite", 1, "completed", "failure")),
	})
	long.Run.WorkflowName = "deploy-preview-environment"
	long.Run.HeadBranch = "feature/a-rather-long-branch-name"
	long.Run.DisplayTitle = "A pull request title that goes on for a while"
	long.Repo = models.NewRepoInfo("/r", "Frontend/an-unusually-long-repository-name", "main", "Frontend")
	cases = append(cases, struct {
		name  string
		panel actionsPanel
	}{"long names", long})

	for _, width := range []int{60, 42, 30} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("%s at %d", tc.name, width), func(t *testing.T) {
				got := lineCount(m.renderPinnedPanel(tc.panel, ui.ColorCyan, width, false))
				if want := pinnedPanelLines(tc.panel); got != want {
					t.Errorf("renderPinnedPanel drew %d lines, pinnedPanelLines says %d — "+
						"the pinned column will scroll to the wrong offset", got, want)
				}
			})
		}
	}

	// Panels are stacked with JoinVertical and no separator, so the scroller's
	// running total is only right if the heights sum exactly.
	t.Run("panels sum", func(t *testing.T) {
		var blocks []string
		total := 0
		for _, tc := range cases {
			blocks = append(blocks, m.renderPinnedPanel(tc.panel, ui.ColorCyan, 60, false))
			total += pinnedPanelLines(tc.panel)
		}
		if got := lineCount(lipgloss.JoinVertical(lipgloss.Left, blocks...)); got != total {
			t.Errorf("%d panels joined to %d lines, heights sum to %d", len(blocks), got, total)
		}
	})
}

func lineCount(s string) int { return strings.Count(s, "\n") + 1 }

// Typing in the Actions filter: Space pinned the highlighted run and → moved
// to the pinned column mid-word.
func TestActionsFilterKeepsSpaceAndArrows(t *testing.T) {
	m := populatedModel()
	m.screen = ScreenActionsOverview
	m.actions.filterActive = true
	m.actions.filter = "ci"
	m.actions.pinned = []actionsPanel{{Run: m.actions.entries[0].Run}}
	before := len(m.actions.pinned)

	m = send(t, m, tea.KeyMsg{Type: tea.KeySpace})
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRight})

	if m.actions.filter != "ci " || len(m.actions.pinned) != before || m.actions.column != 0 {
		t.Errorf("filter %q, pinned %d, column %d: want the space typed and nothing else",
			m.actions.filter, len(m.actions.pinned), m.actions.column)
	}
}

// A pinned run that finished behind a newer run of the same workflow was
// dropped by the one-finished-run-per-workflow rule, so its panel never
// updated again and spun forever.
func TestPinnedRunSurvivesTheWorkflowFilter(t *testing.T) {
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
		t.Errorf("kept runs %v, want the newest ci run and the pinned one", ids)
	}
	if got := selectActionsRuns(runs, now.Add(-48*time.Hour), nil); len(got) != 1 || got[0].DatabaseID != 2 {
		t.Errorf("unpinned: kept %+v, want only the newest finished ci run", got)
	}
}

// One failed jobs fetch unpinned the run without a word. It stays pinned
// now, and the next refresh fetches its jobs again.
func TestFailedJobsFetchKeepsThePin(t *testing.T) {
	m := populatedModel()
	m.screen = ScreenActionsOverview
	run := m.actions.entries[0].Run
	run.Status = "completed"
	m.actions.pinned = []actionsPanel{{Run: run, Repo: m.actions.entries[0].Repo}}

	m = send(t, m, actionsJobsFetchedResult{runID: run.DatabaseID, err: errors.New("HTTP 502")})
	if len(m.actions.pinned) != 1 {
		t.Fatal("a failed jobs fetch unpinned the run")
	}
	entries := []actionsEntry{{Repo: m.actions.entries[0].Repo, Run: run}}
	_, cmd := m.Update(epochMsg{epoch: m.epoch, msg: actionsRunsFetchedResult{entries: entries}})
	if cmd == nil {
		t.Error("the refresh did not fetch the missing jobs again")
	}
}

// Unpinning the last panels left the scroll past the end: a blank column.
func TestUnpinClampsTheScroll(t *testing.T) {
	m := populatedModel()
	m.screen = ScreenActionsOverview
	m.width, m.height = 120, 40
	for i := 0; i < 8; i++ {
		m.actions.pinned = append(m.actions.pinned, actionsPanel{Run: models.WorkflowRun{DatabaseID: uint64(100 + i)}, Jobs: []models.WorkflowJob{{Name: "j"}}})
	}
	m.actions.pinnedIndex = 7
	m.adjustActionsPinnedScroll()
	for i := 7; i >= 2; i-- {
		m.unpinRun(uint64(100 + i))
	}
	if m.actions.pinnedScroll != 0 {
		t.Errorf("pinnedScroll = %d with two short panels left, want 0", m.actions.pinnedScroll)
	}
}
