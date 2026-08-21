package models

// PullStatus represents the status of a pull operation for a single repo
type PullStatus int

const (
	PullUpdated         PullStatus = iota // Pulled with new commits
	PullUpToDate                          // Already up to date
	PullSkippedNoBranch                   // Branch doesn't exist
	PullSkippedDirty                      // Has uncommitted changes
	PullFailed                            // Pull failed
)

// PullResult represents the result of pulling a single repo
type PullResult struct {
	Repo        RepoInfo
	Status      PullStatus
	CommitCount int    // Only for PullUpdated
	Error       string // Only for PullFailed
}
