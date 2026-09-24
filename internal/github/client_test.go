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
	script := fmt.Sprintf("#!/bin/sh\necho \"$@\" > %q\nprintf '%%b' %q\nexit %d\n", argsFile, out, code)
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

// fakeGhAuth puts a gh on PATH whose "auth token" and "auth status" print the
// given output and exit with the given code.
func fakeGhAuth(t *testing.T, tokenOut string, tokenCode int, statusOut string, statusCode int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
case "$1 $2" in
  "auth token") printf '%%b' %q; exit %d ;;
  "auth status") printf '%%b' %q; exit %d ;;
esac
exit 2
`, tokenOut, tokenCode, statusOut, statusCode)
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// gh auth status fails when any stored account is bad or the network is down,
// and its exit code used to lock every screen behind "not authenticated".
func TestCheckAuth(t *testing.T) {
	const ok = "github.com\n  ✓ Logged in to github.com account octo (keyring)\n"
	cases := []struct {
		name       string
		tokenOut   string
		tokenCode  int
		statusOut  string
		statusCode int
		wantUser   string
		wantErr    bool
	}{
		{"logged in", "gho_x", 0, ok, 0, "octo", false},
		{"another account expired", "gho_x", 0, ok + "  X Failed to log in to github.com account old (keyring)\n", 1, "octo", false},
		{"offline", "gho_x", 0, "  X Timeout trying to log in to github.com account octo (keyring)\n", 1, "octo", false},
		{"offline, no output", "gho_x", 0, "", 1, "", false},
		{"not logged in", "no oauth token found for github.com", 1, "", 1, "", true},
		{"old gh, logged in", `unknown command "token" for "gh auth"`, 1, ok, 0, "octo", false},
		{"old gh, not logged in", `unknown command "token" for "gh auth"`, 1, "", 1, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeGhAuth(t, tc.tokenOut, tc.tokenCode, tc.statusOut, tc.statusCode)
			user, err := CheckAuth()
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, want error %v", err, tc.wantErr)
			}
			if user != tc.wantUser {
				t.Errorf("user = %q, want %q", user, tc.wantUser)
			}
		})
	}
}

// gh pr create warns about uncommitted changes on stderr, and the output is
// read combined, so the URL came back with the warning glued to the front:
// open-in-browser, copy-as-markdown and the history all broke.
func TestCreatePRFindsTheURLAmongWarnings(t *testing.T) {
	fakeGh(t, "Warning: 2 uncommitted changes\nhttps://github.com/acme/web/pull/12\n", 0)
	pr, err := CreatePR(t.TempDir(), "dev", "staging", "t", "b")
	if err != nil {
		t.Fatal(err)
	}
	if pr.URL != "https://github.com/acme/web/pull/12" || pr.Number != 12 {
		t.Errorf("got URL %q number %d", pr.URL, pr.Number)
	}
}

// --head matches the branch name whatever the owner, so on a public repo a
// fork's dev -> staging PR was taken for the release PR: its body got
// rewritten and it was offered for merging.
func TestExistingPRIgnoresForks(t *testing.T) {
	args := fakeGh(t, `[{"number":9,"isCrossRepository":true},{"number":4,"isCrossRepository":false}]`, 0)
	pr, err := GetExistingPR(t.TempDir(), "dev", "staging")
	if err != nil {
		t.Fatal(err)
	}
	if pr == nil || pr.Number != 4 {
		t.Errorf("got %+v, want #4, the one from this repo", pr)
	}
	if !strings.Contains(args(), "isCrossRepository") {
		t.Errorf("gh %s: want isCrossRepository requested", args())
	}

	fakeGh(t, `[{"number":9,"isCrossRepository":true}]`, 0)
	if pr, _ := GetExistingPR(t.TempDir(), "dev", "staging"); pr != nil {
		t.Errorf("got fork PR #%d as the release PR", pr.Number)
	}
}

// A remote reaching github.com through an SSH alias failed to parse, and the
// repo dropped out of All PRs and Actions.
func TestNWOFromRemote(t *testing.T) {
	orig := sshHostname
	t.Cleanup(func() { sshHostname = orig })
	sshHostname = func(alias string) string {
		return map[string]string{"github-work": "github.com", "ghe": "ghe.corp.example"}[alias]
	}
	cases := map[string]string{
		"https://github.com/acme/web.git":         "acme/web",
		"https://user:tok@github.com/acme/web":    "acme/web",
		"git@github.com:acme/web.git":             "acme/web",
		"ssh://git@github.com/acme/web.git":       "acme/web",
		"git@github-work:acme/web.git":            "acme/web",
		"ssh://git@github-work/acme/web":          "acme/web",
		"git@ghe:acme/web.git":                    "", // an Enterprise host is not github.com
		"https://github.example.com/acme/web.git": "",
		"https://github-work/acme/web":            "", // aliases are an SSH thing
		"git@github.com:acme/web/extra":           "",
	}
	for remote, want := range cases {
		got, err := nwoFromRemote(remote)
		if want == "" {
			if err == nil {
				t.Errorf("%s: got %q, want refused", remote, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %q", remote, got, err, want)
		}
	}
}

// The resolver reads ssh -G's "hostname" line, driven here by a fake ssh.
func TestSSHHostnameReadsSSHConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n[ \"$1\" = -G ] && printf 'user git\\nhostname github.com\\nport 22\\n'\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if got := sshHostname("github-work"); got != "github.com" {
		t.Errorf("sshHostname = %q, want github.com", got)
	}
}

// Jobs came back in gh's default page of 30, so a big workflow's pinned
// panel was missing jobs without saying so.
func TestWorkflowJobsAskForAFullPage(t *testing.T) {
	args := fakeGh(t, "HTTP/2.0 200 OK\nEtag: W/\"j\"\r\n\r\n{\"jobs\":[]}", 0)
	if _, err := GetWorkflowRunJobsByNWO("acme/web", 7); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(args(), "per_page=100") {
		t.Errorf("gh %s: want per_page=100", args())
	}
}

// One request compares every repo's steps. A repo missing a branch comes back
// as a null with an error beside it, and gh exits 1, but the rest must count.
func TestCompareBranchesKeepsWhatItCan(t *testing.T) {
	out := `{"data":{"c0":{"ref":{"compare":{"aheadBy":14}}},"c1":{"ref":null},"c2":{"ref":{"compare":null}}},"errors":[{"message":"not found"}]}`
	args := fakeGh(t, out, 1)
	pairs := []BranchPair{
		{NWO: "acme/web", Base: "staging", Head: "dev"},
		{NWO: "acme/api", Base: "staging", Head: "dev"},
		{NWO: "acme/old", Base: "main", Head: "staging"},
	}
	got, err := CompareBranches(pairs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[pairs[0]] != 14 {
		t.Errorf("got %v, want only acme/web at 14", got)
	}
	if a := args(); !strings.Contains(a, "b0=refs/heads/staging") || !strings.Contains(a, "h0=dev") || !strings.Contains(a, "o1=acme") {
		t.Errorf("gh %s: variables not passed as expected", a)
	}
	// Comparison has aheadBy. The query first asked for aheadCount, which does
	// not exist, and GitHub rejected the whole request.
	if a := args(); !strings.Contains(a, "compare(headRef: $h0) { aheadBy }") {
		t.Errorf("gh %s: want aheadBy", a)
	}
}

// Merge states come from their own query, one alias per PR, with the number
// passed as an Int. A PR GitHub cannot find is left out.
func TestMergeStatesKeepsWhatItCan(t *testing.T) {
	out := `{"data":{"m0":{"pullRequest":{"mergeable":"MERGEABLE","mergeStateStatus":"BLOCKED"}},"m1":{"pullRequest":null}},"errors":[{"message":"not found"}]}`
	args := fakeGh(t, out, 1)
	refs := []PRRef{{NWO: "acme/web", Number: 12}, {NWO: "acme/api", Number: 3}}
	got, err := MergeStates(refs)
	if err != nil {
		t.Fatal(err)
	}
	if want := (MergeState{Mergeable: "MERGEABLE", Status: "BLOCKED"}); len(got) != 1 || got[refs[0]] != want {
		t.Errorf("got %v, want only acme/web#12 %v", got, want)
	}
	if a := args(); !strings.Contains(a, "-F p0=12") || !strings.Contains(a, "n1=api") {
		t.Errorf("gh %s: variables not passed as expected", a)
	}
}

// A watched run pushed out of its repo's latest runs is fetched by ID.
func TestGetWorkflowRunByNWO(t *testing.T) {
	args := fakeGh(t, `{"id":42,"name":"deploy","status":"completed","conclusion":"success","updated_at":"2026-09-24T10:00:00Z"}`, 0)
	run, err := GetWorkflowRunByNWO("acme/web", 42)
	if err != nil {
		t.Fatal(err)
	}
	if run.DatabaseID != 42 || run.WorkflowName != "deploy" || run.Conclusion != "success" {
		t.Errorf("run = %+v", run)
	}
	if a := args(); !strings.Contains(a, "repos/acme/web/actions/runs/42") {
		t.Errorf("gh %s", a)
	}
}

// Polling every repo every five seconds spent the REST rate limit in about 15
// minutes. The second request for a path carries the ETag, and a 304 answer
// returns the stored body.
func TestPollsAreConditionalRequests(t *testing.T) {
	path := "repos/acme/poll/actions/runs?per_page=10"
	fakeGh(t, "HTTP/2.0 200 OK\nEtag: W/\"abc\"\r\nX-Other: 1\r\n\r\n{\"workflow_runs\":[{\"id\":7}]}", 0)
	if body, err := cachedGet(path); err != nil || !strings.Contains(string(body), `"id":7`) {
		t.Fatalf("first fetch: %q, %v", body, err)
	}

	// gh exits 1 on a 304 but still prints the status line.
	args := fakeGh(t, "HTTP/2.0 304 Not Modified\nEtag: W/\"abc\"\r\n\r\n", 1)
	body, err := cachedGet(path)
	if err != nil || !strings.Contains(string(body), `"id":7`) {
		t.Errorf("304: got %q, %v, want the stored body", body, err)
	}
	if a := args(); !strings.Contains(a, `If-None-Match: W/"abc"`) {
		t.Errorf("gh %s: the second request did not send the ETag", a)
	}

	fakeGh(t, "HTTP/2.0 502 Bad Gateway\n\r\n\r\nupstream", 1)
	if _, err := cachedGet("repos/acme/other"); err == nil {
		t.Error("a 502 was not an error")
	}
}
