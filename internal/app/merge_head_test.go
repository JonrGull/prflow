package app

import (
	"testing"

	"github.com/JonrGull/prflow/internal/models"
)

// The merge pins the head commit the list showed, so it has to reach the
// entry the merge is started from.
func TestListedHeadCommitReachesTheMerge(t *testing.T) {
	m := staleModel(ScreenLoading)
	flow := m.flows()[0]
	pr := &models.GhPr{Number: 7, HeadSHA: "abc123"}
	m = send(t, m, epochMsg{epoch: m.epoch, msg: openPRsFetchedResult{entries: []OpenPREntry{{
		Repo:   testRepos()[0],
		Status: models.RepoPrStatus{Flows: []models.FlowPR{{Flow: flow, PR: pr}}},
	}}}})

	if len(m.merge.prs) != 1 || m.merge.prs[0].HeadSHA != "abc123" {
		t.Fatalf("merge entries = %+v, want one with head abc123", m.merge.prs)
	}
}

// From the merge summary the tab keys work, and tabbing back to Release PRs
// showed the cached list: the PRs just merged, still ticked, one Enter away
// from being merged again.
func TestReleasePRsRefetchAfterAMerge(t *testing.T) {
	m := staleModel(ScreenMerging)
	flow := m.flows()[0]
	m.merge.openPRs = []OpenPREntry{{Repo: testRepos()[0],
		Status: models.RepoPrStatus{Flows: []models.FlowPR{{Flow: flow, PR: &models.GhPr{Number: 7}}}}}}
	m.merge.prs = []models.MergePrEntry{{Repo: testRepos()[0], PrNumber: 7, Flow: flow}}
	m.merge.selected = []bool{true}
	m.merge.results = []models.MergeResult{{PrNumber: 7, Success: true}}

	next, _ := m.finishMerging()
	m = next.(Model)
	m.activeTab = 0
	next, cmd := m.navigateToTab(2)
	m = next.(Model)

	if m.screen == ScreenViewOpenPrs {
		t.Errorf("showed the cached list after a merge; selected = %v", m.merge.selected)
	}
	if cmd == nil {
		t.Error("no fetch started for the Release PRs list")
	}
}
