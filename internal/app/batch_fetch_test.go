package app

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/models"
)

func batchSelectModel() Model {
	m := staleModel(ScreenBatchRepoSelect)
	mode := ModeBatch
	m.mode = &mode
	flow := m.flows()[0]
	m.flow = &flow
	m.batch.repos = testRepos()
	m.batch.repoCommits = make([]*[]models.CommitInfo, len(m.batch.repos))
	m.batch.repoErrs = make([]string, len(m.batch.repos))
	m.batch.selected = make([]bool, len(m.batch.repos))
	m.batch.fetchPending = len(m.batch.repos)
	m.batch.resultsChan = make(chan batchRepoCommitResult, len(m.batch.repos))
	return m
}

func commitsFor(m Model, i int) epochMsg {
	return epochMsg{epoch: m.epoch, msg: batchRepoCommitResult{index: i, commits: make([]models.CommitInfo, 2)}}
}

// Enter cancelled the fetch of repos that hadn't finished. Esc from the title
// input comes back to the list, where they spun forever, and selecting one
// hung on "Waiting for 1 repo(s)" with nothing left to wait for.
func TestRepoStillLoadingWhenYouMoveOnCanStillBeSelected(t *testing.T) {
	m := batchSelectModel()
	cancelled := false
	m.batch.fetchCancel = func() { cancelled = true }

	m = send(t, m, commitsFor(m, 0))
	m.batch.selected[0] = true
	m = send(t, m, keyEnter)
	if cancelled || m.batch.resultsChan == nil {
		t.Fatal("moving on cancelled the fetch of repos still loading")
	}

	m = send(t, m, epochMsg{epoch: m.epoch, msg: batchCommitsResult{reposWithCommits: 1}})
	m = send(t, m, keyEsc)
	if m.screen != ScreenBatchRepoSelect {
		t.Fatalf("Esc from the title input: screen %v, want the repo list", m.screen)
	}

	m.batch.selected[3] = true // still loading
	m = send(t, m, keyEnter)
	if !m.batch.waiting || !strings.Contains(m.loadingMessage, "Waiting for 1") {
		t.Fatalf("waiting = %v, message %q", m.batch.waiting, m.loadingMessage)
	}

	m = send(t, m, commitsFor(m, 3))
	if m.batch.waiting || m.loadingMessage != "Checking for existing PRs..." {
		t.Errorf("after the repo arrived: waiting = %v, message %q", m.batch.waiting, m.loadingMessage)
	}
}

// Results that land after Enter must not restart the existing-PR check: only
// an Enter that is waiting for them does.
func TestLateRepoResultDoesNotRepeatTheExistingPRCheck(t *testing.T) {
	m := batchSelectModel()
	m = send(t, m, commitsFor(m, 0))
	m.batch.selected[0] = true
	m = send(t, m, keyEnter) // Loading: "Checking for existing PRs..."

	next, cmd := m.Update(commitsFor(m, 1))
	m = next.(Model)
	if m.batch.repoCommits[1] == nil {
		t.Error("the late result was not recorded")
	}
	if cmd == nil {
		t.Fatal("stopped listening with repos still fetching")
	}
	// Queue a result so the listener returns. A second existing-PR check
	// would come back batched alongside it instead.
	m.batch.resultsChan <- batchRepoCommitResult{index: 2}
	if e, ok := cmd().(epochMsg); !ok {
		t.Errorf("got %T, want only the listener's result", cmd())
	} else if _, ok := e.msg.(batchRepoCommitResult); !ok {
		t.Errorf("got %T, want a repo result", e.msg)
	}
}

// A failed fetch came back as zero commits, so the list showed the repo as up
// to date. This runs the real background fetch on a repo with no remote.
func TestBatchFetchReportsAFailedFetch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	cfg := config.DefaultConfig()
	cfg.Globs = nil
	cfg.Repos = []config.RepoEntry{{Path: dir, Group: "Frontend"}}
	flow := cfg.FlowEntries()[0]
	ch := make(chan batchRepoCommitResult, 1)

	loaded, ok := loadBatchReposCmd(cfg, &flow, false, ch)().(batchReposLoadedResult)
	if !ok || len(loaded.repos) != 1 {
		t.Fatalf("discovery: %#v", loaded)
	}
	defer loaded.cancelFunc()

	got := <-ch
	if got.err == nil {
		t.Errorf("fetch from a repo with no remote: err = nil, commits = %d", len(got.commits))
	}
}
