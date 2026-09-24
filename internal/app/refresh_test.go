package app

import (
	"testing"
	"time"

	"github.com/JonrGull/prflow/internal/models"

	tea "github.com/charmbracelet/bubbletea"
)

// Every fetch result scheduled the next refresh tick, and so did the toggle
// and re-entering the tab. Each of those added a chain beside the running one,
// so the gh calls multiplied the longer the screen stayed open.

func fastTicks(t *testing.T) {
	t.Helper()
	a, p := actionsRefreshEvery, allPRsRefreshEvery
	actionsRefreshEvery, allPRsRefreshEvery = time.Millisecond, time.Millisecond
	t.Cleanup(func() { actionsRefreshEvery, allPRsRefreshEvery = a, p })
}

func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func deliver(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(epochMsg{epoch: m.epoch, msg: msg})
	return next.(Model), cmd
}

// fetches reports whether cmd starts a fetch alongside the next tick. A lone
// tick comes back as the tick; a tick plus a fetch comes back batched.
func fetches(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	if cmd == nil {
		t.Fatal("the chain stopped")
	}
	_, batched := cmd().(tea.BatchMsg)
	return batched
}

func actionsModel() Model {
	m := staleModel(ScreenActionsOverview)
	// A finished run whose jobs are in, so a refresh has no jobs to fetch.
	m.actions.entries = []actionsEntry{{Run: models.WorkflowRun{DatabaseID: 1, Status: "completed"}}}
	m.actions.jobs = map[uint64]runJobs{1: {jobs: []models.WorkflowJob{}, done: true}}
	m.actions.autoRefresh = true
	_ = m.startActionsRefresh()
	return m
}

func TestActionsToggleEndsTheOldChain(t *testing.T) {
	m := actionsModel()
	old := actionsRefreshTickMsg{gen: m.actions.refreshGen}

	m = send(t, m, key("a")) // off
	m = send(t, m, key("a")) // on: a new chain

	if _, cmd := deliver(t, m, old); cmd != nil {
		t.Error("a tick from the chain before the toggle still ran")
	}
	m, cmd := deliver(t, m, actionsRefreshTickMsg{gen: m.actions.refreshGen})
	if cmd == nil || !m.actions.loading {
		t.Error("the current chain's tick did not refresh")
	}
}

func TestActionsFetchResultDoesNotStartAChain(t *testing.T) {
	m := actionsModel()
	if _, cmd := deliver(t, m, actionsRunsFetchedResult{entries: m.actions.entries}); cmd != nil {
		t.Error("a fetch result scheduled a tick")
	}
}

func TestActionsTickSkipsTheFetchWhileOneIsOut(t *testing.T) {
	fastTicks(t)
	m := actionsModel()
	m.actions.loading = true
	if _, cmd := deliver(t, m, actionsRefreshTickMsg{gen: m.actions.refreshGen}); fetches(t, cmd) {
		t.Error("started a second fetch while one was still out")
	}
	m.actions.loading = false
	if _, cmd := deliver(t, m, actionsRefreshTickMsg{gen: m.actions.refreshGen}); !fetches(t, cmd) {
		t.Error("an idle tick did not fetch")
	}
}

func allPRsModel() Model {
	m := staleModel(ScreenViewAllPrs)
	m.allPRs.entries = []allPREntry{{}}
	m.allPRs.autoRefresh = true
	_ = m.startAllPRsRefresh()
	return m
}

func TestAllPRsToggleEndsTheOldChain(t *testing.T) {
	m := allPRsModel()
	old := allPRsRefreshTickMsg{gen: m.allPRs.refreshGen}

	m = send(t, m, key("a"))
	m = send(t, m, key("a"))

	if _, cmd := deliver(t, m, old); cmd != nil {
		t.Error("a tick from the chain before the toggle still ran")
	}
	m, cmd := deliver(t, m, allPRsRefreshTickMsg{gen: m.allPRs.refreshGen})
	if cmd == nil || !m.allPRs.loading {
		t.Error("the current chain's tick did not refresh")
	}
}

// "r" refreshes by hand. Its result started a second chain.
func TestAllPRsManualRefreshDoesNotStartAChain(t *testing.T) {
	m := allPRsModel()
	m = send(t, m, key("r"))
	if _, cmd := deliver(t, m, allOpenPRsFetchedResult{entries: m.allPRs.entries}); cmd != nil {
		t.Error("the refresh result scheduled a tick")
	}
}

// Leaving and coming back by tab starts a chain; the one from before is
// already dropped with the epoch, and must not come back either.
func TestAllPRsTabRoundTripLeavesOneChain(t *testing.T) {
	fastTicks(t)
	m := allPRsModel()
	m.activeTab = 3
	old := allPRsRefreshTickMsg{gen: m.allPRs.refreshGen}

	m = send(t, m, keyNextTab)
	next, cmd := m.navigateToTab(3)
	m = next.(Model)
	if cmd == nil {
		t.Fatal("coming back did not restart auto-refresh")
	}
	if _, cmd := deliver(t, m, old); cmd != nil {
		t.Error("the chain from before the round trip still ran")
	}
}
