package app

import (
	"reflect"
	"testing"
	"time"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/github"
	"github.com/JonrGull/prflow/internal/models"
)

// The dry-run fixture has known numbers, so it checks buildHomeData's sums.
func TestBuildHomeDataFromTheFixture(t *testing.T) {
	d := dryRunHomeData(config.DefaultConfig().FlowEntries())
	if len(d.Steps) != 2 {
		t.Fatalf("%d steps, want the default 2", len(d.Steps))
	}

	s := d.Steps[0]
	if s.Ahead != 25 || s.ReposAhead != 5 || s.Compared != 6 {
		t.Errorf("dev→staging: ahead %d in %d of %d repos, want 25 in 5 of 6", s.Ahead, s.ReposAhead, s.Compared)
	}
	if s.Open != 4 || s.Green != 2 || s.Failing != 1 || s.Pending != 1 {
		t.Errorf("dev→staging PRs: %d open, %d green, %d failing, %d pending", s.Open, s.Green, s.Failing, s.Pending)
	}
	if !reflect.DeepEqual(s.Ready, []string{"api-service", "admin"}) {
		t.Errorf("ready = %v", s.Ready)
	}
	// Changes requested and pending CI are not ready.
	if s := d.Steps[1]; s.Open != 2 || len(s.Ready) != 0 || s.Ahead != 3 {
		t.Errorf("staging→main: %+v", s)
	}

	var got []string
	for _, a := range d.Attention {
		got = append(got, a.Repo+": "+a.Detail)
	}
	want := []string{"web-app: CI failing on #212", "billing: merge conflict on #41",
		"api-service: changes requested on #90", "worker: 14 commits, no PR"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("attention =\n%v\nwant\n%v", got, want)
	}

	if d.CIGreen <= 0 || d.CIGreen >= 100 {
		t.Errorf("CIGreen = %d, want a share with the two failures counted", d.CIGreen)
	}
	if d.Runs[0].Run.WorkflowName != "ci" {
		t.Errorf("runs not newest first: %s", d.Runs[0].Run.WorkflowName)
	}
}

// A fork's PR with the release branch names is not the release PR, and its
// repo still counts as having commits but no PR.
func TestBuildHomeDataIgnoresForks(t *testing.T) {
	flow := models.Flow{Head: "dev", Base: "staging"}
	repo := repoNWO{Repo: models.NewRepoInfo("/r", "G/web", "main", "G"), NWO: "acme/web"}
	prs := map[string][]models.GhPr{"acme/web": {{Number: 9, HeadBranch: "dev", BaseBranch: "staging", IsCrossRepository: true}}}
	ahead := map[github.BranchPair]int{{NWO: "acme/web", Base: "staging", Head: "dev"}: 3}

	d := buildHomeData([]models.Flow{flow}, []repoNWO{repo}, prs, ahead, nil, time.Now())
	if d.Steps[0].Open != 0 {
		t.Error("the fork PR was counted as the release PR")
	}
	if len(d.Attention) != 1 || d.Attention[0].Kind != attnNoPR {
		t.Errorf("attention = %+v, want the missing release PR", d.Attention)
	}
}

// Results from a fetch that a newer one replaced are ignored.
func TestOlderHomeFetchIsIgnored(t *testing.T) {
	m := staleModel(ScreenMainMenu)
	_ = m.startHomeFetch() // gen 1
	m.home.loading = false
	_ = m.startHomeFetch() // gen 2
	old := homeFetchedResult{gen: 1, data: homeData{Repos: 99}, at: time.Now()}
	m = send(t, m, old)
	if m.home.data.Repos == 99 || !m.home.loading {
		t.Error("the older fetch's result was applied")
	}
	m = send(t, m, homeFetchedResult{gen: 2, data: homeData{Repos: 3}, at: time.Now()})
	if m.home.data.Repos != 3 || m.home.loading || !m.home.loaded {
		t.Errorf("the current fetch's result was not applied: %+v", m.home)
	}
}
