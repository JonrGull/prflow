package models

// MergeResult represents the result of merging a single PR
type MergeResult struct {
	// RepoName (e.g., "frontend/web-app")
	RepoName string
	// PrNumber is the PR number
	PrNumber uint64
	// PrTitle is the PR title
	PrTitle string
	// PrBody is the PR body (contains ticket references)
	PrBody string
	// Flow is the release step this PR belonged to
	Flow Flow
	// MainBranch of the repo (for display purposes)
	MainBranch string
	// Success indicates whether merge succeeded
	Success bool
	// Error message if failed
	Error *string
	// URL is the PR URL
	URL string
}
