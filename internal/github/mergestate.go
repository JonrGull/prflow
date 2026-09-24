package github

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

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

// mergeStateChunk is how many PRs one merge-state request asks about; the
// requests run at once. One request for 84 PRs took 9s, then timed out.
const mergeStateChunk = 10

// MergeStates asks for each PR's merge state, which is too slow to be in the
// search. A group that fails leaves its PRs out; the rest are still returned.
func MergeStates(refs []PRRef) (map[PRRef]MergeState, error) {
	var chunks [][]PRRef
	for i := 0; i < len(refs); i += mergeStateChunk {
		chunks = append(chunks, refs[i:min(i+mergeStateChunk, len(refs))])
	}
	found := make([]map[PRRef]MergeState, len(chunks))
	errs := make([]error, len(chunks))
	var wg sync.WaitGroup
	for i, chunk := range chunks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			found[i], errs[i] = mergeStates(chunk)
		}()
	}
	wg.Wait()

	states := make(map[PRRef]MergeState)
	var firstErr error
	for i := range chunks {
		if errs[i] != nil && firstErr == nil {
			firstErr = errs[i]
		}
		for ref, st := range found[i] {
			states[ref] = st
		}
	}
	return states, firstErr
}

func mergeStates(refs []PRRef) (map[PRRef]MergeState, error) {
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
