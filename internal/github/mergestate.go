package github

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JonrGull/prflow/internal/run"
)

// PRRef names one pull request.
type PRRef struct {
	NWO    string // owner/repo
	Number uint64
}

// MergeState is GitHub's verdict on whether a PR can merge.
type MergeState struct {
	Mergeable string // MERGEABLE, CONFLICTING or UNKNOWN
	Status    string // mergeStateStatus: CLEAN when GitHub would merge it now
}

// MergeStates asks for each PR's merge state in one GraphQL request. It is not
// in SearchAllOpenPRs: working it out for a whole page made GitHub time out.
func MergeStates(refs []PRRef) (map[PRRef]MergeState, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	var params, fields []string
	args := []string{"api", "graphql"}
	for i, r := range refs {
		owner, name, _ := strings.Cut(r.NWO, "/")
		params = append(params, fmt.Sprintf("$o%d: String!, $n%d: String!, $p%d: Int!", i, i, i))
		fields = append(fields, fmt.Sprintf(
			"m%d: repository(owner: $o%d, name: $n%d) { pullRequest(number: $p%d) { mergeable mergeStateStatus } }", i, i, i, i))
		args = append(args, "-f", fmt.Sprintf("o%d=%s", i, owner), "-f", fmt.Sprintf("n%d=%s", i, name),
			"-F", fmt.Sprintf("p%d=%d", i, r.Number))
	}
	query := "query(" + strings.Join(params, ", ") + ") { " + strings.Join(fields, " ") + " }"
	// As in CompareBranches, gh exits non-zero on a partial error but prints the rest.
	out, err := run.Output(run.Network, "", "gh", append(args, "-f", "query="+query)...)
	if len(out) == 0 && err != nil {
		return nil, fmt.Errorf("gh api graphql failed: %w", err)
	}

	var resp struct {
		Data map[string]*struct {
			PullRequest *struct {
				Mergeable        string `json:"mergeable"`
				MergeStateStatus string `json:"mergeStateStatus"`
			} `json:"pullRequest"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("parsing merge states: %w", err)
	}
	if resp.Data == nil && len(resp.Errors) > 0 {
		return nil, fmt.Errorf("graphql: %s", resp.Errors[0].Message)
	}
	states := make(map[PRRef]MergeState)
	for i, r := range refs {
		repo := resp.Data[fmt.Sprintf("m%d", i)]
		if repo == nil || repo.PullRequest == nil {
			continue
		}
		states[r] = MergeState{Mergeable: repo.PullRequest.Mergeable, Status: repo.PullRequest.MergeStateStatus}
	}
	return states, nil
}
