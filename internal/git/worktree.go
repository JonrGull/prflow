package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JonrGull/prflow/internal/run"
)

// worktreesDir is where prflow puts the worktrees it makes, inside the main
// checkout. Outside it, the repos-dir globs would find each one as another repo.
const worktreesDir = ".worktrees"

// MainWorktree is the main checkout of the repo at path, which may itself be
// one of that repo's worktrees.
func MainWorktree(path string) (string, error) {
	out, err := run.Combined(run.Local, path, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", &GitError{Command: "rev-parse", Output: gitErrDetail(out, err)}
	}
	common := strings.TrimSpace(string(out))
	if filepath.Base(common) != ".git" {
		return "", fmt.Errorf("%s is a bare repository, with no checkout to add a worktree beside", common)
	}
	return filepath.Dir(common), nil
}

// PRWorktreePath is where the worktree for a repo's PR goes.
func PRWorktreePath(main string, number uint64) string {
	return filepath.Join(main, worktreesDir, fmt.Sprintf("pr-%d", number))
}

// WorktreeWithBranch is the checkout of repo that has branch checked out, if
// any: git refuses to check a branch out in two places.
func WorktreeWithBranch(repo, branch string) (string, bool) {
	out, err := run.Output(run.Local, repo, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return "", false
	}
	var path string
	for _, line := range strings.Split(string(out), "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			path = p
		}
		if line == "branch refs/heads/"+branch {
			return path, true
		}
	}
	return "", false
}

// AddWorktree makes a detached worktree of the main checkout at dir, first
// keeping the worktrees folder out of git status through info/exclude.
func AddWorktree(main, dir string) error {
	if err := excludeWorktrees(main); err != nil {
		return err
	}
	out, err := run.Combined(run.Slow, main, "git", "worktree", "add", "--detach", dir)
	if err != nil {
		return &GitError{Command: "worktree add", Output: gitErrDetail(out, err)}
	}
	return nil
}

// RemoveWorktree removes a worktree AddWorktree made, even with changes in it.
func RemoveWorktree(main, dir string) error {
	out, err := run.Combined(run.Local, main, "git", "worktree", "remove", "--force", dir)
	if err != nil {
		return &GitError{Command: "worktree remove", Output: gitErrDetail(out, err)}
	}
	return nil
}

func excludeWorktrees(main string) error {
	path := filepath.Join(main, ".git", "info", "exclude")
	line := "/" + worktreesDir + "/"
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, l := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(l) == line {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		line = "\n" + line
	}
	_, err = f.WriteString(line + "\n")
	return err
}
