package github

import (
	"encoding/json"
	"testing"
)

// The rollup mixes check runs and commit statuses. The query used to ask only
// for check runs, so a commit status arrived as {} and the CI column counted it
// as a pass: a failing Vercel or CircleCI status showed green.
func TestStatusRollupReadsCommitStatuses(t *testing.T) {
	const payload = `{
		"number": 7,
		"statusCheckRollup": {"nodes": [{"commit": {"statusCheckRollup": {"contexts": {"nodes": [
			{"__typename": "CheckRun", "name": "build", "status": "COMPLETED", "conclusion": "SUCCESS",
			 "workflowName": {"workflowRun": {"workflow": {"name": "CI"}}}},
			{"__typename": "StatusContext", "context": "ci/circleci", "state": "FAILURE"},
			{"__typename": "StatusContext", "context": "vercel", "state": "PENDING"},
			{"__typename": "SomethingNew"},
			{}
		]}}}}]}
	}`
	var node searchPRNode
	if err := json.Unmarshal([]byte(payload), &node); err != nil {
		t.Fatal(err)
	}
	checks := node.toGhPr().StatusCheckRollup

	if len(checks) != 3 {
		t.Fatalf("got %d checks, want 3: unknown and empty nodes must be skipped, not counted as passes: %+v", len(checks), checks)
	}
	if c := checks[0]; c.Name != "build" || c.WorkflowName != "CI" || c.Conclusion != "SUCCESS" {
		t.Errorf("check run = %+v", c)
	}
	if c := checks[1]; c.Name != "ci/circleci" || c.Status != "COMPLETED" || c.Conclusion != "FAILURE" {
		t.Errorf("failing commit status = %+v, want a completed FAILURE", c)
	}
	if c := checks[2]; c.Name != "vercel" || c.Status != "PENDING" || c.Conclusion != "" {
		t.Errorf("pending commit status = %+v, want an unfinished check", c)
	}
}
