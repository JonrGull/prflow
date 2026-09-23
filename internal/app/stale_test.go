package app

import (
	"testing"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/models"

	tea "github.com/charmbracelet/bubbletea"
)

// Results used to be applied whenever they landed, and every handler assumed
// the screen that asked was still showing. So a result that arrived after the
// user had moved on acted on whatever came next. These drive Update, which is
// where results are now stamped and dropped.

var (
	keyNextTab = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")}
	keyEsc     = tea.KeyMsg{Type: tea.KeyEsc}
	keyEnter   = tea.KeyMsg{Type: tea.KeyEnter}
)

func send(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(Model)
}

// staleModel is a model that will not start the animation tick, so Update
// returns only what the handler returned.
func staleModel(screen Screen) Model {
	return Model{config: config.DefaultConfig(), screen: screen, dryRun: true, tickRunning: true}
}

func TestTagEpochStampsOnlyFlowResults(t *testing.T) {
	cmd := tea.Batch(
		func() tea.Msg { return fetchCommitsResult{} },
		func() tea.Msg { return tickMsg{} },
	)
	batch, ok := tagEpoch(7, cmd)().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("a batch should stay a batch of the same size, got %#v", batch)
	}
	if e, ok := batch[0]().(epochMsg); !ok || e.epoch != 7 {
		t.Errorf("flow result = %#v, want it stamped with epoch 7", batch[0]())
	}
	if _, ok := batch[1]().(tickMsg); !ok {
		t.Error("the animation tick was wrapped; only flow results should be")
	}
	// bubbletea's own messages must reach it untouched, or quitting breaks.
	if _, ok := tagEpoch(7, tea.Quit)().(tea.QuitMsg); !ok {
		t.Error("tea.Quit was wrapped")
	}
}

// Single mode waiting on its commit fetch, then ] to Batch: the fetch used to
// land and pull the user back to CommitReview in the middle of the batch flow.
func TestLateResultAfterTabSwitchIsDropped(t *testing.T) {
	loading := func() Model {
		m := staleModel(ScreenLoading)
		mode := ModeSingle
		m.mode = &mode
		repo := testRepos()[0]
		m.repoInfo = &repo
		return m
	}
	late := func(m Model) epochMsg {
		return epochMsg{epoch: m.epoch, msg: fetchCommitsResult{commits: make([]models.CommitInfo, 1)}}
	}

	// Control: with no navigation, the same result is applied.
	m := loading()
	if got := send(t, m, late(m)); got.screen != ScreenCommitReview {
		t.Fatalf("a current result went to %v, want CommitReview", got.screen)
	}

	m = loading()
	result := late(m)
	m = send(t, m, keyNextTab)
	if m.screen != ScreenPrTypeSelect {
		t.Fatalf("] from Single went to %v, want the Batch step picker", m.screen)
	}
	m = send(t, m, result)
	if m.screen != ScreenPrTypeSelect || m.commits != nil {
		t.Errorf("late fetch applied: screen %v, %d commits", m.screen, len(m.commits))
	}
}

// Leaving through Esc is the other way out, and the one a refresh most often
// outlives: r on All PRs, Esc, and the result used to reopen All PRs.
func TestLateRefreshAfterLeavingIsDropped(t *testing.T) {
	m := staleModel(ScreenViewAllPrs)
	m.allPRs.loading = true
	result := epochMsg{epoch: m.epoch, msg: allOpenPRsFetchedResult{}}

	m = send(t, m, keyEsc)
	if m.screen != ScreenMainMenu {
		t.Fatalf("Esc went to %v, want MainMenu", m.screen)
	}
	m = send(t, m, result)
	if m.screen != ScreenMainMenu {
		t.Errorf("late refresh moved the user to %v", m.screen)
	}
}

// Esc from the batch repo list back to the step picker, then a different step:
// the first step's fetch was still streaming, and its commits landed in the
// new run.
func TestBatchStreamFromPreviousStepIsDropped(t *testing.T) {
	m := staleModel(ScreenBatchRepoSelect)
	mode := ModeBatch
	m.mode = &mode
	m.batch.repos = testRepos()
	m.batch.repoCommits = make([]*[]models.CommitInfo, len(m.batch.repos))
	m.batch.selected = make([]bool, len(m.batch.repos))
	m.batch.fetchPending = len(m.batch.repos)
	result := epochMsg{epoch: m.epoch, msg: batchRepoCommitResult{index: 0, commits: make([]models.CommitInfo, 2)}}

	m = send(t, m, keyEsc)
	m.menuIndex = 1
	m = send(t, m, keyEnter)
	if m.screen != ScreenLoading || m.flow == nil || *m.flow != m.flows()[1] {
		t.Fatalf("picking the second step: screen %v, flow %v", m.screen, m.flow)
	}

	m = send(t, m, result)
	if m.batch.repoCommits[0] != nil {
		t.Error("the previous step's commits were written into the new run")
	}
}

// A write in progress cannot be left: its remaining results would be dropped
// as stale, and leaving is what let a batch start a PR in a repo ticked later.
func TestTabKeysIgnoredWhileAWriteRuns(t *testing.T) {
	for _, s := range []Screen{ScreenCreating, ScreenBatchProcessing, ScreenMerging, ScreenPullProgress, ScreenUpdating} {
		m := staleModel(s)
		m.activeTab = 1
		got := send(t, m, keyNextTab)
		if got.screen != s || got.epoch != m.epoch {
			t.Errorf("] on %v went to %v (epoch %d → %d)", s, got.screen, m.epoch, got.epoch)
		}
	}
}
