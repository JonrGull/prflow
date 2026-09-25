package github

import (
	"encoding/json"
	"fmt"
	neturl "net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JonrGull/prflow/internal/models"
	"github.com/JonrGull/prflow/internal/run"
)

// CheckAuth verifies gh CLI is authenticated and returns the active username
//
// It used to take gh auth status's exit code as the answer. That command checks
// every stored account over the network, so one expired account, or simply
// being offline, failed it while the active login was fine, and every screen
// said "not authenticated" until a restart. gh auth token reads the stored
// login without the network, so it answers only the question asked.
func CheckAuth() (string, error) {
	notAuthed := fmt.Errorf("not authenticated with GitHub CLI. Run 'gh auth login', then try again")
	out, err := run.Combined(run.Local, "", "gh", "auth", "token")
	if err != nil {
		// gh before 2.17 has no auth token; fall back to the old check there.
		if !strings.Contains(string(out), "unknown command") {
			return "", notAuthed
		}
		if _, err := run.Combined(run.Network, "", "gh", "auth", "status"); err != nil {
			return "", notAuthed
		}
	}
	// The account name is only shown in the header, so a failed status still
	// yields it when the output names the account.
	status, _ := run.Combined(run.Network, "", "gh", "auth", "status")
	return parseAuthUser(string(status)), nil
}

// parseAuthUser finds the account in gh auth status output: "Logged in to
// github.com account <name>", or the same account in a failure line.
func parseAuthUser(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, "account "); idx != -1 {
			rest := line[idx+len("account "):]
			if sp := strings.IndexByte(rest, ' '); sp != -1 {
				return rest[:sp]
			}
			return rest
		}
	}
	return ""
}

// GetExistingPR gets an existing open PR for the given head -> base branch
func GetExistingPR(repoPath, headBranch, baseBranch string) (*models.GhPr, error) {
	output, err := run.Combined(run.Network, repoPath, "gh", "pr", "list",
		"--head", headBranch,
		"--base", baseBranch,
		"--state", "open",
		"--json", "number,url,title,state,body,headRefOid,isCrossRepository")
	if err != nil {
		return nil, fmt.Errorf("gh pr list failed: %s", string(output))
	}

	var prs []models.GhPr
	if err := json.Unmarshal(output, &prs); err != nil {
		return nil, fmt.Errorf("failed to parse gh pr list output: %w", err)
	}

	// --head matches the branch name whatever the owner. A fork's dev ->
	// staging PR is not the release PR, and used to be taken for it: edited,
	// and offered for merging.
	for i := range prs {
		if !prs[i].IsCrossRepository {
			return &prs[i], nil
		}
	}
	return nil, nil
}

// prURLPattern matches a pull request URL, capturing its number.
var prURLPattern = regexp.MustCompile(`https?://\S+/pull/(\d+)`)

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

	// gh prints the URL, but the output is read combined, and gh also warns on
	// stderr (about uncommitted changes, say). Taking the whole output as the
	// URL broke every link to the new PR, so find the URL in it.
	m := prURLPattern.FindStringSubmatch(string(output))
	if m == nil {
		return nil, fmt.Errorf("gh pr create printed no PR URL: %s", strings.TrimSpace(string(output)))
	}
	url := m[0]
	number, _ := strconv.ParseUint(m[1], 10, 64)

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

// searchRepoChunk is how many repos one search covers; the searches run at
// once. One search over 31 repos and 106 PRs timed out (HTTP 504) every time,
// and a search takes longer the more PRs it returns.
const searchRepoChunk = 4

// prFieldsAll is everything the All PRs table derives its columns from.
const prFieldsAll = `
	number url title state isDraft
	author { login }
	headRefName headRefOid baseRefName isCrossRepository
	repository { nameWithOwner }
	statusCheckRollup: commits(last: 1) {
		nodes {
			commit {
				statusCheckRollup {
					contexts(first: 100) {
						nodes {
							__typename
							... on CheckRun {
								name
								workflowName: checkSuite { workflowRun { workflow { name } } }
								status conclusion
							}
							... on StatusContext { context state }
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
		nodes { requestedReviewer { ... on User { login } ... on Team { name combinedSlug } } }
	}
	commits(last: 20) {
		nodes { commit { authoredDate } }
	}
`

// prFieldsDashboard is what the dashboard needs: which release PR it is, its
// checks, and whether a reviewer asked for changes.
const prFieldsDashboard = `
	number url title state isDraft
	headRefName baseRefName isCrossRepository
	repository { nameWithOwner }
	statusCheckRollup: commits(last: 1) {
		nodes { commit { statusCheckRollup { contexts(first: 100) { nodes {
			__typename
			... on CheckRun { name status conclusion }
			... on StatusContext { context state }
		} } } } }
	}
	latestReviews(last: 10) { nodes { state } }
	reviewDecision
`

// searchInGroups fetches all open PRs across multiple repos, searching a few
// repos at a time in parallel. Returns a map from NWO (owner/repo) to PRs.
func searchInGroups(nwos []string, fields string) (map[string][]models.GhPr, error) {
	if len(nwos) == 0 {
		return nil, nil
	}
	var chunks [][]string
	for i := 0; i < len(nwos); i += searchRepoChunk {
		chunks = append(chunks, nwos[i:min(i+searchRepoChunk, len(nwos))])
	}
	found := make([]map[string][]models.GhPr, len(chunks))
	errs := make([]error, len(chunks))
	var wg sync.WaitGroup
	for i, chunk := range chunks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			found[i], errs[i] = searchOpenPRs(chunk, fields)
		}()
	}
	wg.Wait()

	result := make(map[string][]models.GhPr)
	for i := range chunks {
		if errs[i] != nil {
			return nil, errs[i]
		}
		for nwo, prs := range found[i] {
			result[nwo] = append(result[nwo], prs...)
		}
	}
	return result, nil
}

// SearchAllOpenPRs fetches every open PR with what the All PRs table needs.
func SearchAllOpenPRs(nwos []string) (map[string][]models.GhPr, error) {
	return searchInGroups(nwos, prFieldsAll)
}

// SearchOpenPRsForDashboard fetches every open PR with only what the
// dashboard's cards need.
func SearchOpenPRsForDashboard(nwos []string) (map[string][]models.GhPr, error) {
	return searchInGroups(nwos, prFieldsDashboard)
}

func searchOpenPRs(nwos []string, fields string) (map[string][]models.GhPr, error) {

	// Build search query: "is:pr is:open repo:owner/repo1 repo:owner/repo2 ..."
	var repoClauses []string
	for _, nwo := range nwos {
		repoClauses = append(repoClauses, "repo:"+nwo)
	}
	searchQuery := "is:pr is:open " + strings.Join(repoClauses, " ")

	query := `query($q: String!, $cursor: String) {
		search(query: $q, type: ISSUE, first: 25, after: $cursor) {
			pageInfo { hasNextPage endCursor }
			nodes { ... on PullRequest { ` + fields + ` } }
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
	// ReviewDecision is REVIEW_REQUIRED, CHANGES_REQUESTED or APPROVED, and
	// empty where no review is required.
	ReviewDecision string `json:"reviewDecision"`
	Author         struct {
		Login string `json:"login"`
	} `json:"author"`
	HeadRefName       string `json:"headRefName"`
	HeadRefOid        string `json:"headRefOid"`
	BaseRefName       string `json:"baseRefName"`
	IsCrossRepository bool   `json:"isCrossRepository"`
	Repository        struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	StatusCheckRollup struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup struct {
					Contexts struct {
						Nodes []checkContextNode `json:"nodes"`
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
				Login        string `json:"login"`        // User
				Name         string `json:"name"`         // Team
				CombinedSlug string `json:"combinedSlug"` // Team
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

// checkContextNode is one entry in a commit's status rollup. GitHub mixes two
// kinds there: check runs (Actions and other GitHub Apps) and commit statuses
// (Vercel, CircleCI, Jenkins and anything else using the older status API).
type checkContextNode struct {
	TypeName     string `json:"__typename"`
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

	// StatusContext fields.
	Context string `json:"context"`
	State   string `json:"state"` // EXPECTED, ERROR, FAILURE, PENDING, SUCCESS
}

// check converts the node, or reports false for a kind it does not know.
//
// The query used to ask only for check runs, so every commit status came back
// as an empty object — which the CI column counted as a pass. A failing Vercel
// or CircleCI status showed a green tick.
func (n checkContextNode) check() (models.CheckRun, bool) {
	switch n.TypeName {
	case "CheckRun":
		return models.CheckRun{
			Name:         n.Name,
			WorkflowName: n.WorkflowName.WorkflowRun.Workflow.Name,
			Status:       n.Status,
			Conclusion:   n.Conclusion,
		}, true
	case "StatusContext":
		cr := models.CheckRun{Name: n.Context, Status: "PENDING"}
		switch strings.ToUpper(n.State) {
		case "SUCCESS", "FAILURE", "ERROR":
			cr.Status, cr.Conclusion = "COMPLETED", strings.ToUpper(n.State)
		}
		return cr, true
	}
	return models.CheckRun{}, false
}

func (n searchPRNode) toGhPr() models.GhPr {
	pr := models.GhPr{
		Number:     n.Number,
		URL:        n.URL,
		Title:      n.Title,
		State:      n.State,
		IsDraft:    n.IsDraft,
		HeadBranch: n.HeadRefName,
		HeadSHA:    n.HeadRefOid,
		BaseBranch: n.BaseRefName,

		IsCrossRepository: n.IsCrossRepository,
		ReviewDecision:    n.ReviewDecision,
	}
	pr.Author.Login = n.Author.Login

	// Status checks
	if len(n.StatusCheckRollup.Nodes) > 0 {
		for _, ctx := range n.StatusCheckRollup.Nodes[0].Commit.StatusCheckRollup.Contexts.Nodes {
			if cr, ok := ctx.check(); ok {
				pr.StatusCheckRollup = append(pr.StatusCheckRollup, cr)
			}
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
			Slug:  rr.RequestedReviewer.CombinedSlug,
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

// PRsAwaitingReviewers lists a repo's open PRs with a review requested. GraphQL
// reviewRequests leaves out bot reviewers (Copilot), so it asks REST. It used
// to ask once per PR, one after another, which made All PRs take 35s.
func PRsAwaitingReviewers(repoPath, nwo string) (map[uint64]bool, error) {
	output, err := run.Output(run.Network, repoPath, "gh", "api", "--paginate",
		fmt.Sprintf("repos/%s/pulls?state=open&per_page=100", nwo),
		"--jq", ".[] | select((.requested_reviewers | length) + (.requested_teams | length) > 0) | .number")
	if err != nil {
		return nil, fmt.Errorf("gh api pulls failed: %w", err)
	}
	awaiting := map[uint64]bool{}
	for _, line := range strings.Fields(string(output)) {
		if n, err := strconv.ParseUint(line, 10, 64); err == nil {
			awaiting[n] = true
		}
	}
	return awaiting, nil
}

// GetRepoNWO returns the "owner/repo" name by parsing the git remote URL.
func GetRepoNWO(repoPath string) (string, error) {
	output, err := run.Output(run.Network, repoPath, "git", "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("git remote get-url failed: %w", err)
	}
	return nwoFromRemote(strings.TrimSpace(string(output)))
}

// nwoFromRemote returns owner/repo for a github.com remote, including one
// that reaches github.com through an SSH host alias (git@github-work:o/r, the
// usual setup for a second account). Those used to fail, and the repo dropped
// out of All PRs and Actions. Other hosts are refused rather than guessed: a
// GitHub Enterprise repo is not the github.com repo of the same name.
func nwoFromRemote(remote string) (string, error) {
	host, path, viaSSH := splitRemote(remote)
	if host != "github.com" && !(viaSSH && host != "" && sshHostname(host) == "github.com") {
		return "", fmt.Errorf("cannot parse NWO from remote URL: %s", remote)
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if parts := strings.Split(path, "/"); len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("cannot parse NWO from remote URL: %s", remote)
	}
	return path, nil
}

// splitRemote splits a git remote into its host and path, and says whether
// it is reached over SSH, where the host may be an alias.
func splitRemote(remote string) (host, path string, viaSSH bool) {
	if strings.Contains(remote, "://") {
		u, err := neturl.Parse(remote)
		if err != nil {
			return "", "", false
		}
		return u.Hostname(), u.Path, u.Scheme == "ssh"
	}
	// scp-like: [user@]host:path
	colon := strings.IndexByte(remote, ':')
	if colon < 0 {
		return "", "", false
	}
	host = remote[:colon]
	if at := strings.LastIndexByte(host, '@'); at >= 0 {
		host = host[at+1:]
	}
	return host, remote[colon+1:], true
}

// sshHostname resolves an SSH host alias through the user's ssh config: ssh -G
// prints the effective settings without connecting. A var so tests can stub it.
var sshHostname = func(alias string) string {
	out, err := run.Output(run.Local, "", "ssh", "-G", alias)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(line, "hostname "); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// The PR body markers. prflow owns only the text between them, so a
// description or checklist someone adds to the PR survives the next update.
const (
	bodyStart = "<!-- prflow:tickets -->"
	bodyEnd   = "<!-- /prflow:tickets -->"
)

// GeneratePRBody returns the ticket section prflow writes into a PR body, or ""
// when there are no tickets. Without a Linear org the tickets are listed
// plainly: linking them built linear.app//issue/... URLs.
func GeneratePRBody(tickets []string, linearOrg string) string {
	if len(tickets) == 0 {
		return ""
	}

	var lines []string
	for _, t := range tickets {
		if linearOrg == "" {
			lines = append(lines, "### - Closes "+t)
			continue
		}
		lines = append(lines, fmt.Sprintf("### - Closes [%s](https://linear.app/%s/issue/%s)", t, linearOrg, strings.ToLower(t)))
	}

	return fmt.Sprintf("%s\n# Tickets\n\n%s\n%s", bodyStart, strings.Join(lines, "\n"), bodyEnd)
}

// mergeBody puts section — GeneratePRBody's output, possibly empty — into an
// existing PR body in place of prflow's previous section, keeping everything
// else.
//
// Updating a PR used to send the generated section as the whole body, so
// re-running a release wiped whatever people had written on the PR, and a run
// with no tickets blanked it. Bodies from before the markers existed start
// with a bare "# Tickets" list; that block is replaced the same way.
func mergeBody(existing, section string) string {
	// Bodies edited on github.com come back with CRLF line endings.
	body := strings.ReplaceAll(existing, "\r\n", "\n")

	// Blank-line-separated, skipping empty parts, so removing a section or
	// adding one to an empty body leaves no stray gaps.
	join := func(parts ...string) string {
		var kept []string
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				kept = append(kept, p)
			}
		}
		return strings.Join(kept, "\n\n")
	}

	// The section is replaced where it stands, so the body's layout holds.
	if i := strings.Index(body, bodyStart); i >= 0 {
		if j := strings.Index(body[i:], bodyEnd); j >= 0 {
			return join(body[:i], section, body[i+j+len(bodyEnd):])
		}
	}
	if strings.HasPrefix(body, "# Tickets\n") {
		lines := strings.Split(body, "\n")
		n := 1
		for n < len(lines) && (lines[n] == "" || strings.HasPrefix(lines[n], "### - Closes ")) {
			n++
		}
		return join(section, strings.Join(lines[n:], "\n"))
	}
	// A body prflow has never written to: the tickets go after what is there.
	return join(body, section)
}

// MergePR merges a PR using regular merge (not squash)
//
// headSHA is the head commit the user saw. GitHub refuses the merge if the
// branch has moved since: without it, commits pushed after the list loaded
// (after review, say) were merged along with the rest.
func MergePR(repoPath string, prNumber uint64, headSHA string) error {
	if headSHA == "" {
		return fmt.Errorf("no head commit recorded for #%d; refresh and try again", prNumber)
	}
	output, err := run.Combined(run.Network, repoPath, "gh", "pr", "merge",
		strconv.FormatUint(prNumber, 10),
		"--merge",
		"--match-head-commit", headSHA,
		"--delete-branch=false")
	if err != nil {
		return mergeFailure(output)
	}

	return nil
}

func mergeFailure(output []byte) error {
	out := strings.TrimSpace(string(output))
	if strings.Contains(out, "Head branch was modified") {
		return fmt.Errorf("new commits were pushed since the list loaded; refresh and review them")
	}
	return fmt.Errorf("gh pr merge failed: %s", out)
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
// It is polled, so it is a conditional request (cachedGet).
func ListWorkflowRunsByNWO(nwo string, limit int) ([]models.WorkflowRun, error) {
	body, err := cachedGet(fmt.Sprintf("repos/%s/actions/runs?per_page=%d", nwo, limit))
	if err != nil {
		return nil, fmt.Errorf("gh api actions/runs failed: %s", err)
	}
	return parseWorkflowRuns(body)
}

// ListWorkflowRunsSince fetches the runs created since a time, up to the API's
// page size of 100.
func ListWorkflowRunsSince(nwo string, since time.Time) ([]models.WorkflowRun, error) {
	return listWorkflowRuns(fmt.Sprintf("repos/%s/actions/runs?per_page=100&created=>=%s",
		nwo, since.UTC().Format(time.RFC3339)))
}

func listWorkflowRuns(path string) ([]models.WorkflowRun, error) {
	output, err := run.Combined(run.Network, "", "gh", "api", path)
	if err != nil {
		return nil, fmt.Errorf("gh api actions/runs failed: %s", string(output))
	}
	return parseWorkflowRuns(output)
}

func parseWorkflowRuns(output []byte) ([]models.WorkflowRun, error) {
	var resp struct {
		WorkflowRuns []restRun `json:"workflow_runs"`
	}
	if err := json.Unmarshal(output, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse actions/runs: %w", err)
	}
	var runs []models.WorkflowRun
	for _, r := range resp.WorkflowRuns {
		runs = append(runs, r.toModel())
	}
	return runs, nil
}

// GetWorkflowRunByNWO fetches one workflow run by its ID.
func GetWorkflowRunByNWO(nwo string, runID uint64) (models.WorkflowRun, error) {
	output, err := run.Combined(run.Network, "", "gh", "api", fmt.Sprintf("repos/%s/actions/runs/%d", nwo, runID))
	if err != nil {
		return models.WorkflowRun{}, fmt.Errorf("gh api actions/runs/%d failed: %s", runID, string(output))
	}
	var r restRun
	if err := json.Unmarshal(output, &r); err != nil {
		return models.WorkflowRun{}, fmt.Errorf("failed to parse actions/runs/%d: %w", runID, err)
	}
	return r.toModel(), nil
}

// restRun is a workflow run as the REST API returns it.
type restRun struct {
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
}

func (r restRun) toModel() models.WorkflowRun {
	conclusion := ""
	if r.Conclusion != nil {
		conclusion = *r.Conclusion
	}
	return models.WorkflowRun{
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
	}
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
// It is polled for running runs, so it is a conditional request (cachedGet).
func GetWorkflowRunJobsByNWO(nwo string, runID uint64) ([]models.WorkflowJob, error) {
	// GitHub's largest page: the default of 30 silently dropped jobs from
	// bigger workflows' panels.
	output, err := cachedGet(fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=100", nwo, runID))
	if err != nil {
		return nil, fmt.Errorf("gh api actions/runs/jobs failed: %s", err)
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
		// Update existing PR, keeping what people wrote on it.
		pr, err := UpdatePR(repoPath, existing.Number, title, mergeBody(existing.Body, body))
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
