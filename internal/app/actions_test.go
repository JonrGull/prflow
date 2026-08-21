package app

import (
	"strings"
	"testing"
	"time"

	"github.com/JonrGull/prflow/internal/models"
	"github.com/JonrGull/prflow/internal/ui"

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

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := lineCount(m.renderPinnedPanel(tc.panel, ui.ColorCyan, 60, false))
			if want := pinnedPanelLines(tc.panel); got != want {
				t.Errorf("renderPinnedPanel drew %d lines, pinnedPanelLines says %d — "+
					"the pinned column will scroll to the wrong offset", got, want)
			}
		})
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
