package models

// MergePrEntry represents an entry for a PR in the merge selection list
type MergePrEntry struct {
	// Repo is the repository info
	Repo RepoInfo
	// PrNumber is the PR number
	PrNumber uint64
	// PrTitle is the PR title
	PrTitle string
	// URL is the PR URL
	URL string
	// PrBody is the PR body (used for ticket extraction)
	PrBody string
	// Flow is the release step this PR belongs to
	Flow Flow
}
