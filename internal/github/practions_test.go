package github

import (
	"reflect"
	"testing"
)

// Runs come newest first. Only the newest run of a workflow for an event says
// what the PR shows: CI failed and then passed on a rerun push is not a failure.
func TestNewestFailuresSkipsReplacedRuns(t *testing.T) {
	body := []byte(`{"workflow_runs": [
		{"id": 5, "name": "CI", "workflow_id": 1, "event": "pull_request", "status": "completed", "conclusion": "success"},
		{"id": 4, "name": "CI", "workflow_id": 1, "event": "pull_request", "status": "completed", "conclusion": "failure"},
		{"id": 3, "name": "CI", "workflow_id": 1, "event": "push", "status": "completed", "conclusion": "timed_out"},
		{"id": 2, "name": "E2E", "workflow_id": 2, "event": "pull_request", "status": "completed", "conclusion": "cancelled"},
		{"id": 1, "name": "Deploy", "workflow_id": 3, "event": "pull_request", "status": "in_progress", "conclusion": null},
		{"id": 0, "name": "Lint", "workflow_id": 4, "event": "pull_request", "status": "completed", "conclusion": "failure"}
	]}`)
	got, err := newestFailures(body)
	if err != nil {
		t.Fatal(err)
	}
	want := []FailedRun{{ID: 3, Name: "CI"}, {ID: 0, Name: "Lint"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("failures = %v, want %v", got, want)
	}
}
