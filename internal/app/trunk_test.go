package app

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/models"
	"github.com/JonrGull/prflow/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
)

func trunkModel(screen Screen) Model {
	m := sized(staleModel(screen))
	m.config.Branching = config.BranchingTrunk
	return m
}

// A tab's ID was also its position: ] added one to it, and a Start row number
// was the tab to open. With the chain's tabs hidden, both landed on them.
func TestTrunkTabsSkipTheChain(t *testing.T) {
	m := trunkModel(ScreenMainMenu)
	var visited []int
	for i := 0; i < 4; i++ {
		m = send(t, m, key("]"))
		visited = append(visited, m.activeTabForDisplay())
	}
	if want := []int{ui.TabAllPRs, ui.TabActions, ui.TabShipped, ui.HomeTab}; !reflect.DeepEqual(visited, want) {
		t.Errorf("] from Home visited %v, want %v", visited, want)
	}
	if m = send(t, m, key("[")); m.activeTabForDisplay() != ui.TabShipped {
		t.Errorf("[ from Home went to %d, want Shipped", m.activeTabForDisplay())
	}
	if next, _ := m.navigateToTab(ui.TabSingle); next.(Model).activeTabForDisplay() != m.activeTabForDisplay() {
		t.Error("navigating to a hidden tab moved")
	}
}

func TestTrunkStartRows(t *testing.T) {
	home := trunkModel(ScreenMainMenu)
	if m := send(t, home, key("1")); m.activeTab != ui.TabAllPRs {
		t.Errorf("1 opened tab %d, want All PRs", m.activeTab)
	}
	// There is no fifth row: the digit used to open the row already selected.
	if m := send(t, home, key("5")); m.screen != ScreenMainMenu {
		t.Errorf("5 left Home for %v", m.screen)
	}
	m := send(t, home, key("a"))
	if m.activeTab != ui.TabActions {
		t.Fatalf("a opened tab %d, want Actions", m.activeTab)
	}
	m.screen = ScreenActionsOverview
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.screen != ScreenMainMenu || m.menuIndex != 1 {
		t.Errorf("back from Actions: screen %v, row %d, want Home on row 1 (Actions)", m.screen, m.menuIndex)
	}
}

// The header draws its tabs three ways by width; each must leave out the
// chain's. The narrowest is below the app's own minimum width, so the header
// is drawn directly.
func TestTrunkHeaderHidesTheChainAtEveryWidth(t *testing.T) {
	tabs := trunkModel(ScreenMainMenu).visibleTabs()
	for w := 5; w <= 160; w += 5 {
		header := stripANSI(ui.RenderHeader(ui.HeaderInfo{ActiveTab: ui.HomeTab, Tabs: tabs, Meta: "gh: someone"}, w))
		for _, hidden := range []string{"Single", "Batch", "Release"} {
			if strings.Contains(header, hidden) {
				t.Errorf("width %d: header shows %s: %q", w, hidden, header)
			}
		}
	}
}

func TestTrunkPullsDefaultBranches(t *testing.T) {
	m := send(t, trunkModel(ScreenMainMenu), key("p"))
	if m.screen != ScreenLoading || !m.pull.useDefault {
		t.Errorf("p: screen %v, useDefault %v, want each repo's default branch pulled", m.screen, m.pull.useDefault)
	}
}

// Trunk-based repos release their real default branch. The saved chain still
// has dev as a head, and the @default rule would have swapped a dev-default
// repo's branch for main.
func TestTrunkKeepsTheRealDefaultBranch(t *testing.T) {
	cfg := testConfig()
	repos := func() []models.RepoInfo { return []models.RepoInfo{models.NewRepoInfo("/x", "G/x", "dev", "G")} }
	chain := repos()
	resolveTargets(cfg, chain)
	cfg.Branching = config.BranchingTrunk
	trunk := repos()
	resolveTargets(cfg, trunk)
	if chain[0].MainBranch == "dev" || trunk[0].MainBranch != "dev" {
		t.Errorf("default branch: chain %q, trunk %q; want the chain's swapped and trunk's dev", chain[0].MainBranch, trunk[0].MainBranch)
	}
	chainKey := repoCacheKeyFor(testConfig())
	if repoCacheKeyFor(cfg) == chainKey {
		t.Error("the repo cache key does not change with the branching model")
	}
}

// The fixture has one PR of each kind, so it checks the trunk arithmetic.
func TestBuildTrunkHomeData(t *testing.T) {
	d := dryRunTrunkHomeData()
	s := d.IntoDefault
	if s.Open != 6 || s.Drafts != 2 || s.Green != 3 || s.Failing != 1 || s.Pending != 2 {
		t.Errorf("open %d, drafts %d, ✓%d ✗%d ◐%d; want 6 open (the stacked PR left out), 2 drafts, ✓3 ✗1 ◐2",
			s.Open, s.Drafts, s.Green, s.Failing, s.Pending)
	}
	if s.AwaitingReview != 3 || s.ChangesRequested != 1 {
		t.Errorf("reviews: %d waiting, %d changes requested; want 3 and 1", s.AwaitingReview, s.ChangesRequested)
	}
	if want := []string{"api-service#120", "api-service#121"}; !reflect.DeepEqual(s.Ready, want) {
		t.Errorf("ready = %v, want %v", s.Ready, want)
	}
	var got []string
	for _, a := range d.Attention {
		got = append(got, fmt.Sprintf("%s#%d %s", a.Repo, a.PR, a.Detail))
	}
	want := []string{"web-app#301 CI failing, review required", "billing#57 conflict, changes requested",
		"admin#33 review required", "worker#12 review required"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("attention =\n%v\nwant\n%v", got, want)
	}
}

// A merge-state failure used to count as the open-PR search failing, which
// replaced both PR cards with the error. Only conflicts depend on it.
func TestMergeStateFailureKeepsTheCards(t *testing.T) {
	m := trunkModel(ScreenMainMenu)
	m.home = homeState{data: dryRunTrunkHomeData(), loaded: true, fetchedAt: timeNow()}
	m.home.data.Problems = []string{"merge states: HTTP 504"}
	card := stripANSI(m.trunkPRsCard(56, 10))
	if !strings.Contains(card, "conflicts unchecked") || !strings.Contains(card, "drafts") {
		t.Errorf("card = %q, want the counts and a note that conflicts are unchecked", card)
	}
}
