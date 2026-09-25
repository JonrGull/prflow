package github

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/JonrGull/prflow/internal/run"
)

// MergeCheck is what GitHub says about merging one PR now.
type MergeCheck struct {
	Status  string // mergeStateStatus: CLEAN or HAS_HOOKS when it would merge now
	HeadSHA string // the head commit GitHub has
	Method  string // viewerDefaultMergeMethod: MERGE, SQUASH or REBASE
}

// CheckMerge asks for a PR's merge state and the method the web UI would
// offer this user, which is always one the repo allows.
func CheckMerge(nwo string, number uint64) (MergeCheck, error) {
	owner, name, _ := strings.Cut(nwo, "/")
	query := `query($o: String!, $n: String!, $p: Int!) { repository(owner: $o, name: $n) {
		viewerDefaultMergeMethod
		pullRequest(number: $p) { mergeStateStatus headRefOid }
	} }`
	out, err := run.Output(run.Network, "", "gh", "api", "graphql",
		"-f", "o="+owner, "-f", "n="+name, "-F", fmt.Sprintf("p=%d", number), "-f", "query="+query)
	if len(out) == 0 && err != nil {
		return MergeCheck{}, fmt.Errorf("gh api graphql failed: %w", err)
	}
	var resp struct {
		Data struct {
			Repository *struct {
				ViewerDefaultMergeMethod string `json:"viewerDefaultMergeMethod"`
				PullRequest              *struct {
					MergeStateStatus string `json:"mergeStateStatus"`
					HeadRefOid       string `json:"headRefOid"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return MergeCheck{}, fmt.Errorf("parsing merge check: %w", err)
	}
	repo := resp.Data.Repository
	if repo == nil || repo.PullRequest == nil {
		if len(resp.Errors) > 0 {
			return MergeCheck{}, fmt.Errorf("graphql: %s", resp.Errors[0].Message)
		}
		return MergeCheck{}, fmt.Errorf("%s#%d not found", nwo, number)
	}
	return MergeCheck{
		Status:  repo.PullRequest.MergeStateStatus,
		HeadSHA: repo.PullRequest.HeadRefOid,
		Method:  repo.ViewerDefaultMergeMethod,
	}, nil
}

var mergeFlags = map[string]string{"MERGE": "--merge", "SQUASH": "--squash", "REBASE": "--rebase"}

// MergePRBy merges a PR by the given method, only if its head is still
// headSHA. Nothing local is touched: no branch is deleted or switched.
func MergePRBy(nwo string, number uint64, headSHA, method string) error {
	flag, ok := mergeFlags[method]
	if !ok {
		return fmt.Errorf("unknown merge method %q", method)
	}
	output, err := run.Combined(run.Network, "", "gh", "pr", "merge", strconv.FormatUint(number, 10),
		"-R", nwo, flag, "--match-head-commit", headSHA)
	if err != nil {
		return mergeFailure(output)
	}
	return nil
}

// FailedRun is a workflow run whose failed jobs can be re-run.
type FailedRun struct {
	ID   uint64
	Name string
}

// rerunnable are the conclusions "re-run failed jobs" applies to. A cancelled
// run was usually superseded, and action_required waits for approval.
var rerunnable = map[string]bool{"failure": true, "timed_out": true, "startup_failure": true}

// FailedRunsFor lists the Actions runs on a commit that failed, taking only the
// newest run of each workflow and event: an older failure a later run has
// replaced is not what the PR shows.
func FailedRunsFor(nwo, sha string) ([]FailedRun, error) {
	output, err := run.Combined(run.Network, "", "gh", "api",
		fmt.Sprintf("repos/%s/actions/runs?head_sha=%s&per_page=100", nwo, sha))
	if err != nil {
		return nil, fmt.Errorf("gh api actions/runs failed: %s", strings.TrimSpace(string(output)))
	}
	return newestFailures(output)
}

func newestFailures(output []byte) ([]FailedRun, error) {
	var resp struct {
		WorkflowRuns []struct {
			ID         uint64    `json:"id"`
			Name       string    `json:"name"`
			WorkflowID uint64    `json:"workflow_id"`
			Event      string    `json:"event"`
			Status     string    `json:"status"`
			Conclusion *string   `json:"conclusion"`
			CreatedAt  time.Time `json:"created_at"`
		} `json:"workflow_runs"`
	}
	if err := json.Unmarshal(output, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse actions/runs: %w", err)
	}
	newest := map[string]bool{}
	var failed []FailedRun
	for _, r := range resp.WorkflowRuns { // newest first
		key := fmt.Sprintf("%d/%s", r.WorkflowID, r.Event)
		if newest[key] {
			continue
		}
		newest[key] = true
		if r.Status == "completed" && r.Conclusion != nil && rerunnable[*r.Conclusion] {
			failed = append(failed, FailedRun{ID: r.ID, Name: r.Name})
		}
	}
	return failed, nil
}

// RerunFailedJobs re-runs a run's failed jobs and the jobs that depend on them.
func RerunFailedJobs(nwo string, runID uint64) error {
	output, err := run.Combined(run.Network, "", "gh", "api", "-X", "POST",
		fmt.Sprintf("repos/%s/actions/runs/%d/rerun-failed-jobs", nwo, runID))
	if err != nil {
		return fmt.Errorf("re-running run %d failed: %s", runID, strings.TrimSpace(string(output)))
	}
	return nil
}

// CheckoutPR checks a PR's branch out in dir, which must be a git checkout of
// its repo. gh handles forks and sets the branch to track the PR's head.
func CheckoutPR(dir, nwo string, number uint64) error {
	output, err := run.Combined(run.Slow, dir, "gh", "pr", "checkout", strconv.FormatUint(number, 10), "-R", nwo)
	if err != nil {
		return fmt.Errorf("gh pr checkout failed: %s", strings.TrimSpace(string(output)))
	}
	return nil
}
