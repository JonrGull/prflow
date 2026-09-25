package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A PR's worktree goes in the main checkout, found even from another worktree,
// and must not show up in that checkout's git status as an untracked folder.
func TestPRWorktreeStaysOutOfStatus(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	main, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitInit(t, main)
	exclude := filepath.Join(main, ".git", "info", "exclude")
	if err := os.WriteFile(exclude, []byte("*.log"), 0o644); err != nil { // no final newline
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other")
	if out, err := exec.Command("git", "-C", main, "worktree", "add", "-q", "--detach", other).CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}

	found, err := MainWorktree(other)
	if err != nil || found != main {
		t.Fatalf("MainWorktree(other worktree) = %q, %v; want %q", found, err, main)
	}
	if err := AddWorktree(main, PRWorktreePath(main, 7)); err != nil {
		t.Fatal(err)
	}
	if err := AddWorktree(main, PRWorktreePath(main, 8)); err != nil {
		t.Fatal(err)
	}
	status, _ := exec.Command("git", "-C", main, "status", "--porcelain").Output()
	if len(status) > 0 {
		t.Errorf("git status in the main checkout = %q, want nothing", status)
	}
	got, _ := os.ReadFile(exclude)
	if string(got) != "*.log\n/.worktrees/\n" {
		t.Errorf("info/exclude = %q, want the old line kept and the worktrees folder added once", got)
	}
	if path, ok := WorktreeWithBranch(main, "main"); !ok || path != main {
		t.Errorf("WorktreeWithBranch(main) = %q, %v; want the main checkout", path, ok)
	}
}
