package github

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JonrGull/prflow/internal/run"
)

// BranchPair is one release step in one repo: how far Head is ahead of Base.
type BranchPair struct {
	NWO  string // owner/repo
	Base string
	Head string
}

// CompareBranches reports how many commits each pair's head is ahead of its
// base, for every pair in one GraphQL request. A pair whose branches do not
// both exist is left out rather than failing the rest. The dashboard used it to
// avoid fetching every repo, which a local count would need to be current.
func CompareBranches(pairs []BranchPair) (map[BranchPair]int, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	query, args := compareQuery(pairs)
	// gh exits non-zero when any part of a GraphQL response has an error, such
	// as a branch missing in one repo, but still prints the data for the rest.
	out, err := run.Output(run.Network, "", "gh", append([]string{"api", "graphql", "-f", "query=" + query}, args...)...)
	if len(out) == 0 && err != nil {
		return nil, fmt.Errorf("gh api graphql failed: %w", err)
	}
	return parseCompare(out, pairs)
}

// compareQuery builds the aliased query and its variables.
func compareQuery(pairs []BranchPair) (string, []string) {
	var params, fields, args []string
	for i, p := range pairs {
		owner, name, _ := strings.Cut(p.NWO, "/")
		params = append(params, fmt.Sprintf("$o%d: String!, $n%d: String!, $b%d: String!, $h%d: String!", i, i, i, i))
		fields = append(fields, fmt.Sprintf(
			"c%d: repository(owner: $o%d, name: $n%d) { ref(qualifiedName: $b%d) { compare(headRef: $h%d) { aheadCount } } }",
			i, i, i, i, i))
		args = append(args,
			"-f", fmt.Sprintf("o%d=%s", i, owner),
			"-f", fmt.Sprintf("n%d=%s", i, name),
			"-f", fmt.Sprintf("b%d=refs/heads/%s", i, p.Base),
			"-f", fmt.Sprintf("h%d=%s", i, p.Head))
	}
	return "query(" + strings.Join(params, ", ") + ") { " + strings.Join(fields, " ") + " }", args
}

func parseCompare(out []byte, pairs []BranchPair) (map[BranchPair]int, error) {
	var resp struct {
		Data map[string]*struct {
			Ref *struct {
				Compare *struct {
					AheadCount int `json:"aheadCount"`
				} `json:"compare"`
			} `json:"ref"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("parsing branch comparison: %w", err)
	}
	if resp.Data == nil && len(resp.Errors) > 0 {
		return nil, fmt.Errorf("graphql: %s", resp.Errors[0].Message)
	}
	ahead := make(map[BranchPair]int)
	for i, p := range pairs {
		repo := resp.Data[fmt.Sprintf("c%d", i)]
		if repo == nil || repo.Ref == nil || repo.Ref.Compare == nil {
			continue // a branch is missing in this repo
		}
		ahead[p] = repo.Ref.Compare.AheadCount
	}
	return ahead, nil
}
