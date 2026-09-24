package app

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/github"
	"github.com/JonrGull/prflow/internal/models"
	"github.com/JonrGull/prflow/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
		got = append(got, fmt.Sprintf("%s: #%d %s", a.Repo, a.PR, a.Detail))
	}
	want := []string{"web-app: #212 CI failing", "billing: #41 merge conflict",
		"api-service: #90 changes requested", "worker: #0 14 commits, no PR"}
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

// Ready asks GitHub's merge state. mergeable only rules out conflicts, so a PR
// still needing a required review, or failing the e2e check that CI leaves to
// its own column, was listed as ready to merge.
func TestReadyNeedsGitHubsMergeState(t *testing.T) {
	flow := models.Flow{Head: "dev", Base: "staging"}
	var repos []repoNWO
	prs := map[string][]models.GhPr{}
	for i, state := range []string{"CLEAN", "BLOCKED", "UNSTABLE", "UNKNOWN"} {
		nwo := fmt.Sprintf("acme/r%d", i)
		repos = append(repos, repoNWO{Repo: models.NewRepoInfo("/"+nwo, nwo, "main", "G"), NWO: nwo})
		prs[nwo] = []models.GhPr{{Number: 1, HeadBranch: "dev", BaseBranch: "staging", Mergeable: "MERGEABLE", MergeStateStatus: state,
			StatusCheckRollup: []models.CheckRun{{Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS"}}}}
	}
	d := buildHomeData([]models.Flow{flow}, repos, prs, nil, nil, time.Now())
	if want := []string{"r0"}; !reflect.DeepEqual(d.Steps[0].Ready, want) {
		t.Errorf("ready = %v, want only the CLEAN PR's repo %v", d.Steps[0].Ready, want)
	}

	// The merge state is asked for the release PRs alone: asking in the open-PR
	// search, for every PR in the page, made GitHub time out.
	prs["acme/r0"] = append(prs["acme/r0"], models.GhPr{Number: 7, HeadBranch: "feature", BaseBranch: "dev"})
	refs := releasePRs([]models.Flow{flow}, repos, prs)
	if _, ok := refs[github.PRRef{NWO: "acme/r0", Number: 7}]; ok || len(refs) != len(repos) {
		t.Errorf("release PRs = %v, want one per repo and no feature PR", refs)
	}
}

// Most of one team's repos default to dev on GitHub, and dev → staging →
// @default then released staging into dev. A default branch the chain
// releases from resolves to main or master instead.
func TestDefaultBranchThatIsAChainHeadIsNotTheTarget(t *testing.T) {
	repos := []models.RepoInfo{
		models.NewRepoInfo("/dev-default", "G/a", "dev", "G"),
		models.NewRepoInfo("/main-default", "G/b", "main", "G"),
		models.NewRepoInfo("/trunk-default", "G/c", "trunk", "G"),
	}
	flows := []models.Flow{{Head: "dev", Base: "staging"}, {Head: "staging", Base: models.DefaultBranchToken}}
	releaseTargets(repos, flows, func(path string) string { return "master" })
	var got []string
	for _, r := range repos {
		got = append(got, r.MainBranch)
	}
	if want := []string{"master", "main", "trunk"}; !reflect.DeepEqual(got, want) {
		t.Errorf("targets = %v, want %v", got, want)
	}
}

// A worktree of a repo under the repos dir shares its owner/repo; it was
// counted as a second repo, doubling that repo's commits, PRs and runs.
func TestWorktreeIsNotASecondRepo(t *testing.T) {
	repo := func(path, nwo string) repoNWO {
		return repoNWO{Repo: models.NewRepoInfo(path, path, "main", "G"), NWO: nwo}
	}
	got := uniqueGitHubRepos([]repoNWO{
		repo("/web", "acme/web"), repo("/local", ""), repo("/wt-web", "acme/web"), repo("/api", "acme/api"),
	})
	var paths []string
	for _, r := range got {
		paths = append(paths, r.Repo.Path)
	}
	if want := []string{"/web", "/api"}; !reflect.DeepEqual(paths, want) {
		t.Errorf("repos = %v, want %v", paths, want)
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

// Arriving home refreshes data older than homeStaleAfter, and only that.
func TestArrivingHomeRefreshesStaleData(t *testing.T) {
	for _, tc := range []struct {
		age  time.Duration
		want bool
	}{{time.Minute, false}, {homeStaleAfter + time.Minute, true}} {
		m := staleModel(ScreenSettings)
		m.home = homeState{loaded: true, fetchedAt: timeNow().Add(-tc.age)}
		m = send(t, m, tea.KeyMsg{Type: tea.KeyEsc})
		if m.screen != ScreenMainMenu {
			t.Fatalf("esc from settings went to %v", m.screen)
		}
		if m.home.loading != tc.want {
			t.Errorf("data %v old: refreshing = %v, want %v", tc.age, m.home.loading, tc.want)
		}
	}
}

// A settings change can change the chain and the repos, so the fetch still
// out describes the old settings: its result is dropped, and the next arrival
// home fetches again.
func TestSettingsChangeInvalidatesTheDashboard(t *testing.T) {
	m := staleModel(ScreenSettings)
	m.home = homeState{loaded: true, fetchedAt: timeNow()}
	_ = m.startHomeFetch()
	gen := m.home.gen
	_ = m.applySettingsChange()

	m = send(t, m, homeFetchedResult{gen: gen, data: homeData{Repos: 99}, at: timeNow()})
	if m.home.data.Repos == 99 {
		t.Error("a fetch made under the old settings was applied")
	}
	if m.home.loaded {
		t.Error("the dashboard still shows data from the old settings")
	}
	if !m.homeStale() {
		t.Error("the dashboard is not due a refresh after a settings change")
	}
}

// A failed fetch is retried on the next arrival rather than five minutes on.
func TestArrivingHomeRetriesAFailedFetch(t *testing.T) {
	m := staleModel(ScreenSettings)
	m.home = homeState{err: fmt.Errorf("repo directory not found"), fetchedAt: timeNow()}
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if !m.home.loading {
		t.Error("arriving home after a failed fetch did not retry")
	}
}

// Home is a tab: ] from the last tab goes home, and [ from home goes back to
// it. The Start card highlights the tab you came from.
func TestHomeIsATab(t *testing.T) {
	m := staleModel(ScreenActionsOverview)
	m.activeTab = len(ui.TabNames) - 1
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	if m.screen != ScreenMainMenu || m.menuIndex != m.activeTab {
		t.Fatalf("] from Actions: screen %v, menu index %d", m.screen, m.menuIndex)
	}
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[")})
	if m.screen == ScreenMainMenu || m.activeTab != len(ui.TabNames)-1 {
		t.Errorf("[ from home: screen %v, tab %d", m.screen, m.activeTab)
	}

	single := staleModel(ScreenPrTypeSelect)
	single = send(t, single, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[")})
	if single.screen != ScreenMainMenu {
		t.Errorf("[ from Single went to %v, not home", single.screen)
	}
}

// The pipeline is drawn from whatever chain is configured. A loop, a repeated
// step and a single step must still render inside the frame.
func TestPipelineDrawsOddChains(t *testing.T) {
	chains := [][]config.FlowEntry{
		{{Head: "dev", Base: "staging"}, {Head: "staging", Base: "dev"}},
		{{Head: "dev", Base: "staging"}, {Head: "dev", Base: "staging"}},
		{{Head: "dev", Base: models.DefaultBranchToken}},
		{{Head: "a-very-long-integration-branch", Base: "b"}, {Head: "b", Base: "c"}, {Head: "c", Base: "d"}, {Head: "d", Base: "e"}},
	}
	for _, flows := range chains {
		m := staleModel(ScreenMainMenu)
		m.config.Flows = flows
		m.home = homeState{data: dryRunHomeData(m.flows()), loaded: true, fetchedAt: timeNow()}
		for _, w := range []int{60, 80, 120, 160} {
			m.width, m.height = w, 40
			for i, line := range strings.Split(m.View(), "\n") {
				if got := lipgloss.Width(line); got > w {
					t.Errorf("%v at width %d: line %d is %d wide", flows, w, i+1, got)
					break
				}
			}
		}
	}
}
