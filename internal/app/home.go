package app

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/github"
	"github.com/JonrGull/prflow/internal/models"

	tea "github.com/charmbracelet/bubbletea"
)

// The dashboard's data: what the home screen shows about the release, fetched
// in the background. Rendering and keys are in mainmenu.go.
//
// Three requests cover every repo: an open-PR search with the fields the
// cards need, one GraphQL branch comparison for the pipeline, and each repo's
// Actions runs from the last day. buildHomeData turns them into panels and is
// pure, so tests and --dry-run drive it without the network.

// homeStaleAfter is how old the data may get before arriving home refreshes it.
const homeStaleAfter = 5 * time.Minute

// homeMergeStateRetry is the wait before asking again for unknown merge states.
const homeMergeStateRetry = 2 * time.Second

// homeState is the dashboard's cache. reset() leaves it alone: it outlives
// every flow, and is only replaced by a newer fetch.
type homeState struct {
	data      homeData
	loaded    bool      // data holds a completed fetch
	loading   bool      // a fetch is out
	err       error     // the last fetch failed outright
	fetchedAt time.Time // when the last fetch finished
	gen       int       // the fetch whose result is wanted; older ones are ignored
}

type homeData struct {
	Steps     []homeStep
	Attention []attentionItem
	Runs      []actionsEntry // newest first
	CI        [24]ciHour     // the last 24 hours, oldest first
	CIGreen   int            // percent of finished runs that passed, or -1 for none
	Problems  []string       // parts that failed, shown rather than hidden
	RunErrors int            // repos whose Actions runs could not be read
	Repos     int            // repos with a GitHub remote that were checked

	// Trunk-based: every open PR into each repo's default branch, in place of
	// the release steps.
	Trunk       bool
	IntoDefault homeStep
}

// homeStep is one configured release step, across every repo.
type homeStep struct {
	Flow       models.Flow
	Ahead      int // commits head has that base lacks, summed over repos
	ReposAhead int
	Compared   int // repos where both branches exist
	Open       int // open release PRs
	Green      int
	Failing    int
	Pending    int
	Ready      []string // repos whose release PR could be merged now; repo#N when trunk-based

	// Trunk-based only.
	Drafts           int
	AwaitingReview   int
	ChangesRequested int
}

type attentionKind int

// In the order they are listed: the first ones block a release.
const (
	attnCIFailing attentionKind = iota
	attnConflict
	attnChangesRequested
	attnReviewRequired
	attnNoPR
)

type attentionItem struct {
	Kind   attentionKind
	Repo   string
	PR     uint64 // 0 when there is no PR
	Detail string
	Step   string // the release step, or the PR's title when trunk-based
}

type ciHour struct{ Runs, Failed int }

// homeFetchedResult is deliberately not a flowResult: it only replaces the
// cache and never moves the screen, so a late one is harmless, and dropping it
// with the epoch would leave the dashboard loading until the next refresh.
// gen drops the result of a fetch that a newer one replaced.
type homeFetchedResult struct {
	gen  int
	data homeData
	err  error
	at   time.Time
}

// startHomeFetch fetches the dashboard unless a fetch is already out. It
// changes m, so call it as its own statement.
func (m *Model) startHomeFetch() tea.Cmd {
	if m.home.loading {
		return nil
	}
	m.home.gen++
	m.home.loading = true
	return fetchHomeCmd(m.config, m.flows(), m.trunkBased(), m.dryRun, m.home.gen)
}

// homeStale reports whether the dashboard should fetch on arrival: it has no
// data, the data is old, or the last fetch failed.
func (m Model) homeStale() bool {
	return !m.home.loading && (m.home.fetchedAt.IsZero() || m.home.err != nil ||
		timeNow().Sub(m.home.fetchedAt) > homeStaleAfter)
}

func (m Model) handleHomeFetched(msg homeFetchedResult) (tea.Model, tea.Cmd) {
	if msg.gen != m.home.gen {
		return m, nil
	}
	m.home.loading = false
	m.home.fetchedAt = msg.at
	m.home.err = msg.err
	if msg.err == nil {
		m.home.data = msg.data
		m.home.loaded = true
	}
	return m, nil
}

func fetchHomeCmd(cfg *config.Config, flows []models.Flow, trunk, dryRun bool, gen int) tea.Cmd {
	return func() tea.Msg {
		if dryRun {
			time.Sleep(dryRunLong)
			if trunk {
				return homeFetchedResult{gen: gen, data: dryRunTrunkHomeData(), at: timeNow()}
			}
			return homeFetchedResult{gen: gen, data: dryRunHomeData(flows), at: timeNow()}
		}

		repos, err := discoverRepos(cfg)
		if err != nil {
			return homeFetchedResult{gen: gen, err: err, at: timeNow()}
		}
		withNWO := githubRepos(repos)
		nwos := make([]string, len(withNWO))
		var pairs []github.BranchPair
		for i, r := range withNWO {
			nwos[i] = r.NWO
			for _, f := range flows {
				pairs = append(pairs, github.BranchPair{NWO: r.NWO, Base: f.BaseBranch(r.Repo.MainBranch), Head: f.HeadBranch()})
			}
		}

		// The three requests are independent, so they run at once.
		var (
			wg       sync.WaitGroup
			prs      map[string][]models.GhPr
			prErr    error
			stateErr error
			ahead    map[github.BranchPair]int
			cmpErr   error
			runs     []actionsEntry
			runErrs  int
		)
		wg.Add(3)
		go func() {
			defer wg.Done()
			prs, prErr = github.SearchOpenPRsForDashboard(nwos)
			if prErr != nil {
				return
			}
			if trunk {
				stateErr = fillMergeStates(defaultBranchPRs(withNWO, prs))
			} else {
				stateErr = fillMergeStates(releasePRs(flows, withNWO, prs))
			}
		}()
		go func() { defer wg.Done(); ahead, cmpErr = github.CompareBranches(pairs) }()
		go func() {
			defer wg.Done()
			type res struct {
				repo models.RepoInfo
				runs []models.WorkflowRun
				err  error
			}
			for _, r := range parallelMap(withNWO, func(r repoNWO) res {
				rs, err := github.ListWorkflowRunsSince(r.NWO, timeNow().Add(-24*time.Hour))
				return res{repo: r.Repo, runs: rs, err: err}
			}) {
				if r.err != nil {
					runErrs++
					continue
				}
				for _, run := range r.runs {
					runs = append(runs, actionsEntry{Repo: r.repo, Run: run})
				}
			}
		}()
		wg.Wait()

		data := buildHomeData(flows, withNWO, prs, ahead, runs, timeNow())
		if trunk {
			data = buildTrunkHomeData(withNWO, prs, runs, timeNow())
		}
		if prErr != nil {
			data.Problems = append(data.Problems, "open PRs: "+prErr.Error())
		}
		// Only conflicts and readiness depend on it, so the cards still show.
		if stateErr != nil {
			data.Problems = append(data.Problems, stateErr.Error())
		}
		if cmpErr != nil {
			data.Problems = append(data.Problems, "branch comparison: "+cmpErr.Error())
		}
		if runErrs > 0 {
			data.RunErrors = runErrs
			data.Problems = append(data.Problems, fmt.Sprintf("Actions: %d repo(s) could not be read", runErrs))
		}
		return homeFetchedResult{gen: gen, data: data, at: timeNow()}
	}
}

// buildHomeData turns the raw requests into the dashboard's panels.
func buildHomeData(flows []models.Flow, repos []repoNWO, prs map[string][]models.GhPr, ahead map[github.BranchPair]int, runs []actionsEntry, now time.Time) homeData {
	d := homeData{Repos: len(repos), CIGreen: -1}

	for _, f := range flows {
		step := homeStep{Flow: f}
		for _, r := range repos {
			base := f.BaseBranch(r.Repo.MainBranch)
			pair := github.BranchPair{NWO: r.NWO, Base: base, Head: f.HeadBranch()}
			n, compared := ahead[pair]
			if compared {
				step.Compared++
				step.Ahead += n
				if n > 0 {
					step.ReposAhead++
				}
			}

			pr := releasePR(prs[r.NWO], f.HeadBranch(), base)
			if pr == nil {
				if n > 0 {
					d.Attention = append(d.Attention, attentionItem{Kind: attnNoPR, Repo: r.Repo.ShortName(),
						Detail: fmt.Sprintf("%d commit%s, no PR", n, pluralS(n)), Step: f.Display(r.Repo.MainBranch)})
				}
				continue
			}
			step.Open++
			ci := computeCIStatus(pr.StatusCheckRollup)
			changes := changesRequested(*pr)
			switch ci {
			case "failure":
				step.Failing++
				d.Attention = append(d.Attention, attentionItem{Kind: attnCIFailing, Repo: r.Repo.ShortName(),
					PR: pr.Number, Detail: "CI failing", Step: f.Display(r.Repo.MainBranch)})
			case "pending":
				step.Pending++
			default:
				step.Green++
			}
			if pr.Mergeable == "CONFLICTING" {
				d.Attention = append(d.Attention, attentionItem{Kind: attnConflict, Repo: r.Repo.ShortName(),
					PR: pr.Number, Detail: "merge conflict", Step: f.Display(r.Repo.MainBranch)})
			}
			if changes {
				d.Attention = append(d.Attention, attentionItem{Kind: attnChangesRequested, Repo: r.Repo.ShortName(),
					PR: pr.Number, Detail: "changes requested", Step: f.Display(r.Repo.MainBranch)})
			}
			if (ci == "success" || ci == "none") && readyMergeStates[pr.MergeStateStatus] && !changes && !pr.IsDraft {
				step.Ready = append(step.Ready, r.Repo.ShortName())
			}
		}
		d.Steps = append(d.Steps, step)
	}
	sortAttention(d.Attention)
	addCIHistory(&d, runs, now)
	return d
}

// buildTrunkHomeData is buildHomeData for trunk-based work: the open PRs into
// each repo's default branch, forks included; a draft is counted, not flagged.
func buildTrunkHomeData(repos []repoNWO, prs map[string][]models.GhPr, runs []actionsEntry, now time.Time) homeData {
	d := homeData{Repos: len(repos), CIGreen: -1, Trunk: true}
	st := &d.IntoDefault
	for _, r := range repos {
		for _, pr := range prs[r.NWO] {
			if pr.BaseBranch != r.Repo.MainBranch {
				continue
			}
			if pr.IsDraft {
				st.Drafts++
				continue
			}
			st.Open++
			ci := computeCIStatus(pr.StatusCheckRollup)
			switch ci {
			case "failure":
				st.Failing++
			case "pending":
				st.Pending++
			default:
				st.Green++
			}
			changes := changesRequested(pr) || pr.ReviewDecision == "CHANGES_REQUESTED"
			review := pr.ReviewDecision == "REVIEW_REQUIRED"
			if changes {
				st.ChangesRequested++
			}
			if review {
				st.AwaitingReview++
			}

			// One row per PR, with every reason, filed under the most blocking.
			var kinds []attentionKind
			var reasons []string
			for _, why := range []struct {
				on     bool
				kind   attentionKind
				reason string
			}{
				{ci == "failure", attnCIFailing, "CI failing"},
				{pr.Mergeable == "CONFLICTING", attnConflict, "conflict"},
				{changes, attnChangesRequested, "changes requested"},
				{review, attnReviewRequired, "review required"},
			} {
				if why.on {
					kinds = append(kinds, why.kind)
					reasons = append(reasons, why.reason)
				}
			}
			if len(reasons) > 0 {
				d.Attention = append(d.Attention, attentionItem{Kind: kinds[0], Repo: r.Repo.ShortName(),
					PR: pr.Number, Detail: strings.Join(reasons, ", "), Step: pr.Title})
			}
			if (ci == "success" || ci == "none") && readyMergeStates[pr.MergeStateStatus] && !changes && !review {
				st.Ready = append(st.Ready, fmt.Sprintf("%s#%d", r.Repo.ShortName(), pr.Number))
			}
		}
	}
	sortAttention(d.Attention)
	addCIHistory(&d, runs, now)
	return d
}

// sortAttention puts the most blocking first, then by repo and PR.
func sortAttention(items []attentionItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		if items[i].Repo != items[j].Repo {
			return items[i].Repo < items[j].Repo
		}
		return items[i].PR < items[j].PR
	})
}

// addCIHistory fills the recent runs, the day's hourly CI strip and its share
// of green.
func addCIHistory(d *homeData, runs []actionsEntry, now time.Time) {
	d.Runs = append([]actionsEntry(nil), runs...)
	sort.SliceStable(d.Runs, func(i, j int) bool { return d.Runs[i].Run.UpdatedAt.After(d.Runs[j].Run.UpdatedAt) })
	passed, failed := 0, 0
	for _, e := range d.Runs {
		age := now.Sub(e.Run.UpdatedAt)
		if age < 0 || age >= 24*time.Hour {
			continue
		}
		hour := &d.CI[23-int(age/time.Hour)]
		hour.Runs++
		if e.Run.Status != "completed" {
			continue
		}
		if runFailed(e.Run.Conclusion) {
			hour.Failed++
			failed++
		} else if strings.EqualFold(e.Run.Conclusion, "success") {
			passed++
		}
	}
	if passed+failed > 0 {
		d.CIGreen = passed * 100 / (passed + failed)
	}
}

// readyMergeStates are the states of a PR GitHub would merge now. mergeable only
// rules out conflicts, so a PR awaiting a required review or e2e passed it.
var readyMergeStates = map[string]bool{"CLEAN": true, "HAS_HOOKS": true}

// defaultBranchPRs are the open PRs into each repo's default branch, by
// reference, drafts left out: they are not asked about.
func defaultBranchPRs(repos []repoNWO, prs map[string][]models.GhPr) map[github.PRRef]*models.GhPr {
	out := map[github.PRRef]*models.GhPr{}
	for _, r := range repos {
		list := prs[r.NWO]
		for i := range list {
			if list[i].BaseBranch == r.Repo.MainBranch && !list[i].IsDraft {
				out[github.PRRef{NWO: r.NWO, Number: list[i].Number}] = &list[i]
			}
		}
	}
	return out
}

// releasePRs are every step's open release PR in every repo, by reference.
func releasePRs(flows []models.Flow, repos []repoNWO, prs map[string][]models.GhPr) map[github.PRRef]*models.GhPr {
	out := map[github.PRRef]*models.GhPr{}
	for _, f := range flows {
		for _, r := range repos {
			if pr := releasePR(prs[r.NWO], f.HeadBranch(), f.BaseBranch(r.Repo.MainBranch)); pr != nil {
				out[github.PRRef{NWO: r.NWO, Number: pr.Number}] = pr
			}
		}
	}
	return out
}

// fillMergeStates sets each PR's merge state. GitHub answers UNKNOWN until the
// first request has it worked out, so those are asked once more.
func fillMergeStates(prs map[github.PRRef]*models.GhPr) error {
	refs := make([]github.PRRef, 0, len(prs))
	for ref := range prs {
		refs = append(refs, ref)
	}
	states, err := github.MergeStates(refs)
	var unknown []github.PRRef
	for ref, s := range states {
		if s.Status == "UNKNOWN" {
			unknown = append(unknown, ref)
		}
	}
	if len(unknown) > 0 {
		time.Sleep(homeMergeStateRetry)
		again, _ := github.MergeStates(unknown)
		for ref, s := range again {
			states[ref] = s
		}
	}
	for ref, s := range states {
		prs[ref].Mergeable, prs[ref].MergeStateStatus = s.Mergeable, s.Status
	}
	if err != nil {
		return fmt.Errorf("merge states: %w", err)
	}
	return nil
}

// releasePR is this repo's open release PR for a step, if it has one. Fork
// PRs are not it, whatever their branch names.
func releasePR(prs []models.GhPr, head, base string) *models.GhPr {
	for i := range prs {
		if prs[i].HeadBranch == head && prs[i].BaseBranch == base && !prs[i].IsCrossRepository {
			return &prs[i]
		}
	}
	return nil
}

// changesRequested reports whether any reviewer's latest review asks for
// changes.
func changesRequested(pr models.GhPr) bool {
	for _, r := range pr.LatestReviews {
		if r.State == "CHANGES_REQUESTED" {
			return true
		}
	}
	return false
}

// runFailed matches the REST API's lower-case conclusions.
func runFailed(conclusion string) bool {
	switch strings.ToLower(conclusion) {
	case "failure", "timed_out", "startup_failure", "action_required":
		return true
	}
	return false
}

// pluralS is "s" unless n is 1.
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
