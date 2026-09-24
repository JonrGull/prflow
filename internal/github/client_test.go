package github

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// Updating a PR used to send the generated ticket list as the whole body:
// re-running a release wiped what people had written on the PR, and a run with
// no tickets blanked it.
func TestMergeBodyKeepsWhatPeopleWrote(t *testing.T) {
	section := GeneratePRBody([]string{"PROJ-2", "PROJ-3"}, "acme")
	old := GeneratePRBody([]string{"PROJ-1"}, "acme")
	notes := "## QA notes\n\n- [x] checked login\n- [ ] checked billing"

	tests := []struct {
		name, existing, section, want string
	}{
		{"new PR", "", section, section},
		{"a body prflow never wrote to keeps it, tickets after", notes, section, notes + "\n\n" + section},
		{"the section is replaced where it stands", "Intro\n\n" + old + "\n\n" + notes, section, "Intro\n\n" + section + "\n\n" + notes},
		{"no tickets leaves the body alone", notes, "", notes},
		{"no tickets removes only the old section", old + "\n\n" + notes, "", notes},
		{"a pre-marker body is upgraded in place", "# Tickets\n\n### - Closes [PROJ-1](https://linear.app/acme/issue/proj-1)\n\n" + notes, section, section + "\n\n" + notes},
		{"github.com edits come back with CRLF", strings.ReplaceAll("# Tickets\n\n### - Closes PROJ-1\n\n"+notes, "\n", "\r\n"), section, section + "\n\n" + notes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergeBody(tt.existing, tt.section); got != tt.want {
				t.Errorf("got:\n%s\n\nwant:\n%s", got, tt.want)
			}
		})
	}
}

// With no Linear org the tickets are listed plainly rather than linked to
// linear.app//issue/..., which is what the config documents.
func TestGeneratePRBodyWithoutLinearOrg(t *testing.T) {
	body := GeneratePRBody([]string{"PROJ-1"}, "")
	if strings.Contains(body, "linear.app") || !strings.Contains(body, "### - Closes PROJ-1") {
		t.Errorf("body = %q", body)
	}
	if GeneratePRBody(nil, "acme") != "" {
		t.Error("no tickets should generate no section")
	}
}

// fakeGh puts a gh on PATH that records its arguments and prints out, exiting
// with code.
func fakeGh(t *testing.T, out string, code int) (args func() string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := fmt.Sprintf("#!/bin/sh\necho \"$@\" > %q\nprintf '%%s' %q\nexit %d\n", argsFile, out, code)
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() string {
		b, _ := os.ReadFile(argsFile)
		return strings.TrimSpace(string(b))
	}
}

// Merging by number alone merged whatever the branch held at that moment,
// including commits pushed after the list was loaded and reviewed.
func TestMergePinsTheHeadCommit(t *testing.T) {
	args := fakeGh(t, "", 0)
	if err := MergePR(t.TempDir(), 42, "abc123"); err != nil {
		t.Fatal(err)
	}
	if got := args(); !strings.Contains(got, "--match-head-commit abc123") {
		t.Errorf("gh %s: want --match-head-commit abc123", got)
	}
}

func TestMergeExplainsAMovedHead(t *testing.T) {
	fakeGh(t, "GraphQL: Head branch was modified. Review and try the merge again. (mergePullRequest)", 1)
	err := MergePR(t.TempDir(), 42, "abc123")
	if err == nil || !strings.Contains(err.Error(), "new commits were pushed") {
		t.Errorf("err = %v, want the moved-head explanation", err)
	}
}

// With no head recorded there is nothing to pin, so it refuses rather than
// merging unguarded.
func TestMergeRefusesWithoutAHeadCommit(t *testing.T) {
	args := fakeGh(t, "", 0)
	if err := MergePR(t.TempDir(), 42, ""); err == nil {
		t.Error("merged with no head commit to match")
	}
	if args() != "" {
		t.Errorf("gh was called: %s", args())
	}
}
