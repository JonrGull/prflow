package app

import (
	"fmt"
	"time"

	"github.com/JonrGull/prflow/internal/linear"
	"github.com/JonrGull/prflow/internal/models"
)

// Fixtures for --dry-run mode.
//
// These used to sit inline in each command in commands.go, so roughly 260 lines
// of fake data were interleaved with the real logic in the largest file in the
// package — reading what a command actually does meant skipping past a wall of
// test scaffolding first.
//
// The artificial delays are deliberate and preserved exactly. They are what
// exercises the spinner and progress rendering; a fixture that returned
// instantly would skip those paths entirely, and now that the animation tick
// stops when nothing is moving, it would exercise even less than before.

const (
	dryRunInstant = 200 * time.Millisecond // per-repo pull
	dryRunQuick   = 300 * time.Millisecond
	dryRunNormal  = 500 * time.Millisecond
	dryRunLong    = 800 * time.Millisecond
	dryRunFetch   = 1000 * time.Millisecond // scanning every repo for open PRs
	dryRunCreate  = 1500 * time.Millisecond // creating a PR, the slowest real operation
)

// dryRunRepos is the shared set of fake repositories. The commands each used to
// declare their own near-identical copy, which is why the dry-run repo list
// differed subtly between screens.
func dryRunRepos() []models.RepoInfo {
	return []models.RepoInfo{
		{Path: "/home/user/repos/frontend/web", DisplayName: "Frontend/web", MainBranch: "main", Group: "Frontend"},
		{Path: "/home/user/repos/frontend/mobile", DisplayName: "Frontend/mobile", MainBranch: "main", Group: "Frontend"},
		{Path: "/home/user/repos/backend/api", DisplayName: "Backend/api", MainBranch: "main", Group: "Backend"},
		{Path: "/home/user/repos/backend/workers", DisplayName: "Backend/workers", MainBranch: "main", Group: "Backend"},
	}
}

// --- single PR flow ---------------------------------------------------------

func dryRunCommits() fetchCommitsResult {
	time.Sleep(dryRunLong)
	return fetchCommitsResult{
		commits: []models.CommitInfo{
			{Hash: "abc1234", Message: "feat: Add new dashboard component", Tickets: []string{"ATT-1234"}},
			{Hash: "def5678", Message: "fix: Resolve authentication bug", Tickets: []string{"ATT-1235"}},
			{Hash: "ghi9012", Message: "chore: Update dependencies", Tickets: []string{}},
			{Hash: "jkl3456", Message: "feat: Implement user settings page", Tickets: []string{"ATT-1236", "ATT-1237"}},
			{Hash: "mno7890", Message: "docs: Update README with new instructions", Tickets: []string{}},
		},
		tickets: []string{"ATT-1234", "ATT-1235", "ATT-1236", "ATT-1237"},
	}
}

func dryRunPRCreated(repo *models.RepoInfo) prCreatedResult {
	time.Sleep(dryRunCreate)
	repoName := "example-repo"
	if repo != nil {
		repoName = repo.DisplayName
	}
	return prCreatedResult{
		url:      "https://github.com/example/" + repoName + "/pull/123 (DRY RUN)",
		prNumber: 123,
	}
}

// --- batch flow -------------------------------------------------------------

func dryRunBatchCommits(repos []models.RepoInfo, selected []bool) batchCommitsResult {
	time.Sleep(dryRunQuick)
	selectedCount := 0
	for i := range repos {
		if i < len(selected) && selected[i] {
			selectedCount++
		}
	}
	return batchCommitsResult{
		tickets:          []string{"ATT-1234", "ATT-1235", "ATT-1236"},
		existingPRs:      1,
		reposWithCommits: selectedCount,
	}
}

// dryRunRepoCommits fakes the per-repo commit fetch during batch discovery.
// idx staggers the delay so repos resolve one after another rather than all at
// once, which is what makes the loading spinners visible; every third repo comes
// back empty so the "nothing to merge" state is reachable.
func dryRunRepoCommits(idx int) []models.CommitInfo {
	time.Sleep(time.Duration(100+idx*50) * time.Millisecond)
	if idx%3 == 0 {
		return nil
	}
	return []models.CommitInfo{
		{Hash: "abc1234", Message: "feat: Add new feature", Tickets: []string{"ATT-1234"}},
		{Hash: "def5678", Message: "fix: Bug fix", Tickets: []string{"ATT-1235"}},
	}
}

func dryRunBatchRepoResult(repo models.RepoInfo) batchRepoResult {
	time.Sleep(dryRunNormal)
	url := "https://github.com/example/" + repo.DisplayName + "/pull/123 (DRY RUN)"
	return batchRepoResult{result: models.BatchResult{
		Repo:   repo,
		Status: models.Created,
		PrURL:  &url,
	}}
}

// --- merge flow -------------------------------------------------------------

// dryRunMergeResult takes the partially-built result the command has already
// assembled and marks it successful, rather than rebuilding it here.
func dryRunMergeResult(base models.MergeResult) mergeCompleteResult {
	time.Sleep(dryRunNormal)
	base.Success = true
	return mergeCompleteResult{result: base}
}

// --- open PRs ---------------------------------------------------------------

// dryRunOpenPRs fakes an open PR on every configured step for one repo and on
// the first step only for another, so the columns show both a repo mid-chain
// and a repo that has only just started.
func dryRunOpenPRs(flows []models.Flow) openPRsFetchedResult {
	time.Sleep(dryRunFetch)
	repos := dryRunRepos()

	bodies := []string{
		"# Tickets\n\n### - Closes [ATT-1234](https://linear.app/example/issue/att-1234)\n### - Closes [ATT-5678](https://linear.app/example/issue/att-5678)",
		"# Tickets\n\n### - Closes [ATT-1234](https://linear.app/example/issue/att-1234)",
	}

	// steps builds a status carrying a PR on the first withPR steps and none on
	// the rest, mirroring what GetOpenReleasePRs returns: one entry per
	// configured step, with a nil PR where nothing is open.
	steps := func(slug string, firstNumber uint64, withPR int) models.RepoPrStatus {
		status := models.RepoPrStatus{Flows: make([]models.FlowPR, 0, len(flows))}
		for i, f := range flows {
			entry := models.FlowPR{Flow: f}
			if i < withPR {
				n := firstNumber + uint64(i)
				entry.PR = &models.GhPr{
					Number: n,
					URL:    fmt.Sprintf("https://github.com/example/%s/pull/%d", slug, n),
					Title:  f.Display("main"),
					State:  "open",
					Body:   bodies[i%len(bodies)],
				}
			}
			status.Flows = append(status.Flows, entry)
		}
		return status
	}

	return openPRsFetchedResult{entries: []OpenPREntry{
		{Repo: repos[0], Status: steps("web", 123, len(flows))},
		{Repo: repos[2], Status: steps("api", 456, 1)},
	}}
}

// dryRunAllOpenPRs covers the status columns on the All Open PRs screen: a
// passing PR, a draft awaiting review, a failing build, a pending run, and one
// with no checks at all.
func dryRunAllOpenPRs() allOpenPRsFetchedResult {
	time.Sleep(dryRunLong)
	repos := dryRunRepos()

	checksCI := []models.CheckRun{
		{Name: "lint-and-typecheck", WorkflowName: "CI", Status: "COMPLETED", Conclusion: "SUCCESS"},
		{Name: "deploy-preview", WorkflowName: "Preview Deploy Lite", Status: "COMPLETED", Conclusion: "SUCCESS"},
		{Name: "e2e", WorkflowName: "Preview Deploy Lite", Status: "COMPLETED", Conclusion: "SUCCESS"},
	}
	checksFail := []models.CheckRun{
		{Name: "Build", WorkflowName: "Automatic build", Status: "COMPLETED", Conclusion: "FAILURE"},
	}
	checksPending := []models.CheckRun{
		{Name: "shared", WorkflowName: "CI", Status: "IN_PROGRESS", Conclusion: ""},
	}

	e2eComment := models.PrComment{Body: "<!-- e2e-results -->\n**16** passed, **0** failed, **2** skipped (18 total)", CreatedAt: "2026-03-10T08:00:00Z"}
	e2eComment.Author.Login = "github-actions"
	userComment := models.PrComment{Body: "Looks good!", CreatedAt: "2026-03-10T09:00:00Z"}
	userComment.Author.Login = "somedev"
	botComment := models.PrComment{Body: "linear sync", CreatedAt: "2026-03-10T07:00:00Z"}
	botComment.Author.Login = "linear"

	review := models.PrReview{State: "COMMENTED", SubmittedAt: "2026-03-10T10:00:00Z"}
	review.Author.Login = "somedev"
	latestReview := models.PrReview{State: "COMMENTED", SubmittedAt: "2026-03-10T10:00:00Z"}
	latestReview.Author.Login = "copilot"

	commitOld := models.PrCommit{AuthoredDate: "2026-03-09T10:00:00Z"} // before the review -> current
	commitNew := models.PrCommit{AuthoredDate: "2026-03-11T10:00:00Z"} // after the review  -> stale
	reviewReq := models.ReviewRequest{Login: "reviewer1"}

	author := struct {
		Login string `json:"login"`
	}{Login: "lorenzo"}

	entries := []allPREntry{
		{Repo: repos[0], PR: models.GhPr{Number: 123, URL: "https://github.com/example/web/pull/123", Title: "feat: Add dashboard component", State: "open", Author: author, HeadBranch: "dev", BaseBranch: "staging", StatusCheckRollup: checksCI, Comments: []models.PrComment{userComment, botComment, e2eComment}, Reviews: []models.PrReview{review, review, review}, LatestReviews: []models.PrReview{latestReview}, Commits: []models.PrCommit{commitOld}}},
		{Repo: repos[0], PR: models.GhPr{Number: 124, URL: "https://github.com/example/web/pull/124", Title: "staging → main", State: "open", Author: author, IsDraft: true, HeadBranch: "staging", BaseBranch: "main", StatusCheckRollup: checksCI, Comments: []models.PrComment{userComment, userComment, e2eComment}, Reviews: []models.PrReview{review}, LatestReviews: []models.PrReview{latestReview}, Commits: []models.PrCommit{commitNew}, ReviewRequests: []models.ReviewRequest{reviewReq}}},
		{Repo: repos[2], PR: models.GhPr{Number: 456, URL: "https://github.com/example/api/pull/456", Title: "fix: Resolve auth timeout", State: "open", Author: author, HeadBranch: "feature/auth", BaseBranch: "dev", StatusCheckRollup: checksFail, Comments: []models.PrComment{userComment}, Reviews: []models.PrReview{review, review}, LatestReviews: []models.PrReview{latestReview}, Commits: []models.PrCommit{commitNew}}},
		{Repo: repos[2], PR: models.GhPr{Number: 457, URL: "https://github.com/example/api/pull/457", Title: "dev → staging", State: "open", Author: author, HeadBranch: "dev", BaseBranch: "staging", StatusCheckRollup: checksPending, Comments: nil}},
		{Repo: repos[3], PR: models.GhPr{Number: 89, URL: "https://github.com/example/workers/pull/89", Title: "chore: Update queue handler", State: "open", Author: author, HeadBranch: "chore/queue", BaseBranch: "main", StatusCheckRollup: checksCI[:1], Comments: nil}},
	}
	for i := range entries {
		enrichAllPREntry(&entries[i])
	}
	return allOpenPRsFetchedResult{entries: entries}
}

// --- pull -------------------------------------------------------------------

// dryRunPullResult varies the outcome by repo name so the summary screen shows
// a mix of updated, up-to-date and skipped, deterministically for a given repo.
func dryRunPullResult(repo models.RepoInfo) pullRepoResult {
	time.Sleep(dryRunInstant)

	hash := 0
	for _, c := range repo.DisplayName {
		hash += int(c)
	}
	statuses := []models.PullStatus{
		models.PullUpdated,
		models.PullUpToDate,
		models.PullSkippedNoBranch,
	}
	status := statuses[hash%len(statuses)]

	commits := 0
	if status == models.PullUpdated {
		commits = (hash % 5) + 1
	}
	return makePullResult(repo, status, commits, "")
}

// --- QA tagging -------------------------------------------------------------

func dryRunQaPerson(name string) qaPersonLookupResult {
	time.Sleep(dryRunNormal)
	return qaPersonLookupResult{name: name, id: "fake-uuid-1234"}
}

func dryRunTicketTitles(tickets []string) qaTicketTitlesResult {
	time.Sleep(dryRunQuick)
	fakes := []string{
		"Add new dashboard component",
		"Resolve authentication bug",
		"Implement user settings page",
		"Update pricing page layout",
	}
	titles := make(map[string]string, len(tickets))
	for i, t := range tickets {
		titles[t] = fakes[i%len(fakes)]
	}
	return qaTicketTitlesResult{titles: titles}
}

func dryRunQaTagResults(toTag []string) qaTagResultMsg {
	time.Sleep(dryRunQuick)
	results := make([]linear.QaTagResult, len(toTag))
	for i, t := range toTag {
		results[i] = linear.QaTagResult{Ticket: t, Success: true}
	}
	return qaTagResultMsg{results: results}
}

// --- GitHub Actions ---------------------------------------------------------

// dryRunActionsRuns covers every status icon: running, queued, succeeded,
// failed and cancelled. Timestamps are relative to now so the "x minutes ago"
// column and the auto-refresh countdown both render realistically.
func dryRunActionsRuns() actionsRunsFetchedResult {
	time.Sleep(dryRunLong)
	now := time.Now()
	repos := dryRunRepos()

	return actionsRunsFetchedResult{entries: []actionsEntry{
		{Repo: repos[0], Run: models.WorkflowRun{DatabaseID: 1001, DisplayTitle: "feat: Add dashboard", WorkflowName: "CI", Status: "in_progress", HeadBranch: "dev", Event: "push", URL: "https://github.com/example/web/actions/runs/1001", CreatedAt: now.Add(-3 * time.Minute), UpdatedAt: now.Add(-1 * time.Minute)}},
		{Repo: repos[0], Run: models.WorkflowRun{DatabaseID: 1000, DisplayTitle: "fix: Auth bug", WorkflowName: "CI", Status: "completed", Conclusion: "success", HeadBranch: "staging", Event: "push", URL: "https://github.com/example/web/actions/runs/1000", CreatedAt: now.Add(-30 * time.Minute), UpdatedAt: now.Add(-25 * time.Minute)}},
		{Repo: repos[1], Run: models.WorkflowRun{DatabaseID: 2001, DisplayTitle: "chore: Update deps", WorkflowName: "CI", Status: "completed", Conclusion: "failure", HeadBranch: "dev", Event: "push", URL: "https://github.com/example/mobile/actions/runs/2001", CreatedAt: now.Add(-10 * time.Minute), UpdatedAt: now.Add(-8 * time.Minute)}},
		{Repo: repos[2], Run: models.WorkflowRun{DatabaseID: 3001, DisplayTitle: "feat: Add endpoints", WorkflowName: "CI", Status: "in_progress", HeadBranch: "dev", Event: "push", URL: "https://github.com/example/api/actions/runs/3001", CreatedAt: now.Add(-2 * time.Minute), UpdatedAt: now.Add(-30 * time.Second)}},
		{Repo: repos[2], Run: models.WorkflowRun{DatabaseID: 3002, DisplayTitle: "Deploy staging", WorkflowName: "Deploy", Status: "queued", HeadBranch: "staging", Event: "push", URL: "https://github.com/example/api/actions/runs/3002", CreatedAt: now.Add(-1 * time.Minute), UpdatedAt: now.Add(-1 * time.Minute)}},
		{Repo: repos[2], Run: models.WorkflowRun{DatabaseID: 3000, DisplayTitle: "fix: DB migration", WorkflowName: "CI", Status: "completed", Conclusion: "success", HeadBranch: "main", Event: "push", URL: "https://github.com/example/api/actions/runs/3000", CreatedAt: now.Add(-1 * time.Hour), UpdatedAt: now.Add(-55 * time.Minute)}},
		{Repo: repos[3], Run: models.WorkflowRun{DatabaseID: 4001, DisplayTitle: "refactor: Queue handler", WorkflowName: "CI", Status: "completed", Conclusion: "cancelled", HeadBranch: "dev", Event: "push", URL: "https://github.com/example/workers/actions/runs/4001", CreatedAt: now.Add(-15 * time.Minute), UpdatedAt: now.Add(-12 * time.Minute)}},
	}}
}

// dryRunActionsJobs gives a pinned run one finished job, one in progress and
// one queued, so the expanded step list has something of each to draw.
func dryRunActionsJobs(runID uint64) actionsJobsFetchedResult {
	time.Sleep(dryRunNormal)
	now := time.Now()

	return actionsJobsFetchedResult{runID: runID, jobs: []models.WorkflowJob{
		{
			Name: "build", Status: "completed", Conclusion: "success",
			StartedAt: now.Add(-5 * time.Minute), CompletedAt: now.Add(-3 * time.Minute),
			URL: "https://github.com/example/repo/actions/runs/1001/job/1",
			Steps: []models.WorkflowStep{
				{Name: "Checkout", Number: 1, Status: "completed", Conclusion: "success"},
				{Name: "Setup Node", Number: 2, Status: "completed", Conclusion: "success"},
				{Name: "Install deps", Number: 3, Status: "completed", Conclusion: "success"},
				{Name: "Build", Number: 4, Status: "completed", Conclusion: "success"},
			},
		},
		{
			Name: "test", Status: "in_progress", Conclusion: "",
			StartedAt: now.Add(-2 * time.Minute),
			URL:       "https://github.com/example/repo/actions/runs/1001/job/2",
			Steps: []models.WorkflowStep{
				{Name: "Checkout", Number: 1, Status: "completed", Conclusion: "success"},
				{Name: "Setup Node", Number: 2, Status: "completed", Conclusion: "success"},
				{Name: "Run tests", Number: 3, Status: "in_progress", Conclusion: ""},
				{Name: "Upload coverage", Number: 4, Status: "queued", Conclusion: ""},
			},
		},
		{
			Name: "deploy", Status: "queued", Conclusion: "",
			URL: "https://github.com/example/repo/actions/runs/1001/job/3",
			Steps: []models.WorkflowStep{
				{Name: "Deploy to staging", Number: 1, Status: "queued", Conclusion: ""},
			},
		},
	}}
}
