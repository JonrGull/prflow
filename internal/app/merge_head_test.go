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
