package git

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JonrGull/prflow/internal/models"
	"github.com/JonrGull/prflow/internal/run"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// IsGitRepo checks if the path is a git repository
func IsGitRepo(path string) bool {
	_, err := git.PlainOpen(path)
	return err == nil
}

// GetRepoInfo opens a repository and gets basic info
func GetRepoInfo(path, displayName, group string) (*models.RepoInfo, error) {
	repo, err := git.PlainOpen(path)
	if err != nil {
		return nil, err
	}

	mainBranch, err := DetectMainBranch(repo)
	if err != nil {
		return nil, err
	}

	info := models.NewRepoInfo(path, displayName, mainBranch, group)
	return &info, nil
}

// GetCurrentRepoInfo gets info for the current working directory
func GetCurrentRepoInfo() (*models.RepoInfo, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	// Walk up to find git root
	path := cwd
	for {
		if IsGitRepo(path) {
			break
		}
		parent := filepath.Dir(path)
		if parent == path {
			return nil, os.ErrNotExist
		}
		path = parent
	}

	// Use directory name as display name
	displayName := filepath.Base(path)
	return GetRepoInfo(path, displayName, "")
}

// DetectMainBranch determines if the repo uses "main" or "master"
func DetectMainBranch(repo *git.Repository) (string, error) {
	// Check remote refs first
	refs, err := repo.References()
	if err != nil {
		return "main", nil
	}

	hasRemoteMain := false
	hasRemoteMaster := false
	hasLocalMain := false
	hasLocalMaster := false

	refs.ForEach(func(ref *plumbing.Reference) error {
		name := ref.Name().String()
		if name == "refs/remotes/origin/main" {
			hasRemoteMain = true
		}
		if name == "refs/remotes/origin/master" {
			hasRemoteMaster = true
		}
		if name == "refs/heads/main" {
			hasLocalMain = true
		}
		if name == "refs/heads/master" {
			hasLocalMaster = true
		}
		return nil
	})

	// Prefer remote refs
	if hasRemoteMain {
		return "main", nil
	}
	if hasRemoteMaster {
		return "master", nil
	}

	// Fall back to local refs
	if hasLocalMain {
		return "main", nil
	}
	if hasLocalMaster {
		return "master", nil
	}

	// Default to main
	return "main", nil
}

// FetchBranches fetches specified branches from origin using git CLI (to inherit SSH agent)
func FetchBranches(repoPath string, branches []string) error {
	args := append([]string{"fetch", "origin"}, branches...)

	output, err := run.Combined(run.Network, repoPath, "git", args...)
	if err != nil {
		if run.IsTimeout(err) {
			return &GitError{Command: "fetch", Output: err.Error()}
		}
		outputStr := strings.TrimSpace(string(output))
		if strings.Contains(outputStr, "couldn't find remote ref") {
			return &BranchNotFoundError{Branches: branches}
		}
		// Provide a more helpful error message
		if outputStr != "" {
			return &GitError{Command: "fetch", Output: outputStr}
		}
		return &GitError{Command: "fetch", Output: "Failed to fetch from remote (check network/auth)"}
	}

	return nil
}

// GitError provides better context for git command failures
type GitError struct {
	Command string
	Output  string
}

func (e *GitError) Error() string {
	return "git " + e.Command + ": " + e.Output
}

// gitErrDetail picks the most useful message for a failed git command.
// A timed-out command is killed before it prints anything, so its output is
// empty and only the error explains what happened.
func gitErrDetail(output []byte, err error) string {
	if detail := strings.TrimSpace(string(output)); detail != "" {
		return detail
	}
	return err.Error()
}

// BranchNotFoundError indicates a branch was not found on remote
type BranchNotFoundError struct {
	Branches []string
}

func (e *BranchNotFoundError) Error() string {
	return "Branch not found on remote: " + strings.Join(e.Branches, ", ")
}

// FindRepos finds every git repository the config points at: the glob patterns
// resolved under basePath, plus any explicitly listed repos.
// and merges any explicit repo entries from config
func FindRepos(basePath string, globs []models.GlobEntry, explicit []models.ExplicitRepo) ([]models.RepoInfo, error) {
	var repos []models.RepoInfo

	// Glob-based discovery (skip if basePath is empty or missing)
	if basePath != "" {
		if info, err := os.Stat(basePath); err != nil || !info.IsDir() {
			if len(explicit) == 0 {
				return nil, fmt.Errorf("repo directory not found: %s\nUpdate paths.repos_dir in your config", basePath)
			}
			// basePath missing but we have explicit repos — skip globs
		} else {
			for _, g := range globs {
				pattern := filepath.Join(basePath, g.Pattern)
				matches, err := filepath.Glob(pattern)
				if err != nil {
					return nil, fmt.Errorf("invalid %s glob %q: %w", g.Group, g.Pattern, err)
				}

				for _, path := range matches {
					info, err := os.Stat(path)
					if err != nil || !info.IsDir() {
						continue
					}

					repoName := filepath.Base(path)

					if IsGitRepo(path) {
						displayName := g.Group + "/" + repoName

						nestedRepos := findNestedRepos(path, g.Group, repoName)

						if len(nestedRepos) > 0 {
							repos = append(repos, nestedRepos...)
						} else {
							if repoInfo, err := GetRepoInfo(path, displayName, g.Group); err == nil {
								repos = append(repos, *repoInfo)
							}
						}
					}
				}
			}
		}
	}

	// Merge explicit repos, deduplicating by absolute path
	if len(explicit) > 0 {
		seen := make(map[string]bool)
		for _, r := range repos {
			if abs, err := filepath.Abs(r.Path); err == nil {
				seen[abs] = true
			}
		}

		for _, er := range explicit {
			abs, err := filepath.Abs(er.Path)
			if err != nil || seen[abs] {
				continue
			}
			if !IsGitRepo(er.Path) {
				continue
			}
			displayName := er.Group + "/" + filepath.Base(er.Path)
			if repoInfo, err := GetRepoInfo(er.Path, displayName, er.Group); err == nil {
				repos = append(repos, *repoInfo)
				seen[abs] = true
			}
		}
	}

	// Sort: group by group name, then nested repos at end of group, then by name
	sort.Slice(repos, func(i, j int) bool {
		a, b := repos[i], repos[j]

		catA := strings.Split(a.DisplayName, "/")[0]
		catB := strings.Split(b.DisplayName, "/")[0]

		if catA != catB {
			return catA < catB
		}

		if a.ParentRepo == nil && b.ParentRepo != nil {
			return true
		}
		if a.ParentRepo != nil && b.ParentRepo == nil {
			return false
		}
		if a.ParentRepo != nil && b.ParentRepo != nil {
			if *a.ParentRepo != *b.ParentRepo {
				return *a.ParentRepo < *b.ParentRepo
			}
		}

		return a.DisplayName < b.DisplayName
	})

	return repos, nil
}

// HasBranch checks if a branch exists (locally or remote)
func HasBranch(repoPath, branch string) bool {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return false
	}

	// Check remote ref first
	remoteRef := "refs/remotes/origin/" + branch
	if _, err := repo.Reference(plumbing.ReferenceName(remoteRef), true); err == nil {
		return true
	}

	// Check local ref
	localRef := "refs/heads/" + branch
	if _, err := repo.Reference(plumbing.ReferenceName(localRef), true); err == nil {
		return true
	}

	return false
}

// IsDirty checks if the repo has uncommitted changes (using git CLI for accuracy)
func IsDirty(repoPath string) (bool, error) {
	// Use git status --porcelain which outputs nothing if clean
	output, err := run.Output(run.Local, repoPath, "git", "status", "--porcelain")
	if err != nil {
		return false, err
	}

	// If output is empty, repo is clean
	return len(strings.TrimSpace(string(output))) > 0, nil
}

// CheckoutAndPull checks out the branch and pulls, returning commit count
func CheckoutAndPull(repoPath, branch string) (int, error) {
	// Get current commit before pull
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return 0, err
	}

	// Use git CLI for checkout (better SSH agent handling)
	if output, err := run.Combined(run.Network, repoPath, "git", "checkout", branch); err != nil {
		return 0, &GitError{Command: "checkout", Output: gitErrDetail(output, err)}
	}

	// Get HEAD before pull
	headBefore, err := repo.Head()
	if err != nil {
		return 0, err
	}

	// Use git CLI for pull (better SSH agent handling)
	if output, err := run.Combined(run.Network, repoPath, "git", "pull", "--ff-only"); err != nil {
		return 0, &GitError{Command: "pull", Output: gitErrDetail(output, err)}
	}

	// Re-open repo and get HEAD after pull
	repo, err = git.PlainOpen(repoPath)
	if err != nil {
		return 0, err
	}

	headAfter, err := repo.Head()
	if err != nil {
		return 0, err
	}

	// If same commit, no changes
	if headBefore.Hash() == headAfter.Hash() {
		return 0, nil
	}

	// Count commits between old and new HEAD
	commitCount := 0
	iter, err := repo.Log(&git.LogOptions{From: headAfter.Hash()})
	if err != nil {
		return 0, err
	}

	iter.ForEach(func(c *object.Commit) error {
		if c.Hash == headBefore.Hash() {
			return fmt.Errorf("stop") // Stop iteration
		}
		commitCount++
		return nil
	})

	return commitCount, nil
}

// findNestedRepos finds nested git repos inside a parent repo (a monorepo of services, say)
func findNestedRepos(parentPath, group, parentName string) []models.RepoInfo {
	var nested []models.RepoInfo

	entries, err := os.ReadDir(parentPath)
	if err != nil {
		return nested
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		repoName := entry.Name()
		// Skip hidden directories and common non-repo dirs
		if strings.HasPrefix(repoName, ".") || repoName == "node_modules" {
			continue
		}

		path := filepath.Join(parentPath, repoName)
		if IsGitRepo(path) {
			displayName := group + "/" + parentName + "/" + repoName

			if repoInfo, err := GetRepoInfo(path, displayName, group); err == nil {
				info := repoInfo.WithParent(parentName)
				nested = append(nested, info)
			}
		}
	}

	return nested
}
