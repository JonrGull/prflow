package app

import (
	"fmt"
	"time"

	"github.com/JonrGull/prflow/internal/github"
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
			{Hash: "abc1234", Message: "feat: Add new dashboard component", Tickets: []string{"PROJ-1234"}},
			{Hash: "def5678", Message: "fix: Resolve authentication bug", Tickets: []string{"PROJ-1235"}},
			{Hash: "ghi9012", Message: "chore: Update dependencies", Tickets: []string{}},
			{Hash: "jkl3456", Message: "feat: Implement user settings page", Tickets: []string{"PROJ-1236", "PROJ-1237"}},
			{Hash: "mno7890", Message: "docs: Update README with new instructions", Tickets: []string{}},
		},
		tickets: []string{"PROJ-1234", "PROJ-1235", "PROJ-1236", "PROJ-1237"},
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
		tickets:          []string{"PROJ-1234", "PROJ-1235", "PROJ-1236"},
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
		{Hash: "abc1234", Message: "feat: Add new feature", Tickets: []string{"PROJ-1234"}},
		{Hash: "def5678", Message: "fix: Bug fix", Tickets: []string{"PROJ-1235"}},
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
		"# Tickets\n\n### - Closes [PROJ-1234](https://linear.app/example/issue/proj-1234)\n### - Closes [PROJ-5678](https://linear.app/example/issue/proj-5678)",
		"# Tickets\n\n### - Closes [PROJ-1234](https://linear.app/example/issue/proj-1234)",
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
	// The viewer is lorenzo, on the example/web team: #123 is his, #124 asks
	// for his review, #457 asks his team's, and the other two are not his.
	reviewReq := models.ReviewRequest{Login: "lorenzo"}
	teamReq := models.ReviewRequest{Name: "Web", Slug: "example/web"}

	type login = struct {
		Login string `json:"login"`
	}
	lorenzo, maria := login{Login: "lorenzo"}, login{Login: "maria"}

	entries := []allPREntry{
		{Repo: repos[0], PR: models.GhPr{Number: 123, URL: "https://github.com/example/web/pull/123", Title: "feat: Add dashboard component", State: "open", Author: lorenzo, HeadBranch: "dev", BaseBranch: "staging", HeadSHA: "a1b2c3d", StatusCheckRollup: checksCI, Comments: []models.PrComment{userComment, botComment, e2eComment}, Reviews: []models.PrReview{review, review, review}, LatestReviews: []models.PrReview{latestReview}, Commits: []models.PrCommit{commitOld}}},
		{Repo: repos[0], PR: models.GhPr{Number: 124, URL: "https://github.com/example/web/pull/124", Title: "staging → main", State: "open", Author: maria, IsDraft: true, HeadBranch: "staging", BaseBranch: "main", HeadSHA: "b2c3d4e", StatusCheckRollup: checksCI, Comments: []models.PrComment{userComment, userComment, e2eComment}, Reviews: []models.PrReview{review}, LatestReviews: []models.PrReview{latestReview}, Commits: []models.PrCommit{commitNew}, ReviewRequests: []models.ReviewRequest{reviewReq}}},
		{Repo: repos[2], PR: models.GhPr{Number: 456, URL: "https://github.com/example/api/pull/456", Title: "fix: Resolve auth timeout", State: "open", Author: maria, HeadBranch: "feature/auth", BaseBranch: "dev", HeadSHA: "c3d4e5f", StatusCheckRollup: checksFail, Comments: []models.PrComment{userComment}, Reviews: []models.PrReview{review, review}, LatestReviews: []models.PrReview{latestReview}, Commits: []models.PrCommit{commitNew}}},
		{Repo: repos[2], PR: models.GhPr{Number: 457, URL: "https://github.com/example/api/pull/457", Title: "dev → staging", State: "open", Author: maria, HeadBranch: "dev", BaseBranch: "staging", HeadSHA: "d4e5f6a", StatusCheckRollup: checksPending, Comments: nil, ReviewRequests: []models.ReviewRequest{teamReq}}},
		{Repo: repos[3], PR: models.GhPr{Number: 89, URL: "https://github.com/example/workers/pull/89", Title: "chore: Update queue handler", State: "open", Author: maria, HeadBranch: "chore/queue", BaseBranch: "main", HeadSHA: "e5f6a7b", StatusCheckRollup: checksCI[:1], Comments: nil}},
	}
	for i := range entries {
		enrichAllPREntry(&entries[i])
	}
	return allOpenPRsFetchedResult{entries: entries, viewer: "lorenzo", teams: []string{"example/web"}}
}

// dryRunMergeCheck says what GitHub would about a fixture PR: a failing one is
// unstable, a draft is a draft, and the rest would merge by squash.
func dryRunMergeCheck(e allPREntry) github.MergeCheck {
	time.Sleep(dryRunNormal)
	check := github.MergeCheck{Status: "CLEAN", HeadSHA: e.PR.HeadSHA, Method: "SQUASH"}
	switch {
	case e.PR.IsDraft:
		check.Status = "DRAFT"
	case e.CIStatus == "failure":
		check.Status = "UNSTABLE"
	}
	return check
}

// dryRunFailedRuns is one failed run for each failing fixture check.
func dryRunFailedRuns(e allPREntry) []github.FailedRun {
	time.Sleep(dryRunNormal)
	var runs []github.FailedRun
	for i, c := range e.PR.StatusCheckRollup {
		if checkFailed(c) {
			runs = append(runs, github.FailedRun{ID: uint64(1000 + i), Name: c.WorkflowName})
		}
	}
	return runs
}

// --- shipped ----------------------------------------------------------------

func dryRunShipped() shippedFetchedResult {
	time.Sleep(dryRunLong)
	return shippedFetchedResult{entries: dryRunShippedEntries()}
}

// dryRunShippedEntries is three repos' releases: date tags several times a
// day, semver tags, and a rollback, which is billing's newest release.
func dryRunShippedEntries() []shippedEntry {
	repos := dryRunHomeRepos()
	rel := func(tag string, age time.Duration) github.Release {
		return github.Release{Tag: tag, SHA: "sha-" + tag, PublishedAt: timeNow().Add(-age), URL: "https://github.com/example/releases/tag/" + tag}
	}
	byRepo := map[string][]github.Release{
		repos[0].NWO: {rel("v2026.09.24.02", 2*time.Hour), rel("v2026.09.24.01", 26*time.Hour), rel("v2026.09.23.01", 50*time.Hour)},
		repos[1].NWO: {rel("v3.4.1", 5*time.Hour), rel("v3.4.0", 3*24*time.Hour)},
		repos[2].NWO: {rel("v1.9.0", 30*time.Minute), rel("v1.9.1", 6*time.Hour), rel("v1.8.0", 8*24*time.Hour)},
	}
	return shippedEntries(repos[:3], byRepo)
}

func dryRunReleaseDiff(tag string) github.ReleaseDiff {
	time.Sleep(dryRunNormal)
	return dryRunReleaseDiffFor(tag)
}

func dryRunReleaseDiffFor(tag string) github.ReleaseDiff {
	pr := func(n uint64, title string) *github.ShippedPR {
		return &github.ShippedPR{Number: n, Title: title, URL: fmt.Sprintf("https://github.com/example/pull/%d", n), HeadBranch: "feature"}
	}
	commit := func(headline string, p *github.ShippedPR) github.ShippedCommit {
		return github.ShippedCommit{SHA: headline, Headline: headline, PR: p}
	}
	switch tag {
	case "v2026.09.24.02":
		return github.ReleaseDiff{Status: "AHEAD", ShippedTotal: 4, Shipped: []github.ShippedCommit{
			commit("[ATT-7810] fix: keep the filter after a refresh (#298)", pr(298, "[ATT-7810] fix: keep the filter after a refresh")),
			commit("chore: update CHANGELOG [skip ci]", nil),
			commit("chore(deps): update playwright (#302)", pr(302, "chore(deps): update playwright")),
			commit("feat: show the team roster on the dashboard (ATT-7806) (#301)", pr(301, "feat: show the team roster on the dashboard (ATT-7806)")),
		}}
	case "v1.9.0":
		return github.ReleaseDiff{Status: "BEHIND", RemovedTotal: 2, Removed: []github.ShippedCommit{
			commit("ATT-7790 Charge in the customer's currency (#57)", pr(57, "ATT-7790 Charge in the customer's currency")),
			commit("Bump version to 1.9.1", nil),
		}}
	}
	return github.ReleaseDiff{Status: "AHEAD", ShippedTotal: 1, Shipped: []github.ShippedCommit{
		commit("ATT-7799 fix: retry the webhook once (#120)", pr(120, "ATT-7799 fix: retry the webhook once")),
	}}
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

// dryRunActionsJobs gives each run from dryRunActionsRuns jobs that match how
// it went: a running run has a step in progress, a failed one a failed step.
func dryRunActionsJobs(runID uint64) actionsJobsFetchedResult {
	time.Sleep(dryRunNormal)
	step := func(n int, name, status, conclusion string) models.WorkflowStep {
		return models.WorkflowStep{Name: name, Number: n, Status: status, Conclusion: conclusion}
	}
	done := func(n int, name string) models.WorkflowStep { return step(n, name, "completed", "success") }
	job := func(name, status, conclusion string, steps ...models.WorkflowStep) models.WorkflowJob {
		return models.WorkflowJob{Name: name, Status: status, Conclusion: conclusion, Steps: steps,
			URL: fmt.Sprintf("https://github.com/example/repo/actions/runs/%d", runID)}
	}
	passed := func(name string) models.WorkflowJob {
		return job(name, "completed", "success", done(1, "Checkout"), done(2, "Setup Node"), done(3, "Run"))
	}

	var jobs []models.WorkflowJob
	switch runID {
	case 1001, 3001: // running
		jobs = []models.WorkflowJob{
			passed("build"),
			job("test", "in_progress", "", done(1, "Checkout"), done(2, "Setup Node"),
				step(3, "Run tests", "in_progress", ""), step(4, "Upload coverage", "queued", "")),
			job("deploy", "queued", "", step(1, "Deploy to staging", "queued", "")),
		}
	case 2001: // failed
		jobs = []models.WorkflowJob{
			passed("build"),
			job("test", "completed", "failure", done(1, "Checkout"), done(2, "Setup Node"),
				step(3, "Run tests", "completed", "failure"), step(4, "Upload coverage", "completed", "skipped")),
			passed("lint"),
		}
	case 3002: // queued
		jobs = []models.WorkflowJob{job("deploy", "queued", "", step(1, "Deploy to staging", "queued", ""))}
	case 4001: // cancelled
		jobs = []models.WorkflowJob{job("build", "completed", "cancelled", done(1, "Checkout"),
			step(2, "Install deps", "completed", "cancelled"))}
	default:
		jobs = []models.WorkflowJob{passed("build"), passed("test"), passed("deploy")}
	}
	return actionsJobsFetchedResult{runID: runID, jobs: jobs}
}

// --- Dashboard --------------------------------------------------------------

// dryRunHomeData runs made-up repos, PRs and Actions runs through the real
// buildHomeData, so --dry-run exercises the same arithmetic as a live fetch.
// It covers every attention kind and every CI state.
func dryRunHomeData(flows []models.Flow) homeData {
	now := timeNow()
	repos := dryRunHomeRepos()
	passing, failing, running := dryRunChecks()
	pr := func(n uint64, f models.Flow, repo repoNWO, ci []models.CheckRun, state string, changes bool) models.GhPr {
		p := models.GhPr{Number: n, HeadBranch: f.HeadBranch(), BaseBranch: f.BaseBranch(repo.Repo.MainBranch),
			StatusCheckRollup: ci, Mergeable: "MERGEABLE", MergeStateStatus: state}
		if state == "DIRTY" {
			p.Mergeable = "CONFLICTING"
		}
		if changes {
			p.LatestReviews = []models.PrReview{{State: "CHANGES_REQUESTED"}}
		}
		return p
	}

	prs := map[string][]models.GhPr{}
	ahead := map[github.BranchPair]int{}
	pairOf := func(f models.Flow, r repoNWO) github.BranchPair {
		return github.BranchPair{NWO: r.NWO, Base: f.BaseBranch(r.Repo.MainBranch), Head: f.HeadBranch()}
	}
	for i, f := range flows {
		for _, r := range repos {
			ahead[pairOf(f, r)] = 0
		}
		switch i {
		case 0:
			for j, n := range []int{5, 3, 2, 14, 1, 0} {
				ahead[pairOf(f, repos[j])] = n
			}
			prs["acme/web-app"] = append(prs["acme/web-app"], pr(212, f, repos[0], failing, "UNSTABLE", false))
			prs["acme/api-service"] = append(prs["acme/api-service"], pr(88, f, repos[1], passing, "CLEAN", false))
			prs["acme/billing"] = append(prs["acme/billing"], pr(41, f, repos[2], running, "DIRTY", false))
			prs["acme/admin"] = append(prs["acme/admin"], pr(19, f, repos[4], passing, "CLEAN", false))
		case 1:
			ahead[pairOf(f, repos[0])] = 1
			ahead[pairOf(f, repos[1])] = 2
			prs["acme/api-service"] = append(prs["acme/api-service"], pr(90, f, repos[1], passing, "BLOCKED", true))
			prs["acme/web-app"] = append(prs["acme/web-app"], pr(215, f, repos[0], running, "BLOCKED", false))
		default:
			ahead[pairOf(f, repos[i%len(repos)])] = 4
		}
	}

	runs := dryRunHomeRuns(repos, now, [4]string{"main", "dev", "staging", "dev"})
	return buildHomeData(flows, repos, prs, ahead, runs, now)
}

// dryRunTrunkHomeData covers every trunk attention reason, ready PRs, drafts,
// and a stacked PR into a feature branch, which is not counted.
func dryRunTrunkHomeData() homeData {
	now := timeNow()
	repos := dryRunHomeRepos()
	passing, failing, running := dryRunChecks()
	pr := func(repo repoNWO, n uint64, title string, ci []models.CheckRun, state, review string) models.GhPr {
		p := models.GhPr{Number: n, Title: title, HeadBranch: "feature", BaseBranch: repo.Repo.MainBranch,
			StatusCheckRollup: ci, Mergeable: "MERGEABLE", MergeStateStatus: state, ReviewDecision: review}
		if state == "DIRTY" {
			p.Mergeable = "CONFLICTING"
		}
		return p
	}
	draft := func(repo repoNWO, n uint64, title string) models.GhPr {
		p := pr(repo, n, title, passing, "DRAFT", "")
		p.IsDraft = true
		return p
	}
	web, api, billing, worker, admin, docs := repos[0], repos[1], repos[2], repos[3], repos[4], repos[5]
	stacked := pr(api, 122, "Export: the download button", passing, "CLEAN", "")
	stacked.BaseBranch = "feature/export"
	prs := map[string][]models.GhPr{
		web.NWO: {pr(web, 301, "Add the onboarding checklist", failing, "UNSTABLE", "REVIEW_REQUIRED"),
			draft(web, 299, "Try the new nav")},
		api.NWO: {pr(api, 120, "Rate-limit the export endpoint", passing, "CLEAN", "APPROVED"),
			pr(api, 121, "Bump Go to 1.27", passing, "CLEAN", ""), stacked},
		billing.NWO: {pr(billing, 57, "Retry failed webhooks", running, "DIRTY", "CHANGES_REQUESTED")},
		worker.NWO:  {pr(worker, 12, "Batch the nightly job", running, "BLOCKED", "REVIEW_REQUIRED")},
		admin.NWO:   {pr(admin, 33, "Team settings page", passing, "BLOCKED", "REVIEW_REQUIRED")},
		docs.NWO:    {draft(docs, 8, "Rewrite the setup guide")},
	}
	runs := dryRunHomeRuns(repos, now, [4]string{"main", "feature/checklist", "main", "feature/export"})
	return buildTrunkHomeData(repos, prs, runs, now)
}

func dryRunHomeRepos() []repoNWO {
	names := []struct{ name, group, main string }{
		{"web-app", "Frontend", "main"}, {"api-service", "Backend", "main"}, {"billing", "Backend", "main"},
		{"worker", "Backend", "master"}, {"admin", "Frontend", "main"}, {"docs", "Frontend", "main"},
	}
	var repos []repoNWO
	for _, n := range names {
		repos = append(repos, repoNWO{Repo: models.NewRepoInfo("/demo/"+n.name, n.group+"/"+n.name, n.main, n.group), NWO: "acme/" + n.name})
	}
	return repos
}

func dryRunChecks() (passing, failing, running []models.CheckRun) {
	check := func(status, conclusion string) []models.CheckRun {
		return []models.CheckRun{{Name: "test", Status: status, Conclusion: conclusion}}
	}
	return check("COMPLETED", "SUCCESS"), check("COMPLETED", "FAILURE"), check("IN_PROGRESS", "")
}

// dryRunHomeRuns is the recent runs and a day of CI history, on the branches
// given for the four latest runs.
func dryRunHomeRuns(repos []repoNWO, now time.Time, branches [4]string) []actionsEntry {
	run := func(repo int, wf, branch, status, conclusion string, ago time.Duration) actionsEntry {
		return actionsEntry{Repo: repos[repo].Repo, Run: models.WorkflowRun{WorkflowName: wf, HeadBranch: branch,
			Status: status, Conclusion: conclusion, UpdatedAt: now.Add(-ago)}}
	}
	runs := []actionsEntry{
		run(0, "deploy-prod", branches[0], "completed", "success", 4*time.Minute),
		run(1, "ci", branches[1], "in_progress", "", 20*time.Second),
		run(2, "e2e", branches[2], "completed", "failure", time.Hour),
		run(3, "lint", branches[3], "completed", "success", 2*time.Hour),
	}
	// A day of history for the CI strip: busy in working hours, two failures.
	for h := 3; h < 24; h++ {
		for k := 0; k < (h*7)%5+1; k++ {
			conclusion := "success"
			if h == 9 && k == 0 || h == 17 && k == 0 {
				conclusion = "failure"
			}
			runs = append(runs, run((h+k)%len(repos), "ci", branches[1], "completed", conclusion, time.Duration(h)*time.Hour+time.Duration(k)*7*time.Minute))
		}
	}
	return runs
}
