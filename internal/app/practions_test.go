package app

import (
	"strings"
	"testing"

	"github.com/JonrGull/prflow/internal/github"
	"github.com/JonrGull/prflow/internal/models"
)

func actionPR() allPREntry {
	e := minePR(12, "me", func(pr *models.GhPr) {
		pr.HeadSHA, pr.HeadBranch, pr.BaseBranch = "abc123", "fix/login", "main"
	})
	e.Repo = models.NewRepoInfo("/r/web", "Frontend/web", "main", "Frontend")
	return e
}

func TestMergeVerdict(t *testing.T) {
	e := actionPR()
	for _, tc := range []struct {
		status, sha string
		want        string // the refusal, or "" for a merge to confirm
	}{
		{"CLEAN", "abc123", ""},
		{"HAS_HOOKS", "abc123", ""},
		// Merging would take commits nobody on this screen has seen.
		{"CLEAN", "def456", "web#12 has new commits since the list loaded"},
		{"BLOCKED", "abc123", "web#12 is blocked"},
		{"UNSTABLE", "abc123", "web#12 has failing checks"},
		{"SOMETHING_NEW", "abc123", "web#12 can't be merged now (something_new)"},
	} {
		got := mergeVerdict(e, github.MergeCheck{Status: tc.status, HeadSHA: tc.sha, Method: "SQUASH"})
		switch {
		case tc.want == "" && got.action == nil:
			t.Errorf("%s at %s: refused (%q), want a merge to confirm", tc.status, tc.sha, got.refusal)
		case tc.want != "" && !strings.HasPrefix(got.refusal, tc.want):
			t.Errorf("%s at %s: refusal %q, want it to start %q", tc.status, tc.sha, got.refusal, tc.want)
		}
	}
	if got := mergeVerdict(e, github.MergeCheck{Status: "CLEAN", HeadSHA: "abc123", Method: "SQUASH"}); got.action.prompt() != "Merge web#12 into main (squash)" {
		t.Errorf("prompt = %q", got.action.prompt())
	}
}

func TestRerunVerdict(t *testing.T) {
	e := actionPR()
	if got := rerunVerdict(e, []github.FailedRun{{ID: 1, Name: "CI"}}); got.action == nil || got.action.prompt() != "Re-run failed jobs in CI (web#12)" {
		t.Errorf("with a failed run: %+v", got)
	}
	e.CIStatus = "failure"
	if got := rerunVerdict(e, nil); !strings.Contains(got.refusal, "from another app") {
		t.Errorf("failing CI with no failed run: refusal %q, want it to say the checks are not Actions runs", got.refusal)
	}
}

// y does the checked action, and any other key only cancels it: j must not
// also move the cursor onto a PR the prompt is not about.
func TestPRActionWaitsForY(t *testing.T) {
	m := sized(staleModel(ScreenViewAllPrs))
	m.allPRs.entries = []allPREntry{actionPR(), minePR(13, "me", nil)}
	a := mergeVerdict(actionPR(), github.MergeCheck{Status: "CLEAN", HeadSHA: "abc123", Method: "SQUASH"}).action

	for _, k := range []string{"j", "]", "F", "?"} {
		m.allPRs.pending = a
		m = send(t, m, key(k))
		if m.allPRs.pending != nil || m.allPRs.index != 0 || m.allPRs.running != "" ||
			m.screen != ScreenViewAllPrs || m.fullscreen || m.showHelp {
			t.Errorf("%s: pending %v, cursor %d, running %q, screen %v, fullscreen %v, help %v; want only the prompt cancelled",
				k, m.allPRs.pending, m.allPRs.index, m.allPRs.running, m.screen, m.fullscreen, m.showHelp)
		}
	}

	m.allPRs.pending = a
	next, cmd := m.Update(key("y"))
	if m = next.(Model); m.allPRs.running != "merging web#12" || cmd == nil {
		t.Errorf("y: running %q, cmd %v; want the merge started", m.allPRs.running, cmd != nil)
	}
	if m = send(t, m, key("m")); m.allPRs.checking != "" {
		t.Error("m started a second action while the merge runs")
	}
}

// The check answers a flow: leaving the tab drops it, so the flags saying it
// is coming, and a prompt about a PR no longer shown, have to go too.
func TestLeavingClearsTheCheck(t *testing.T) {
	m := sized(staleModel(ScreenViewAllPrs))
	m.allPRs.entries = []allPREntry{actionPR()}
	m.allPRs.checking = "web#12"
	m = send(t, m, keyNextTab)
	if m.allPRs.checking != "" {
		t.Error("the check is still marked as coming after leaving the tab")
	}
	m.screen, m.allPRs.pending = ScreenViewAllPrs, &prAction{entry: actionPR()}
	if m = send(t, m, keyNextTab); m.allPRs.pending != nil {
		t.Error("a prompt survived leaving the tab")
	}
}

func TestMergedPRIsRecordedAndRefreshed(t *testing.T) {
	m := sized(staleModel(ScreenViewAllPrs))
	e := actionPR()
	m.allPRs.running = "merging web#12"
	m = send(t, m, prActionDoneResult{text: "Merged web#12", merged: &e, refresh: true})
	if m.allPRs.running != "" || !m.allPRs.loading || m.copyFeedback != "✓ Merged web#12" {
		t.Errorf("running %q, loading %v, feedback %q", m.allPRs.running, m.allPRs.loading, m.copyFeedback)
	}
	if n := len(m.sessionPRs); n != 1 || m.sessionPRs[0].action != "merged" || m.sessionPRs[0].prType != "fix/login → main" {
		t.Errorf("session history = %+v, want the merge", m.sessionPRs)
	}
}

func TestPRNWO(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/acme/web/pull/12":  "acme/web",
		"https://github.com/acme/web/issues/3": "",
		"":                                     "",
	} {
		if got := prNWO(url); got != want {
			t.Errorf("prNWO(%q) = %q, want %q", url, got, want)
		}
	}
}
