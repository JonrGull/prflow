package models

// CheckRun represents a single CI check on a PR
type CheckRun struct {
	Name         string `json:"name"`
	WorkflowName string `json:"workflowName"`
	Status       string `json:"status"`     // COMPLETED, IN_PROGRESS, QUEUED
	Conclusion   string `json:"conclusion"` // SUCCESS, FAILURE, SKIPPED, etc.
}

// PrComment represents a comment on a PR
type PrComment struct {
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
}

// PrReview represents a review on a PR
type PrReview struct {
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	State       string `json:"state"` // COMMENTED, APPROVED, CHANGES_REQUESTED, etc.
	SubmittedAt string `json:"submittedAt"`
}

// PrCommit represents a commit on a PR
type PrCommit struct {
	AuthoredDate string `json:"authoredDate"`
}

// ReviewRequest represents a pending review request
type ReviewRequest struct {
	Login    string `json:"login"`      // for user reviewers
	Name     string `json:"name"`       // for team reviewers
	TypeName string `json:"__typename"` // "User" or "Team"
}

// GhPr represents GitHub PR info returned from gh CLI
type GhPr struct {
	Number uint64 `json:"number"`
	URL    string `json:"url"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Body   string `json:"body"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	IsDraft           bool            `json:"isDraft"`
	HeadBranch        string          `json:"headRefName"`
	BaseBranch        string          `json:"baseRefName"`
	StatusCheckRollup []CheckRun      `json:"statusCheckRollup"`
	Comments          []PrComment     `json:"comments"`
	Reviews           []PrReview      `json:"reviews"`
	LatestReviews     []PrReview      `json:"latestReviews"`
	ReviewRequests    []ReviewRequest `json:"reviewRequests"`
	Commits           []PrCommit      `json:"commits"`
}

// InlineComment represents a single inline review comment (from REST API)
type InlineComment struct {
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	Body           string `json:"body"`
	CreatedAt      string `json:"created_at"`
	PullRequestURL string `json:"pull_request_url"`
}

// FlowPR pairs a configured flow with the open PR for it, if any.
type FlowPR struct {
	Flow Flow
	PR   *GhPr // nil when no PR is open for this flow
}

// RepoPrStatus is the open release PRs for a repo, one entry per configured
// flow, in configured order.
//
// This used to be two named fields — DevToStaging and StagingToMain — which is
// how the two-step model reached all the way into the data layer. Carrying the
// flow with each PR rather than relying on index order keeps callers honest
// across package boundaries.
type RepoPrStatus struct {
	Flows []FlowPR
}

// Open returns the flows that have a PR waiting.
func (s RepoPrStatus) Open() []FlowPR {
	var out []FlowPR
	for _, f := range s.Flows {
		if f.PR != nil {
			out = append(out, f)
		}
	}
	return out
}

// HasAny reports whether any flow has an open PR.
func (s RepoPrStatus) HasAny() bool { return len(s.Open()) > 0 }
