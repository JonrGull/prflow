package app

import (
	"testing"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/models"

	tea "github.com/charmbracelet/bubbletea"
)

// The open-PRs screen used to hold a column flag plus a devIndex and a
// mainIndex, and filtered each column against a PrType constant. That is what
// pinned it to exactly two release steps. It is driven by the configured steps
// now, so these cover a chain that is neither two steps long nor complete.

func threeStepModel() Model {
	cfg := config.DefaultConfig()
	cfg.Flows = []config.FlowEntry{
		{Head: "dev", Base: "qa"},
		{Head: "qa", Base: "staging"},
		{Head: "staging", Base: models.DefaultBranchToken},
	}
	flows := cfg.FlowEntries()
	repos := testRepos()

	m := Model{config: cfg, screen: ScreenViewOpenPrs, width: 120, height: 40}
	m.merge.prs = []models.MergePrEntry{
		{Repo: repos[0], PrNumber: 1, Flow: flows[0]},
		{Repo: repos[1], PrNumber: 2, Flow: flows[2]},
		{Repo: repos[2], PrNumber: 3, Flow: flows[2]},
	}
	m.merge.selected = make([]bool, len(m.merge.prs))
	m.merge.cursors = make([]int, len(flows))
	return m
}

func TestMergeColumnsFollowTheConfiguredSteps(t *testing.T) {
	m := threeStepModel()

	for column, want := range [][]int{{0}, {}, {1, 2}} {
		got := m.getFilteredMergePRs(column)
		if len(got) != len(want) {
			t.Fatalf("column %d = %v, want %v", column, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("column %d = %v, want %v", column, got, want)
			}
		}
	}

	// A fourth column does not exist; asking for one must not panic or wrap.
	if got := m.getFilteredMergePRs(3); got != nil {
		t.Errorf("column 3 = %v, want nil", got)
	}
}

// Right from the first column has to reach the third: the middle step has no
// open PRs, and stopping there would strand the cursor on an empty column.
func TestMergeColumnNavigationSkipsEmptySteps(t *testing.T) {
	m := threeStepModel()

	m.stepMergeColumn(1)
	if m.merge.column != 2 {
		t.Fatalf("right from column 0 landed on %d, want 2", m.merge.column)
	}

	m.stepMergeColumn(1)
	if m.merge.column != 2 {
		t.Errorf("right from the last column moved to %d, want 2", m.merge.column)
	}

	m.stepMergeColumn(-1)
	if m.merge.column != 0 {
		t.Errorf("left from column 2 landed on %d, want 0", m.merge.column)
	}
}

// Each column keeps its own cursor, and moving to a shorter column clamps it
// rather than pointing past the end.
func TestMergeCursorsArePerColumnAndClamped(t *testing.T) {
	m := threeStepModel()
	m.merge.column = 2
	m.setMergeCursor(2, 1)

	m.stepMergeColumn(-1)
	if m.merge.column != 0 || m.mergeCursor(0) != 0 {
		t.Fatalf("column %d cursor %d, want column 0 cursor 0", m.merge.column, m.mergeCursor(0))
	}
	if m.mergeCursor(2) != 1 {
		t.Errorf("column 2 cursor = %d, want it kept at 1", m.mergeCursor(2))
	}

	// Column 0 holds one PR, so down wraps back to it rather than to index 1.
	m.navigateMergeColumn(false)
	if got := m.mergeCursor(0); got != 0 {
		t.Errorf("cursor after down in a one-row column = %d, want 0", got)
	}
}

// Space selects the PR under the cursor of the *current* column, which is the
// bug the two hardcoded indices made possible.
func TestMergeSelectionTogglesTheHighlightedPR(t *testing.T) {
	m := threeStepModel()
	m.merge.column = 2
	m.setMergeCursor(2, 1)

	m.toggleMergeSelection()
	if !m.merge.selected[2] {
		t.Errorf("selected = %v, want the third PR selected", m.merge.selected)
	}
	if m.merge.selected[0] || m.merge.selected[1] {
		t.Errorf("selected = %v, want only the third PR", m.merge.selected)
	}
}

// A cursors slice shorter than the column count must render rather than panic:
// the config can gain a step while the screen is open.
func TestMergeScreenSurvivesAConfigThatGrewAStep(t *testing.T) {
	m := threeStepModel()
	m.merge.cursors = nil
	m.merge.column = 2

	if got := m.mergeCursor(2); got != 0 {
		t.Errorf("cursor with no cursors slice = %d, want 0", got)
	}
	// Rendering is the part that would panic on an out-of-range index.
	if out := m.renderViewOpenPrsWithHeight(20); out == "" {
		t.Error("rendered nothing")
	}

	// And a PR whose step is no longer configured simply drops out of the
	// columns instead of being counted in one of them.
	m.merge.prs[0].Flow = models.Flow{Head: "gone", Base: "nowhere"}
	total := 0
	for c := range m.flows() {
		total += len(m.getFilteredMergePRs(c))
	}
	if total != 2 {
		t.Errorf("%d PRs across the columns, want 2 with one step unconfigured", total)
	}
}

// The QA note used to say "production" only for the StagingToMain enum value.
func TestQaEnvironmentNamesTheLastStepProduction(t *testing.T) {
	m := threeStepModel()
	flows := m.flows()

	if got := m.qaEnvironment(); got != "staging" {
		t.Errorf("no merges = %q, want the staging fallback", got)
	}

	m.merge.results = []models.MergeResult{{Success: true, Flow: flows[0], MainBranch: "main"}}
	if got := m.qaEnvironment(); got != "qa" {
		t.Errorf("first step = %q, want %q", got, "qa")
	}

	m.merge.results = append(m.merge.results, models.MergeResult{Success: true, Flow: flows[2], MainBranch: "main"})
	if got := m.qaEnvironment(); got != "production" {
		t.Errorf("last step = %q, want %q", got, "production")
	}

	// A failed release is not a release.
	m.merge.results = []models.MergeResult{{Success: false, Flow: flows[2], MainBranch: "main"}}
	if got := m.qaEnvironment(); got != "staging" {
		t.Errorf("failed last step = %q, want the staging fallback", got)
	}
}

// Selecting a step on the PR-type screen must index into the configured steps,
// not into a fixed pair.
func TestSelectingAStepUsesTheConfiguredFlow(t *testing.T) {
	m := threeStepModel()
	m.screen = ScreenPrTypeSelect
	m.dryRun = true
	m.menuIndex = 2

	next, _ := m.selectPrType()
	got := next.(Model)
	if got.flow == nil {
		t.Fatal("no step selected")
	}
	if *got.flow != m.flows()[2] {
		t.Errorf("selected %+v, want %+v", *got.flow, m.flows()[2])
	}

	// "3" has to reach the third step; the handler used to accept only 1 and 2.
	byKey, _ := Model{config: m.config, screen: ScreenPrTypeSelect, dryRun: true}.
		handlePrTypeSelectKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	if f := byKey.(Model).flow; f == nil || *f != m.flows()[2] {
		t.Errorf("pressing 3 selected %v, want the third step", f)
	}
}
