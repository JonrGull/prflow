package app

import (
	"fmt"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/JonrGull/prflow/internal/github"
	"github.com/JonrGull/prflow/internal/models"
)

// Each release is compared with the one before it in its own repo. The extra
// release fetched per repo is only there for the oldest listed to have one.
func TestShippedEntriesPairEachReleaseWithTheOneBefore(t *testing.T) {
	web := repoNWO{Repo: models.NewRepoInfo("/r/web", "F/web", "main", "F"), NWO: "acme/web"}
	api := repoNWO{Repo: models.NewRepoInfo("/r/api", "B/api", "main", "B"), NWO: "acme/api"}
	var webRels []github.Release
	for i := 0; i <= shippedPerRepo; i++ { // one more than is listed
		webRels = append(webRels, github.Release{Tag: fmt.Sprintf("w%d", i), PublishedAt: fixedNow.Add(-time.Duration(2*i+1) * time.Hour)})
	}
	byRepo := map[string][]github.Release{
		web.NWO: webRels,
		api.NWO: {{Tag: "a0", PublishedAt: fixedNow.Add(-2 * time.Hour)}},
	}
	got := shippedEntries([]repoNWO{web, api}, byRepo)
	if len(got) != shippedPerRepo+1 {
		t.Fatalf("%d entries, want %d of web's and api's one", len(got), shippedPerRepo+1)
	}
	if got[0].Release.Tag != "w0" || got[1].Release.Tag != "a0" || got[2].Release.Tag != "w1" {
		t.Errorf("order = %s %s %s, want newest first across repos: w0 a0 w1", got[0].Release.Tag, got[1].Release.Tag, got[2].Release.Tag)
	}
	if got[1].Prev != nil {
		t.Errorf("api's only release compares with %v, want nothing", got[1].Prev)
	}
	last := got[len(got)-1]
	if last.Release.Tag != fmt.Sprintf("w%d", shippedPerRepo-1) || last.Prev == nil || last.Prev.Tag != fmt.Sprintf("w%d", shippedPerRepo) {
		t.Errorf("oldest listed = %s since %v, want it compared with the extra release", last.Release.Tag, last.Prev)
	}
}

// A PR's several commits are one row, newest PR first; a release PR between
// the chain's branches is left out, since its commits come with their own PRs;
// tickets come from headlines and titles alike.
func TestSummarizeGroupsCommitsByPR(t *testing.T) {
	pr := func(n uint64, title, head string) *github.ShippedPR {
		return &github.ShippedPR{Number: n, Title: title, HeadBranch: head}
	}
	feature := pr(10, "Add the roster", "feature/roster")
	commits := []github.ShippedCommit{ // oldest first, as GitHub lists them
		{Headline: "wip att-7 roster", PR: feature},
		{Headline: "Add the roster (ATT-12)", PR: feature},
		{Headline: "chore: changelog", PR: nil},
		{Headline: "Merge dev into staging", PR: pr(11, "dev → staging", "dev")},
		{Headline: "Fix it", PR: pr(12, "PROJ-3 Fix it", "fix/it")},
	}
	s := summarize(commits, map[string]bool{"dev": true}, regexp.MustCompile(`(?i)[A-Z]+-[0-9]+`))
	var nums []uint64
	for _, p := range s.prs {
		nums = append(nums, p.Number)
	}
	if want := []uint64{12, 10}; !reflect.DeepEqual(nums, want) {
		t.Errorf("PRs = %v, want %v", nums, want)
	}
	if len(s.loose) != 1 || s.loose[0].Headline != "chore: changelog" {
		t.Errorf("commits without a PR = %+v", s.loose)
	}
	if want := []string{"ATT-12", "ATT-7", "PROJ-3"}; !reflect.DeepEqual(s.tickets, want) {
		t.Errorf("tickets = %v, want %v", s.tickets, want)
	}
}

func TestShippedMarkdown(t *testing.T) {
	e := shippedEntry{Repo: models.NewRepoInfo("/r/web", "F/web", "main", "F"),
		Release: github.Release{Tag: "v2"}, Prev: &github.Release{Tag: "v1"}}
	s := shippedSummary{
		prs:     []github.ShippedPR{{Number: 7, Title: "Add it", URL: "https://github.com/acme/web/pull/7"}},
		loose:   []github.ShippedCommit{{Headline: "Bump"}},
		tickets: []string{"ATT-1"},
	}
	want := "**web v2** (since v1)\n- [#7](https://github.com/acme/web/pull/7) Add it\n- Bump\n\nTickets: ATT-1"
	if got := shippedMarkdown(e, s); got != want {
		t.Errorf("markdown =\n%s\nwant\n%s", got, want)
	}
}

// A refresh puts the cursor back on its release, and a comparison is asked
// for once: they never change.
func TestShippedCursorAndComparisons(t *testing.T) {
	m := sized(staleModel(ScreenShipped))
	m = send(t, m, shippedFetchedResult{entries: dryRunShippedEntries()})
	m.shipped.index = 3
	want := m.shipped.entries[3].key()

	fresh := dryRunShippedEntries()
	newer := fresh[0]
	newer.Release.Tag, newer.Release.PublishedAt = "v1.9.2", fixedNow
	m = send(t, m, shippedFetchedResult{entries: append([]shippedEntry{newer}, fresh...)})
	if e, _ := m.highlightedRelease(); e.key() != want {
		t.Errorf("after a refresh the cursor is on %s, want %s", e.key(), want)
	}

	e, _ := m.highlightedRelease()
	if !m.shipped.diffs[e.key()].loading {
		t.Fatal("the highlighted release's comparison was not asked for on load")
	}
	if cmd := m.fetchReleaseDiff(e); cmd != nil {
		t.Error("the comparison was asked for twice")
	}
	m = send(t, m, shippedDiffResult{key: e.key(), diff: github.ReleaseDiff{Status: "AHEAD"}})
	if cmd := m.previewRelease(); cmd != nil {
		t.Error("a loaded comparison is previewed again")
	}
}
