package models

import "strings"

// RepoInfo contains information about a git repository
type RepoInfo struct {
	// Path to the repository
	Path string
	// DisplayName (e.g., "Frontend/web-app")
	DisplayName string
	// MainBranch name ("main" or "master")
	MainBranch string
	// Group name (e.g., "Frontend", "Backend")
	Group string
	// ParentRepo name if this is a nested repo (e.g., "services")
	ParentRepo *string
}

// InColumn returns true if this repo belongs to the given column (0=left, 1=right)
func (r RepoInfo) InColumn(column int, leftGroups map[string]bool) bool {
	isLeft := leftGroups[strings.ToLower(r.Group)]
	if column == 0 {
		return isLeft
	}
	return !isLeft
}

// NewRepoInfo creates a new RepoInfo
func NewRepoInfo(path, displayName, mainBranch, group string) RepoInfo {
	return RepoInfo{
		Path:        path,
		DisplayName: displayName,
		MainBranch:  mainBranch,
		Group:       group,
		ParentRepo:  nil,
	}
}

// WithParent sets the parent repo and returns the RepoInfo
func (r RepoInfo) WithParent(parent string) RepoInfo {
	r.ParentRepo = &parent
	return r
}

// ExplicitRepo is a user-configured repo path with a group
type ExplicitRepo struct {
	Path  string
	Group string
}

// GlobEntry maps a glob pattern to a group name
type GlobEntry struct {
	Pattern string
	Group   string
}

// ShortName returns just the last segment of DisplayName (after the last "/")
func (r RepoInfo) ShortName() string {
	if idx := strings.LastIndex(r.DisplayName, "/"); idx != -1 {
		return r.DisplayName[idx+1:]
	}
	return r.DisplayName
}
