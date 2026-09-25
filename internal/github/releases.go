package github

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/JonrGull/prflow/internal/run"
)

// Release is one published GitHub release.
type Release struct {
	Tag         string
	SHA         string // the commit the tag points at
	PublishedAt time.Time
	URL         string
	Prerelease  bool
}

// releaseRepoChunk is how many repos one releases request covers; the
// requests run at once, as the open-PR search does.
const releaseRepoChunk = 10

// Releases lists each repo's newest published releases, up to perRepo, newest
// first. A group of repos that fails is left out and its error returned with
// the rest.
func Releases(nwos []string, perRepo int) (map[string][]Release, error) {
	var chunks [][]string
	for i := 0; i < len(nwos); i += releaseRepoChunk {
		chunks = append(chunks, nwos[i:min(i+releaseRepoChunk, len(nwos))])
	}
	found := make([]map[string][]Release, len(chunks))
	errs := make([]error, len(chunks))
	var wg sync.WaitGroup
	for i, chunk := range chunks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			found[i], errs[i] = releases(chunk, perRepo)
		}()
	}
	wg.Wait()

	out := map[string][]Release{}
	var firstErr error
	for i := range chunks {
		if errs[i] != nil && firstErr == nil {
			firstErr = errs[i]
		}
		for nwo, rs := range found[i] {
			out[nwo] = rs
		}
	}
	return out, firstErr
}

func releases(nwos []string, perRepo int) (map[string][]Release, error) {
	var params, fields []string
	args := []string{"api", "graphql", "-F", fmt.Sprintf("k=%d", perRepo)}
	for i, nwo := range nwos {
		owner, name, _ := strings.Cut(nwo, "/")
		params = append(params, fmt.Sprintf("$o%d: String!, $n%d: String!", i, i))
		fields = append(fields, fmt.Sprintf(`r%d: repository(owner: $o%d, name: $n%d) {
			releases(first: $k, orderBy: {field: CREATED_AT, direction: DESC}) {
				nodes { tagName publishedAt isDraft isPrerelease url tagCommit { oid } }
			} }`, i, i, i))
		args = append(args, "-f", fmt.Sprintf("o%d=%s", i, owner), "-f", fmt.Sprintf("n%d=%s", i, name))
	}
	query := "query($k: Int!, " + strings.Join(params, ", ") + ") { " + strings.Join(fields, " ") + " }"
	// As in CompareBranches, gh exits non-zero on a partial error but prints the rest.
	out, err := run.Output(run.Network, "", "gh", append(args, "-f", "query="+query)...)
	if len(out) == 0 && err != nil {
		return nil, fmt.Errorf("gh api graphql failed: %w", err)
	}
	return parseReleases(out, nwos)
}

func parseReleases(out []byte, nwos []string) (map[string][]Release, error) {
	var resp struct {
		Data map[string]*struct {
			Releases struct {
				Nodes []struct {
					TagName      string    `json:"tagName"`
					PublishedAt  time.Time `json:"publishedAt"`
					IsDraft      bool      `json:"isDraft"`
					IsPrerelease bool      `json:"isPrerelease"`
					URL          string    `json:"url"`
					TagCommit    *struct {
						Oid string `json:"oid"`
					} `json:"tagCommit"`
				} `json:"nodes"`
			} `json:"releases"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("parsing releases: %w", err)
	}
	if resp.Data == nil && len(resp.Errors) > 0 {
		return nil, fmt.Errorf("graphql: %s", resp.Errors[0].Message)
	}
	found := map[string][]Release{}
	for i, nwo := range nwos {
		repo := resp.Data[fmt.Sprintf("r%d", i)]
		if repo == nil {
			continue
		}
		for _, n := range repo.Releases.Nodes {
			// A draft is not published, and a release whose tag is gone has
			// nothing to compare.
			if n.IsDraft || n.TagCommit == nil {
				continue
			}
			found[nwo] = append(found[nwo], Release{Tag: n.TagName, SHA: n.TagCommit.Oid,
				PublishedAt: n.PublishedAt, URL: n.URL, Prerelease: n.IsPrerelease})
		}
	}
	return found, nil
}

// ShippedCommit is one commit in the difference between two releases, with the
// PR that brought it in if there was one.
type ShippedCommit struct {
	SHA      string
	Headline string
	PR       *ShippedPR
}

// ShippedPR is a PR as the release comparison shows it.
type ShippedPR struct {
	Number     uint64
	Title      string
	URL        string
	Author     string
	HeadBranch string
}

// ReleaseDiff is what changed from one release to the next. A rollback's new
// release is behind the old one, so its commits are Removed, not Shipped.
type ReleaseDiff struct {
	Status       string          // AHEAD, BEHIND, DIVERGED or IDENTICAL
	Shipped      []ShippedCommit // in the new release and not the old, oldest first
	Removed      []ShippedCommit // in the old release and not the new
	ShippedTotal int             // Shipped holds at most compareCommits of them
	RemovedTotal int
}

// compareCommits is how many commits each side of a comparison lists: the
// newest ones, since a long gap's oldest commits matter least.
const compareCommits = 100

// CompareReleases asks for both directions between two tags in one request.
func CompareReleases(nwo, prevTag, tag string) (ReleaseDiff, error) {
	owner, name, _ := strings.Cut(nwo, "/")
	commits := fmt.Sprintf(`commits(last: %d) { nodes { oid messageHeadline
		associatedPullRequests(first: 5) { nodes { number title url headRefName author { login } mergeCommit { oid } } } } }`, compareCommits)
	query := `query($o: String!, $n: String!, $prev: String!, $this: String!) { repository(owner: $o, name: $n) {
		shipped: ref(qualifiedName: $prev) { compare(headRef: $this) { status aheadBy behindBy ` + commits + ` } }
		removed: ref(qualifiedName: $this) { compare(headRef: $prev) { ` + commits + ` } }
	} }`
	out, err := run.Output(run.Network, "", "gh", "api", "graphql",
		"-f", "o="+owner, "-f", "n="+name,
		"-f", "prev=refs/tags/"+prevTag, "-f", "this=refs/tags/"+tag, "-f", "query="+query)
	if len(out) == 0 && err != nil {
		return ReleaseDiff{}, fmt.Errorf("gh api graphql failed: %w", err)
	}
	return parseReleaseDiff(out)
}

type compareCommitNode struct {
	Oid                    string `json:"oid"`
	MessageHeadline        string `json:"messageHeadline"`
	AssociatedPullRequests struct {
		Nodes []struct {
			Number      uint64 `json:"number"`
			Title       string `json:"title"`
			URL         string `json:"url"`
			HeadRefName string `json:"headRefName"`
			Author      struct {
				Login string `json:"login"`
			} `json:"author"`
			MergeCommit *struct {
				Oid string `json:"oid"`
			} `json:"mergeCommit"`
		} `json:"nodes"`
	} `json:"associatedPullRequests"`
}

// toShipped keeps the PR whose merge made this commit: a commit is also
// associated with every PR that later carried it, like a release PR.
func (n compareCommitNode) toShipped() ShippedCommit {
	c := ShippedCommit{SHA: n.Oid, Headline: n.MessageHeadline}
	prs := n.AssociatedPullRequests.Nodes
	for _, p := range prs {
		if p.MergeCommit != nil && p.MergeCommit.Oid == n.Oid {
			c.PR = &ShippedPR{Number: p.Number, Title: p.Title, URL: p.URL, Author: p.Author.Login, HeadBranch: p.HeadRefName}
			return c
		}
	}
	if len(prs) > 0 {
		p := prs[0]
		c.PR = &ShippedPR{Number: p.Number, Title: p.Title, URL: p.URL, Author: p.Author.Login, HeadBranch: p.HeadRefName}
	}
	return c
}

func parseReleaseDiff(out []byte) (ReleaseDiff, error) {
	type side struct {
		Compare *struct {
			Status   string `json:"status"`
			AheadBy  int    `json:"aheadBy"`
			BehindBy int    `json:"behindBy"`
			Commits  struct {
				Nodes []compareCommitNode `json:"nodes"`
			} `json:"commits"`
		} `json:"compare"`
	}
	var resp struct {
		Data struct {
			Repository *struct {
				Shipped *side `json:"shipped"`
				Removed *side `json:"removed"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return ReleaseDiff{}, fmt.Errorf("parsing release comparison: %w", err)
	}
	repo := resp.Data.Repository
	if repo == nil || repo.Shipped == nil || repo.Shipped.Compare == nil {
		if len(resp.Errors) > 0 {
			return ReleaseDiff{}, fmt.Errorf("graphql: %s", resp.Errors[0].Message)
		}
		return ReleaseDiff{}, fmt.Errorf("a tag is missing")
	}
	cmp := repo.Shipped.Compare
	d := ReleaseDiff{Status: cmp.Status, ShippedTotal: cmp.AheadBy, RemovedTotal: cmp.BehindBy}
	for _, n := range cmp.Commits.Nodes {
		d.Shipped = append(d.Shipped, n.toShipped())
	}
	if cmp.BehindBy > 0 && repo.Removed != nil && repo.Removed.Compare != nil {
		for _, n := range repo.Removed.Compare.Commits.Nodes {
			d.Removed = append(d.Removed, n.toShipped())
		}
	}
	return d, nil
}
