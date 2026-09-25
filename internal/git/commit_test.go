package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/JonrGull/prflow/internal/models"
)

// A match cut out of a longer word is not a ticket.
func TestExtractTicketsNeedsWordBoundaries(t *testing.T) {
	ops := regexp.MustCompile(`(?i:OPS-)[0-9]+`)
	cases := []struct {
		re   *regexp.Regexp
		text string
		want []string
	}{
		{ops, "Merge branch 'stops-12-login'", nil}, // STOPS-12 is not OPS-12
		{ops, "see OPS-12abc", nil},
		{ops, "Merge pull request #4 from acme/jon/ops-123-fix", []string{"OPS-123"}},
		{ops, "(OPS-1) and OPS-2, OPS-3.", []string{"OPS-1", "OPS-2", "OPS-3"}},
		{ops, "OPS-7_login", []string{"OPS-7"}},
		// An edge that is not a letter or digit is never cut.
		{regexp.MustCompile(`#[0-9]+`), "fixes PR#12 and #13", []string{"#12", "#13"}},
	}
	for _, tc := range cases {
		got := ExtractTickets(tc.text, tc.re)
		if len(got) == 0 {
			got = nil
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: got %v, want %v", tc.text, got, tc.want)
		}
	}
}

// The screen highlights exactly what goes into the PR.
func TestHighlightTicketsMatchesExtraction(t *testing.T) {
	re := regexp.MustCompile(`(?i:OPS-)[0-9]+`)
	got := HighlightTickets("STOPS-1 fixes OPS-2", re, func(s string) string { return "[" + s + "]" })
	if got != "STOPS-1 fixes [OPS-2]" {
		t.Errorf("got %q", got)
	}
	if strings.Contains(HighlightTickets("x", nil, strings.ToUpper), "X") {
		t.Error("a nil pattern changed the text")
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// A repo holding a submodule was replaced by the submodule in discovery: the
// nested-repo scan is for folders of independent clones, and a submodule
// (whose .git is a file pointing elsewhere) is part of its parent.
func TestSubmoduleDoesNotReplaceItsRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	base := t.TempDir()
	web := filepath.Join(base, "frontend", "web")
	modules := filepath.Join(t.TempDir(), "proto")
	for _, d := range []string{web, modules} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		gitInit(t, d)
	}
	// What git submodule leaves in the parent's working tree.
	proto := filepath.Join(web, "proto")
	if err := os.MkdirAll(proto, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proto, ".git"), []byte("gitdir: "+filepath.Join(modules, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repos, err := FindRepos(base, []models.GlobEntry{{Pattern: "frontend/*", Group: "Frontend"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range repos {
		names = append(names, r.DisplayName)
	}
	if len(names) != 1 || names[0] != "Frontend/web" {
		t.Errorf("found %v, want just Frontend/web", names)
	}
}

// The case the nested scan exists for: a repo folder holding independent
// clones lists those clones.
func TestNestedClonesAreListed(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	base := t.TempDir()
	meta := filepath.Join(base, "backend", "meta")
	svc := filepath.Join(meta, "svc")
	for _, d := range []string{meta, svc} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		gitInit(t, d)
	}
	repos, err := FindRepos(base, []models.GlobEntry{{Pattern: "backend/*", Group: "Backend"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].DisplayName != "Backend/meta/svc" {
		t.Errorf("found %+v, want Backend/meta/svc", repos)
	}
}

// A branch from the config reaches git's argument list, where one starting
// with - is an option: --upload-pack=cmd runs a command.
func TestBranchNamesCannotBeOptions(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	err := FetchBranches(t.TempDir(), []string{"--upload-pack=touch " + marker})
	if err == nil || !strings.Contains(err.Error(), "cannot start with -") {
		t.Errorf("err = %v, want the name refused", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the option ran")
	}
	if _, err := CheckoutAndPull(t.TempDir(), "-b"); err == nil || !strings.Contains(err.Error(), "cannot start with -") {
		t.Errorf("checkout err = %v, want the name refused", err)
	}
}

// The default branch was guessed from which of main and master exist, so a
// repo whose default is trunk (or master, with a stale main left on the
// remote) got PRs against the wrong base. origin/HEAD says what it really is.
func TestDefaultBranchFollowsOriginHEAD(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	gitInit(t, dir)
	for _, args := range [][]string{
		{"update-ref", "refs/remotes/origin/main", "HEAD"},
		{"update-ref", "refs/remotes/origin/trunk", "HEAD"},
		{"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	info, err := GetRepoInfo(dir, "G/r", "G")
	if err != nil {
		t.Fatal(err)
	}
	if info.MainBranch != "trunk" {
		t.Errorf("default branch = %q, want trunk from origin/HEAD", info.MainBranch)
	}
}

// A repo whose GitHub default is dev, released dev → staging → @default, got
// PRs from staging into dev. The app asks for main or master instead, and
// that guess must not follow origin/HEAD back to dev.
func TestGuessMainBranchIgnoresOriginHEAD(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	gitInit(t, dir)
	for _, args := range [][]string{
		{"update-ref", "refs/remotes/origin/master", "HEAD"},
		{"update-ref", "refs/remotes/origin/dev", "HEAD"},
		{"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/dev"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if info, _ := GetRepoInfo(dir, "G/r", "G"); info.MainBranch != "dev" {
		t.Errorf("default branch = %q, want dev from origin/HEAD", info.MainBranch)
	}
	if got := GuessMainBranch(dir); got != "master" {
		t.Errorf("guess = %q, want master", got)
	}
}
