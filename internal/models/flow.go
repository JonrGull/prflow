package models

import "fmt"

// A release flow: one branch merged into another.
//
// This replaces a two-value PrType enum whose branch names were baked into a
// switch — dev→staging and staging→main, with no way to say otherwise. That made
// the tool usable only by teams with exactly those three branches; anyone on
// git-flow, trunk-based development, or any other naming could not use it at
// all. Flows are configured instead, so the branching model is the user's.

// DefaultBranchToken is the base value meaning "whatever this repo's own default
// branch is". Repos disagree — main here, master there — so a flow that targets
// the trunk has to name it indirectly.
const DefaultBranchToken = "@default"

// Flow is one step of a release: Head is merged into Base.
type Flow struct {
	Head string
	Base string

	// Title seeds the PR title input. Empty means use the flow's description,
	// which is what the earlier dev→staging step did; the final step into the
	// trunk wanted something else ("Sprint # ").
	Title string
}

// BaseBranch resolves Base against the repo's own default branch.
func (f Flow) BaseBranch(defaultBranch string) string {
	if f.Base == DefaultBranchToken {
		return defaultBranch
	}
	return f.Base
}

// HeadBranch is the branch being merged.
func (f Flow) HeadBranch() string { return f.Head }

// Display names the flow for the UI, resolving the default-branch token.
func (f Flow) Display(defaultBranch string) string {
	return fmt.Sprintf("%s → %s", f.Head, f.BaseBranch(defaultBranch))
}

// DefaultTitle is what the title input starts with.
func (f Flow) DefaultTitle(defaultBranch string) string {
	if f.Title != "" {
		return f.Title
	}
	return f.Display(defaultBranch)
}

// IsZero reports an unset flow, so callers can treat "no flow chosen" as a
// value rather than needing a pointer.
func (f Flow) IsZero() bool { return f.Head == "" && f.Base == "" }
