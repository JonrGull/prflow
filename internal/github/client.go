package github

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/JonrGull/prflow/internal/models"
	"github.com/JonrGull/prflow/internal/run"
)

// CheckAuth verifies gh CLI is authenticated and returns the active username
func CheckAuth() (string, error) {
	output, err := run.Combined(run.Network, "", "gh", "auth", "status")
	if err != nil {
		return "", fmt.Errorf("not authenticated with GitHub CLI. Run 'gh auth login' first")
	}
	// Parse "Logged in to github.com account <username>"
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, "account "); idx != -1 {
			rest := line[idx+len("account "):]
			if sp := strings.IndexByte(rest, ' '); sp != -1 {
				return rest[:sp], nil
			}
			return rest, nil
		}
	}
	return "", nil
}

// GetExistingPR gets an existing open PR for the given head -> base branch
func GetExistingPR(repoPath, headBranch, baseBranch string) (*models.GhPr, error) {
	output, err := run.Combined(run.Network, repoPath, "gh", "pr", "list",
		"--head", headBranch,
		"--base", baseBranch,
		"--state", "open",
		"--json", "number,url,title,state,body")
	if err != nil {
		return nil, fmt.Errorf("gh pr list failed: %s", string(output))
	}

	var prs []models.GhPr
	if err := json.Unmarshal(output, &prs); err != nil {
		return nil, fmt.Errorf("failed to parse gh pr list output: %w", err)
	}

	if len(prs) == 0 {
		return nil, nil
	}

	return &prs[0], nil
}

// CreatePR creates a new pull request
func CreatePR(repoPath, headBranch, baseBranch, title, body string) (*models.GhPr, error) {
	output, err := run.Combined(run.Network, repoPath, "gh", "pr", "create",
		"--head", headBranch,
		"--base", baseBranch,
		"--title", title,
		"--body", body)
	if err != nil {
		return nil, fmt.Errorf("gh pr create failed: %s", string(output))
	}

	// gh pr create outputs the URL
	url := strings.TrimSpace(string(output))

	// Extract PR number from URL (e.g., https://github.com/org/repo/pull/123)
	parts := strings.Split(url, "/")
	var number uint64
	if len(parts) > 0 {
		number, _ = strconv.ParseUint(parts[len(parts)-1], 10, 64)
	}

	return &models.GhPr{
		Number: number,
		URL:    url,
		Title:  title,
		State:  "open",
	}, nil
}

// UpdatePR updates an existing PR's title and body
func UpdatePR(repoPath string, prNumber uint64, title, body string) (*models.GhPr, error) {
	output, err := run.Combined(run.Network, repoPath, "gh", "pr", "edit",
		strconv.FormatUint(prNumber, 10),
		"--title", title,
		"--body", body)
	if err != nil {
		return nil, fmt.Errorf("gh pr edit failed: %s", string(output))
	}

	// Get the updated PR info
	return GetPR(repoPath, prNumber)
}

// GetPR gets PR details by number
func GetPR(repoPath string, prNumber uint64) (*models.GhPr, error) {
	output, err := run.Output(run.Network, repoPath, "gh", "pr", "view",
		strconv.FormatUint(prNumber, 10),
		"--json", "number,url,title,state,body")
	if err != nil {
		return nil, fmt.Errorf("gh pr view failed: %w", err)
	}

	var pr models.GhPr
	if err := json.Unmarshal(output, &pr); err != nil {
		return nil, fmt.Errorf("failed to parse gh pr view output: %w", err)
	}

	return &pr, nil
}

// GetOpenReleasePRs checks each configured release step for an open PR and
// returns one entry per step, with a nil PR where nothing is open. It used to
// check exactly two hardcoded branch pairs.
func GetOpenReleasePRs(repoPath string, flows []models.Flow, defaultBranch string) (*models.RepoPrStatus, error) {
	status := &models.RepoPrStatus{Flows: make([]models.FlowPR, 0, len(flows))}

	for _, flow := range flows {
		base := flow.BaseBranch(defaultBranch)
		pr, err := GetExistingPR(repoPath, flow.HeadBranch(), base)
		if err != nil {
			return nil, fmt.Errorf("checking %s->%s: %w", flow.HeadBranch(), base, err)
		}
		status.Flows = append(status.Flows, models.FlowPR{Flow: flow, PR: pr})
	}

	return status, nil
}

// ListOpenPRs lists all open PRs for a repo
func ListOpenPRs(repoPath string, limit int) ([]models.GhPr, error) {
	output, err := run.Combined(run.Network, repoPath, "gh", "pr", "list",
		"--state", "open",
		"--json", "number,url,title,state,body,isDraft,author,headRefName,baseRefName,statusCheckRollup,comments,reviews,latestReviews,reviewRequests,commits",
		"--limit", strconv.Itoa(limit))
	if err != nil {
		return nil, fmt.Errorf("gh pr list failed: %s", string(output))
	}

	var prs []models.GhPr
	if err := json.Unmarshal(output, &prs); err != nil {
		return nil, fmt.Errorf("failed to parse gh pr list output: %w", err)
	}

	return prs, nil
}

// SearchAllOpenPRs fetches all open PRs across multiple repos in a single GraphQL query.
// Returns a map from NWO (owner/repo) to PRs.
func SearchAllOpenPRs(nwos []string) (map[string][]models.GhPr, error) {
	if len(nwos) == 0 {
		return nil, nil
	}

	// Build search query: "is:pr is:open repo:owner/repo1 repo:owner/repo2 ..."
	var repoClauses []string
	for _, nwo := range nwos {
		repoClauses = append(repoClauses, "repo:"+nwo)
	}
	searchQuery := "is:pr is:open " + strings.Join(repoClauses, " ")

	// GraphQL query with all fields we need
	query := `query($q: String!, $cursor: String) {
		search(query: $q, type: ISSUE, first: 100, after: $cursor) {
			pageInfo { hasNextPage endCursor }
			nodes {
				... on PullRequest {
					number url title state isDraft
					author { login }
					headRefName baseRefName
					repository { nameWithOwner }
					statusCheckRollup: commits(last: 1) {
						nodes {
							commit {
								statusCheckRollup {
									contexts(first: 50) {
										nodes {
											... on CheckRun {
												name
												workflowName: checkSuite { workflowRun { workflow { name } } }
												status conclusion
											}
										}
									}
								}
							}
						}
					}
					comments(last: 50) {
						nodes { author { login } body createdAt }
					}
					reviews(last: 50) {
						nodes { author { login } state submittedAt }
					}
					latestReviews(last: 10) {
						nodes { author { login } state submittedAt }
					}
					reviewRequests(last: 10) {
						nodes { requestedReviewer { ... on User { login } ... on Team { name } } }
					}
					commits(last: 20) {
						nodes { commit { authoredDate } }
					}
				}
			}
		}
	}`

	var allNodes []json.RawMessage

	var cursor *string
	for {
		args := []string{"api", "graphql",
			"-f", "q=" + searchQuery,
			"-f", "query=" + query,
		}
		if cursor != nil {
			args = append(args, "-f", "cursor="+*cursor)
		} else {
			args = append(args, "-F", "cursor=null")
		}

		output, err := run.Combined(run.Network, "", "gh", args...)
		if err != nil {
			return nil, fmt.Errorf("gh api graphql failed: %s", string(output))
		}

		var resp struct {
			Data struct {
				Search struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []json.RawMessage `json:"nodes"`
				} `json:"search"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.Unmarshal(output, &resp); err != nil {
			return nil, fmt.Errorf("failed to parse graphql response: %w", err)
		}
		if len(resp.Errors) > 0 {
			var msgs []string
			for _, e := range resp.Errors {
				msgs = append(msgs, e.Message)
			}
			return nil, fmt.Errorf("graphql errors: %s", strings.Join(msgs, "; "))
		}

		allNodes = append(allNodes, resp.Data.Search.Nodes...)

		if !resp.Data.Search.PageInfo.HasNextPage {
			break
		}
		c := resp.Data.Search.PageInfo.EndCursor
		cursor = &c
	}

	// Parse each node into our PR model
	result := make(map[string][]models.GhPr)
	for _, raw := range allNodes {
		var node searchPRNode
		if err := json.Unmarshal(raw, &node); err != nil {
			continue
		}
		pr := node.toGhPr()
		nwo := node.Repository.NameWithOwner
		result[nwo] = append(result[nwo], pr)
	}

	return result, nil
}

// searchPRNode maps the GraphQL search result shape to our internal model
type searchPRNode struct {
	Number  uint64 `json:"number"`
	URL     string `json:"url"`
	Title   string `json:"title"`
	State   string `json:"state"`
	IsDraft bool   `json:"isDraft"`
	Author  struct {
		Login string `json:"login"`
	} `json:"author"`
	HeadRefName string `json:"headRefName"`
	BaseRefName string `json:"baseRefName"`
	Repository  struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	StatusCheckRollup struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup struct {
					Contexts struct {
						Nodes []struct {
							Name         string `json:"name"`
							WorkflowName struct {
								WorkflowRun struct {
									Workflow struct {
										Name string `json:"name"`
									} `json:"workflow"`
								} `json:"workflowRun"`
							} `json:"workflowName"`
							Status     string `json:"status"`
							Conclusion string `json:"conclusion"`
						} `json:"nodes"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"statusCheckRollup"`
	Comments struct {
		Nodes []struct {
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
			Body      string `json:"body"`
			CreatedAt string `json:"createdAt"`
		} `json:"nodes"`
	} `json:"comments"`
	Reviews struct {
		Nodes []struct {
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
			State       string `json:"state"`
			SubmittedAt string `json:"submittedAt"`
		} `json:"nodes"`
	} `json:"reviews"`
	LatestReviews struct {
		Nodes []struct {
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
			State       string `json:"state"`
			SubmittedAt string `json:"submittedAt"`
		} `json:"nodes"`
	} `json:"latestReviews"`
	ReviewRequests struct {
		Nodes []struct {
			RequestedReviewer struct {
				Login string `json:"login"` // User
				Name  string `json:"name"`  // Team
			} `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"reviewRequests"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				AuthoredDate string `json:"authoredDate"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

func (n searchPRNode) toGhPr() models.GhPr {
	pr := models.GhPr{
		Number:     n.Number,
		URL:        n.URL,
		Title:      n.Title,
		State:      n.State,
		IsDraft:    n.IsDraft,
		HeadBranch: n.HeadRefName,
		BaseBranch: n.BaseRefName,
	}
	pr.Author.Login = n.Author.Login

	// Status checks
	if len(n.StatusCheckRollup.Nodes) > 0 {
		for _, ctx := range n.StatusCheckRollup.Nodes[0].Commit.StatusCheckRollup.Contexts.Nodes {
			pr.StatusCheckRollup = append(pr.StatusCheckRollup, models.CheckRun{
				Name:         ctx.Name,
				WorkflowName: ctx.WorkflowName.WorkflowRun.Workflow.Name,
				Status:       ctx.Status,
				Conclusion:   ctx.Conclusion,
			})
		}
	}

	// Comments
	for _, c := range n.Comments.Nodes {
		comment := models.PrComment{Body: c.Body, CreatedAt: c.CreatedAt}
		comment.Author.Login = c.Author.Login
		pr.Comments = append(pr.Comments, comment)
	}

	// Reviews
	for _, r := range n.Reviews.Nodes {
		review := models.PrReview{State: r.State, SubmittedAt: r.SubmittedAt}
		review.Author.Login = r.Author.Login
		pr.Reviews = append(pr.Reviews, review)
	}

	// Latest reviews
	for _, r := range n.LatestReviews.Nodes {
		review := models.PrReview{State: r.State, SubmittedAt: r.SubmittedAt}
		review.Author.Login = r.Author.Login
		pr.LatestReviews = append(pr.LatestReviews, review)
	}

	// Review requests
	for _, rr := range n.ReviewRequests.Nodes {
		pr.ReviewRequests = append(pr.ReviewRequests, models.ReviewRequest{
			Login: rr.RequestedReviewer.Login,
			Name:  rr.RequestedReviewer.Name,
		})
	}

	// Commits
	for _, c := range n.Commits.Nodes {
		pr.Commits = append(pr.Commits, models.PrCommit{
			AuthoredDate: c.Commit.AuthoredDate,
		})
	}

	return pr
}

// ListPRReviewComments fetches individual inline review comments for all PRs
// in a repo via the REST API. Returns a map from PR number to comments.
func ListPRReviewComments(repoPath string, nwo string) (map[uint64][]models.InlineComment, error) {
	// Fetch review comments (most recent 100)
	output, err := run.Combined(run.Network, repoPath, "gh", "api",
		fmt.Sprintf("repos/%s/pulls/comments?per_page=100&sort=created&direction=desc", nwo))
	if err != nil {
		return nil, fmt.Errorf("gh api pulls/comments failed: %s", string(output))
	}

	var comments []models.InlineComment
	if err := json.Unmarshal(output, &comments); err != nil {
		return nil, fmt.Errorf("failed to parse review comments: %w", err)
	}

	// Group by PR number (extracted from pull_request_url)
	result := make(map[uint64][]models.InlineComment)
	for _, c := range comments {
		// URL format: https://api.github.com/repos/{owner}/{repo}/pulls/{number}
		parts := strings.Split(c.PullRequestURL, "/")
		if len(parts) > 0 {
			if num, err := strconv.ParseUint(parts[len(parts)-1], 10, 64); err == nil {
				result[num] = append(result[num], c)
			}
		}
	}
	return result, nil
}

// HasRequestedReviewers checks if a PR has pending reviewer requests via REST API.
// GraphQL reviewRequests doesn't include bot reviewers (e.g. Copilot), so we use REST.
func HasRequestedReviewers(repoPath string, nwo string, prNumber uint64) bool {
	output, err := run.Output(run.Network, repoPath, "gh", "api",
		fmt.Sprintf("repos/%s/pulls/%d/requested_reviewers", nwo, prNumber),
		"--jq", ".users | length + (.teams | length)")
	if err != nil {
		return false
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(output)))
	return err == nil && count > 0
}

// GetRepoNWO returns the "owner/repo" name by parsing the git remote URL.
func GetRepoNWO(repoPath string) (string, error) {
	output, err := run.Output(run.Network, repoPath, "git", "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("git remote get-url failed: %w", err)
	}
	url := strings.TrimSpace(string(output))
	url = strings.TrimSuffix(url, ".git")
	if idx := strings.Index(url, "github.com:"); idx >= 0 {
		return url[idx+len("github.com:"):], nil
	}
	if idx := strings.Index(url, "github.com/"); idx >= 0 {
		return url[idx+len("github.com/"):], nil
	}
	return "", fmt.Errorf("cannot parse NWO from remote URL: %s", url)
}

// GeneratePRBody generates PR body with ticket links using Linear magic words
func GeneratePRBody(tickets []string, linearOrg string) string {
	if len(tickets) == 0 {
		return ""
	}

	var lines []string
	for _, t := range tickets {
		line := fmt.Sprintf("### - Closes [%s](https://linear.app/%s/issue/%s)", t, linearOrg, strings.ToLower(t))
		lines = append(lines, line)
	}

	return fmt.Sprintf("# Tickets\n\n%s", strings.Join(lines, "\n"))
}

// MergePR merges a PR using regular merge (not squash)
func MergePR(repoPath string, prNumber uint64) error {
	output, err := run.Combined(run.Network, repoPath, "gh", "pr", "merge",
		strconv.FormatUint(prNumber, 10),
		"--merge",
		"--delete-branch=false")
	if err != nil {
		return fmt.Errorf("gh pr merge failed: %s", string(output))
	}

	return nil
}

// ListWorkflowRuns lists recent workflow runs for a repo
func ListWorkflowRuns(repoPath string, limit int) ([]models.WorkflowRun, error) {
	nwo, err := GetRepoNWO(repoPath)
	if err != nil {
		return nil, err
	}
	return ListWorkflowRunsByNWO(nwo, limit)
}

// ListWorkflowRunsByNWO fetches workflow runs via REST API using owner/repo name.
// Avoids spawning gh run list (which uses GraphQL internally).
func ListWorkflowRunsByNWO(nwo string, limit int) ([]models.WorkflowRun, error) {
	output, err := run.Combined(run.Network, "", "gh", "api",
		fmt.Sprintf("repos/%s/actions/runs?per_page=%d", nwo, limit))
	if err != nil {
		return nil, fmt.Errorf("gh api actions/runs failed: %s", string(output))
	}

	var resp struct {
		WorkflowRuns []struct {
			ID           uint64    `json:"id"`
			DisplayTitle string    `json:"display_title"`
			Name         string    `json:"name"` // workflow name
			Status       string    `json:"status"`
			Conclusion   *string   `json:"conclusion"`
			HeadBranch   string    `json:"head_branch"`
			Event        string    `json:"event"`
			HTMLURL      string    `json:"html_url"`
			CreatedAt    time.Time `json:"created_at"`
			UpdatedAt    time.Time `json:"updated_at"`
		} `json:"workflow_runs"`
	}
	if err := json.Unmarshal(output, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse actions/runs: %w", err)
	}

	var runs []models.WorkflowRun
	for _, r := range resp.WorkflowRuns {
		conclusion := ""
		if r.Conclusion != nil {
			conclusion = *r.Conclusion
		}
		runs = append(runs, models.WorkflowRun{
			DatabaseID:   r.ID,
			DisplayTitle: r.DisplayTitle,
			WorkflowName: r.Name,
			Status:       r.Status,
			Conclusion:   conclusion,
			HeadBranch:   r.HeadBranch,
			Event:        r.Event,
			URL:          r.HTMLURL,
			CreatedAt:    r.CreatedAt,
			UpdatedAt:    r.UpdatedAt,
		})
	}
	return runs, nil
}

// GetWorkflowRunJobs gets the jobs for a specific workflow run via REST API
func GetWorkflowRunJobs(repoPath string, runID uint64) ([]models.WorkflowJob, error) {
	nwo, err := GetRepoNWO(repoPath)
	if err != nil {
		return nil, err
	}
	return GetWorkflowRunJobsByNWO(nwo, runID)
}

// GetWorkflowRunJobsByNWO fetches jobs via REST API using owner/repo name
func GetWorkflowRunJobsByNWO(nwo string, runID uint64) ([]models.WorkflowJob, error) {
	output, err := run.Combined(run.Network, "", "gh", "api",
		fmt.Sprintf("repos/%s/actions/runs/%d/jobs", nwo, runID))
	if err != nil {
		return nil, fmt.Errorf("gh api actions/runs/jobs failed: %s", string(output))
	}

	// REST API uses snake_case; map to our camelCase model
	var resp struct {
		Jobs []struct {
			Name        string     `json:"name"`
			Status      string     `json:"status"`
			Conclusion  *string    `json:"conclusion"`
			StartedAt   *time.Time `json:"started_at"`
			CompletedAt *time.Time `json:"completed_at"`
			HTMLURL     string     `json:"html_url"`
			Steps       []struct {
				Name       string  `json:"name"`
				Number     int     `json:"number"`
				Status     string  `json:"status"`
				Conclusion *string `json:"conclusion"`
			} `json:"steps"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(output, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse jobs: %w", err)
	}

	var jobs []models.WorkflowJob
	for _, j := range resp.Jobs {
		conclusion := ""
		if j.Conclusion != nil {
			conclusion = *j.Conclusion
		}
		var steps []models.WorkflowStep
		for _, s := range j.Steps {
			sc := ""
			if s.Conclusion != nil {
				sc = *s.Conclusion
			}
			steps = append(steps, models.WorkflowStep{
				Name:       s.Name,
				Number:     s.Number,
				Status:     s.Status,
				Conclusion: sc,
			})
		}
		var startedAt, completedAt time.Time
		if j.StartedAt != nil {
			startedAt = *j.StartedAt
		}
		if j.CompletedAt != nil {
			completedAt = *j.CompletedAt
		}
		jobs = append(jobs, models.WorkflowJob{
			Name:        j.Name,
			Status:      j.Status,
			Conclusion:  conclusion,
			StartedAt:   startedAt,
			CompletedAt: completedAt,
			Steps:       steps,
			URL:         j.HTMLURL,
		})
	}

	return jobs, nil
}

// CreateOrUpdatePR creates a new PR or updates an existing one
func CreateOrUpdatePR(repoPath, headBranch, baseBranch, title string, tickets []string, linearOrg string) (*models.GhPr, bool, error) {
	body := GeneratePRBody(tickets, linearOrg)

	// Check for existing PR
	existing, err := GetExistingPR(repoPath, headBranch, baseBranch)
	if err != nil {
		return nil, false, err
	}

	if existing != nil {
		// Update existing PR
		pr, err := UpdatePR(repoPath, existing.Number, title, body)
		if err != nil {
			return nil, false, err
		}
		return pr, true, nil // true = updated
	}

	// Create new PR
	pr, err := CreatePR(repoPath, headBranch, baseBranch, title, body)
	if err != nil {
		return nil, false, err
	}
	return pr, false, nil // false = created
}
