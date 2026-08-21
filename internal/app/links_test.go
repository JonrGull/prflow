package app

import (
	"reflect"
	"testing"

	"github.com/JonrGull/prflow/internal/models"
)

// mergedPRLinks replaced a nested loop that scanned every merge PR for every
// result — run once for the "open all" action and again, identically, for
// "copy all". This checks the map-indexed version agrees with the original.
func TestMergedPRLinks(t *testing.T) {
	pr := func(name string, n uint64, url string) models.MergePrEntry {
		return models.MergePrEntry{
			Repo:     models.RepoInfo{DisplayName: name},
			PrNumber: n,
			URL:      url,
		}
	}

	m := Model{merge: mergeState{
		prs: []models.MergePrEntry{
			pr("Frontend/web", 1, "u1"),
			pr("Backend/api", 2, "u2"),
			pr("Backend/api", 3, "u3"), // same repo, different PR
		},
		results: []models.MergeResult{
			{RepoName: "Frontend/web", PrNumber: 1, Success: true},
			{RepoName: "Backend/api", PrNumber: 2, Success: false}, // failed, excluded
			{RepoName: "Backend/api", PrNumber: 3, Success: true},
			{RepoName: "Ghost/repo", PrNumber: 9, Success: true}, // no matching PR
		},
	}}

	// The original implementation, kept here as the oracle.
	var want []namedLink
	for _, result := range m.merge.results {
		if result.Success {
			for _, p := range m.merge.prs {
				if p.Repo.DisplayName == result.RepoName && p.PrNumber == result.PrNumber {
					want = append(want, namedLink{p.Repo.DisplayName, p.URL})
					break
				}
			}
		}
	}

	got := m.mergedPRLinks()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mergedPRLinks()\n got %+v\nwant %+v", got, want)
	}

	if md := markdownList(got); md != "- Frontend/web: u1\n- Backend/api: u3" {
		t.Errorf("markdownList = %q", md)
	}
	if u := urls(got); !reflect.DeepEqual(u, []string{"u1", "u3"}) {
		t.Errorf("urls = %v", u)
	}
}

func TestLinkBuildersEmpty(t *testing.T) {
	var m Model
	// The summary screens call these before any work has run; none may panic.
	if got := m.mergedPRLinks(); len(got) != 0 {
		t.Errorf("mergedPRLinks on empty model = %v", got)
	}
	if got := m.batchPRLinks(); len(got) != 0 {
		t.Errorf("batchPRLinks on empty model = %v", got)
	}
	if got := m.openPRLinks(); len(got) != 0 {
		t.Errorf("openPRLinks on empty model = %v", got)
	}
	if got := markdownList(nil); got != "" {
		t.Errorf("markdownList(nil) = %q", got)
	}
}

// batchPRLinks skips repos whose PR was never created (PrURL stays nil).
func TestBatchPRLinksSkipsMissingURLs(t *testing.T) {
	url := "https://example.test/pull/1"
	m := Model{batch: batchState{results: []models.BatchResult{
		{Repo: models.RepoInfo{DisplayName: "a"}, PrURL: &url},
		{Repo: models.RepoInfo{DisplayName: "b"}, PrURL: nil},
	}}}
	got := m.batchPRLinks()
	if len(got) != 1 || got[0].Name != "a" || got[0].URL != url {
		t.Errorf("batchPRLinks = %+v", got)
	}
}
