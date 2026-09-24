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
	att := regexp.MustCompile(`(?i:ATT-)[0-9]+`)
	cases := []struct {
		re   *regexp.Regexp
		text string
		want []string
	}{
		{att, "Merge branch 'matt-12-login'", nil}, // MATT-12 is not ATT-12
		{att, "see ATT-12abc", nil},
		{att, "Merge pull request #4 from acme/jon/att-123-fix", []string{"ATT-123"}},
		{att, "(ATT-1) and ATT-2, ATT-3.", []string{"ATT-1", "ATT-2", "ATT-3"}},
		{att, "ATT-7_login", []string{"ATT-7"}},
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
	re := regexp.MustCompile(`(?i:ATT-)[0-9]+`)
	got := HighlightTickets("MATT-1 fixes ATT-2", re, func(s string) string { return "[" + s + "]" })
	if got != "MATT-1 fixes [ATT-2]" {
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
