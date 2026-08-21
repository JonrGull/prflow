package app

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/linear"
	"github.com/JonrGull/prflow/internal/models"
	"github.com/JonrGull/prflow/internal/update"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Golden renders of every screen.
//
// Written for the per-screen restructure, which moved every screen's rendering
// and state at once — clicking through them by hand is not a check. It earned
// its keep twice: it found a live crash on its first run (renderPullProgress
// dividing by zero with no repos), and it held every render byte-identical
// through the restructure itself.
//
// Regenerate after an intentional change:
//
//	go test ./internal/app -update
//
// then read the diff — an unexplained change in a screen you did not touch is the
// signal this is here to give you.

var updateGolden = flag.Bool("update", false, "rewrite golden files instead of comparing")

// fixedNow anchors every relative timestamp so renders are reproducible.
var fixedNow = time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

func TestMain(m *testing.M) {
	// lipgloss picks its colour profile from the terminal, so goldens recorded
	// under one TERM would not match another. Pin it.
	lipgloss.SetColorProfile(termenv.TrueColor)
	// Pin the clock and config-path seams for the whole package, so renders do
	// not vary by machine, $HOME, or operating system.
	timeNow = func() time.Time { return fixedNow }
	configPathFn = func() (string, error) { return "/home/testuser/.config/prflow.toml", nil }
	os.Exit(m.Run())
}

func ago(d time.Duration) time.Time { return fixedNow.Add(-d) }

func strptr(s string) *string { return &s }

func TestScreenRenders(t *testing.T) {
	for _, tc := range screenCases() {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.model
			m.width, m.height = 120, 40
			m.screen = tc.screen

			var b strings.Builder
			b.WriteString("=== normal ===\n")
			b.WriteString(m.View())
			b.WriteString("\n=== fullscreen ===\n")
			m.fullscreen = true
			b.WriteString(m.View())
			b.WriteString("\n")

			compareGolden(t, tc.name, b.String())
		})
	}
}

// heightAwareScreens are the screens that promise to fit the height they are
// handed. Everything else in this app renders whatever it has and lets the
// terminal clip: on a 24-row terminal 21 of the 54 fixtures overflow, the main
// menu among them, and that has been true since long before this branch.
//
// That is real debt, but fixing it is a layout redesign, not something to
// smuggle into a review response. So the list below is the contract as it
// actually stands — screens that have been made to fit, and are held to it.
// Moving a screen into this list is the way to pay the debt down one at a time.
var heightAwareScreens = map[Screen]bool{
	ScreenSettings: true,
	ScreenListEdit: true,
}

// Nothing may be wider than the terminal.
//
// Unlike height, this one holds for every screen: the overflow was all in the
// chrome. The banner was drawn at its natural 111 columns whatever the terminal
// was, and the status bar joined its hints into one unwrapped line — so before
// those were fixed all 54 fixtures overflowed at 80 and 100 columns, and the
// main menu still overflowed by 22 at 120. Horizontal overflow is worse than
// vertical: it does not just hide content, it tears boxes and ASCII art apart.
func TestScreensFitTheirWidth(t *testing.T) {
	for _, tc := range screenCases() {
		t.Run(tc.name, func(t *testing.T) {
			for _, w := range []int{60, 80, 100, 120, 160} {
				m := tc.model
				m.width, m.height = w, 40
				m.screen = tc.screen

				for i, line := range strings.Split(m.View(), "\n") {
					if got := lipgloss.Width(line); got > w {
						t.Errorf("width %d: line %d is %d wide, overflowing by %d",
							w, i+1, got, got-w)
						break // one report per width is enough to act on
					}
				}
			}
		})
	}
}

// A screen that promises to fit its height must actually fit it.
//
// The goldens record what a renderer produces, not whether it fits, so they were
// blind to two overflows in a row: the list editor drew 13 lines into 12 on a
// 25-line terminal, and the settings screen ignored the height entirely and drew
// 50 lines — 57 with diagnostics — needing a 50-row terminal before its last
// fields were visible at all. Overflow does not crash; the content just pushes
// the status bar off the bottom while the cursor keeps moving invisibly.
//
// minContentHeight is the floor View puts under this value, so it is the bottom
// of the range a screen can actually be given.
func TestScreensFitTheirHeight(t *testing.T) {
	seen := map[Screen]bool{}
	for _, tc := range screenCases() {
		if !heightAwareScreens[tc.screen] {
			continue
		}
		seen[tc.screen] = true
		t.Run(tc.name, func(t *testing.T) {
			// Asserted on the assembled View against the terminal height, not on
			// renderContentWithHeight against its budget. The latter passed while
			// the list editor still drew a row past the bottom of the terminal,
			// because the height charged for the outer box was a row short of
			// what the box takes — a gap only the whole view can show.
			//
			// From minTerminalHeight up. Below it the banner, tabs and status bar
			// already fill the terminal and minContentHeight forces a floor of
			// content on top, so overflow there is the documented behaviour
			// rather than a bug — fullscreen (F) is the answer on a tiny window.
			for _, h := range []int{minTerminalHeight, 30, 40, 60} {
				m := tc.model
				m.width, m.height = 120, h
				m.screen = tc.screen

				if got := lipgloss.Height(m.View()); got > h {
					t.Errorf("terminal height %d: rendered %d lines, overflowing by %d", h, got, got-h)
				}
			}
		})
	}

	// A screen listed as height-aware but never rendered here is untested, which
	// is the quiet way this contract would stop meaning anything.
	for s := range heightAwareScreens {
		if !seen[s] {
			t.Errorf("%v is listed as height-aware but has no case in screenCases()", s)
		}
	}
}

func compareGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "screens", name+".golden")

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0644); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden file %s (run: go test ./internal/app -update): %v", path, err)
	}
	if got == string(want) {
		return
	}

	// Report the first differing line rather than dumping two full screens.
	gotLines := strings.Split(got, "\n")
	wantLines := strings.Split(string(want), "\n")
	for i := 0; i < len(gotLines) || i < len(wantLines); i++ {
		g, w := lineAt(gotLines, i), lineAt(wantLines, i)
		if g != w {
			t.Errorf("%s differs at line %d\n got: %q\nwant: %q\n(%d lines vs %d; run -update to accept)",
				path, i+1, g, w, len(gotLines), len(wantLines))
			return
		}
	}
	t.Errorf("%s differs but no line mismatch found (trailing whitespace?)", path)
}

func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return "<missing>"
}

type screenCase struct {
	name   string
	screen Screen
	model  Model
}

// screenCases covers every screen, plus the empty-state variants separately.
// The empty cases matter most: that is where the pull-progress crash lived, and
// a golden suite that only renders populated screens would have missed it.
func screenCases() []screenCase {
	full := populatedModel()
	empty := emptyModel()

	cases := []screenCase{
		{"main_menu", ScreenMainMenu, full},
		{"pr_type_select", ScreenPrTypeSelect, full},
		{"loading", ScreenLoading, full},
		{"commit_review", ScreenCommitReview, full},
		{"title_input", ScreenTitleInput, full},
		{"confirmation", ScreenConfirmation, full},
		{"creating", ScreenCreating, full},
		{"complete", ScreenComplete, full},
		{"error", ScreenError, full},
		{"batch_repo_select", ScreenBatchRepoSelect, full},
		{"batch_confirmation", ScreenBatchConfirmation, full},
		{"batch_processing", ScreenBatchProcessing, full},
		{"batch_summary", ScreenBatchSummary, full},
		{"view_open_prs", ScreenViewOpenPrs, full},
		{"merge_confirmation", ScreenMergeConfirmation, full},
		{"merging", ScreenMerging, full},
		{"merge_summary", ScreenMergeSummary, full},
		{"update_prompt", ScreenUpdatePrompt, full},
		{"updating", ScreenUpdating, full},
		{"session_history", ScreenSessionHistory, full},
		{"pull_branch_select", ScreenPullBranchSelect, full},
		{"pull_progress", ScreenPullProgress, full},
		{"pull_summary", ScreenPullSummary, full},
		{"actions_overview", ScreenActionsOverview, full},
		{"view_all_prs", ScreenViewAllPrs, full},
		{"qa_tag_select", ScreenQaTagSelect, full},
		{"settings", ScreenSettings, full},

		// Empty states: distinct branches in the renderers.
		{"empty_commit_review", ScreenCommitReview, empty},
		{"empty_batch_repo_select", ScreenBatchRepoSelect, empty},
		{"empty_view_open_prs", ScreenViewOpenPrs, empty},
		{"empty_view_all_prs", ScreenViewAllPrs, empty},
		{"empty_actions_overview", ScreenActionsOverview, empty},
		{"empty_batch_summary", ScreenBatchSummary, empty},
		{"empty_session_history", ScreenSessionHistory, empty},
		// The crash case: progress bar with nothing to pull.
		{"empty_pull_progress", ScreenPullProgress, empty},
	}

	// Variants that exercise state the base fixtures do not reach.
	diag := populatedModel()
	diag.configDiagnostics = []config.Diagnostic{
		{Severity: config.SeverityError, Field: "paths.repos_dir", Message: `"/nope" does not exist`, Fix: "correct the path"},
		{Severity: config.SeverityWarning, Field: "columns", Message: `group "Typo" has no repos`},
	}
	cases = append(cases, screenCase{"settings_with_diagnostics", ScreenSettings, diag})

	editing := populatedModel()
	editing.settings.editing = true
	editing.settings.editValue = "some.person"
	cases = append(cases, screenCase{"settings_editing", ScreenSettings, editing})

	filtered := populatedModel()
	filtered.actions.filterActive = true
	filtered.actions.filter = "deploy"
	cases = append(cases, screenCase{"actions_filtering", ScreenActionsOverview, filtered})

	pinned := populatedModel()
	pinned.actions.column = 1
	cases = append(cases, screenCase{"actions_pinned_column", ScreenActionsOverview, pinned})

	// A three-step chain. Nothing else in the suite leaves the default pair, so
	// without these two the N-column layout and the derived step descriptions
	// are only ever exercised at the one width that used to be hardcoded.
	threeStep := populatedModel()
	threeStep.config = testConfig()
	threeStep.config.Flows = []config.FlowEntry{
		{Head: "dev", Base: "qa"},
		{Head: "qa", Base: "staging"},
		{Head: "staging", Base: models.DefaultBranchToken},
	}
	flows3 := threeStep.config.FlowEntries()
	firstOf3 := flows3[0]
	threeStep.flow = &firstOf3
	repos3 := testRepos()
	threeStep.merge.prs = []models.MergePrEntry{
		{Repo: repos3[0], PrNumber: 101, PrTitle: "dev → qa", URL: "https://github.com/acme/web-app/pull/101", Flow: flows3[0]},
		{Repo: repos3[1], PrNumber: 55, PrTitle: "qa → staging", URL: "https://github.com/acme/api/pull/55", Flow: flows3[1]},
		{Repo: repos3[2], PrNumber: 7, PrTitle: "日本語 リリース", URL: "https://github.com/acme/jp/pull/7", Flow: flows3[2]},
		{Repo: repos3[3], PrNumber: 8, PrTitle: "staging → main", URL: "https://github.com/acme/launcher/pull/8", Flow: flows3[2]},
	}
	threeStep.merge.selected = []bool{true, false, true, false}
	threeStep.merge.column = 1
	threeStep.merge.cursors = []int{0, 0, 1}
	threeStep.merge.repoErrors = nil
	cases = append(cases,
		screenCase{"pr_type_select_three_steps", ScreenPrTypeSelect, threeStep},
		screenCase{"view_open_prs_three_steps", ScreenViewOpenPrs, threeStep},
	)

	// A chain longer than the frame can hold. Six steps do not fit at any
	// terminal width the suite renders at, so the screen windows the columns
	// around the active step — this is the fixture that proves it still fits.
	longChain := populatedModel()
	longChain.config = testConfig()
	longChain.config.Flows = []config.FlowEntry{
		{Head: "feature", Base: "dev"},
		{Head: "dev", Base: "qa"},
		{Head: "qa", Base: "uat"},
		{Head: "uat", Base: "staging"},
		{Head: "staging", Base: "release"},
		{Head: "release", Base: models.DefaultBranchToken},
	}
	flows6 := longChain.config.FlowEntries()
	reposLong := testRepos()
	longChain.merge.prs = nil
	for i, f := range flows6 {
		longChain.merge.prs = append(longChain.merge.prs, models.MergePrEntry{
			Repo: reposLong[i%len(reposLong)], PrNumber: uint64(200 + i),
			URL: "https://github.com/acme/web-app/pull/200", Flow: f,
		})
	}
	longChain.merge.selected = make([]bool, len(longChain.merge.prs))
	longChain.merge.column = 3
	longChain.merge.cursors = make([]int, len(flows6))
	longChain.merge.repoErrors = nil
	cases = append(cases, screenCase{"view_open_prs_long_chain", ScreenViewOpenPrs, longChain})

	dry := populatedModel()
	dry.dryRun = true
	cases = append(cases, screenCase{"main_menu_dry_run", ScreenMainMenu, dry})

	// The batch selector's second column carries its own highlight and commits
	// panel; with the cursor in column 0 that path never renders.
	rightCol := populatedModel()
	rightCol.batch.column = 1
	rightCol.batch.beIndex = 1
	cases = append(cases, screenCase{"batch_repo_select_right_column", ScreenBatchRepoSelect, rightCol})

	// Filtering changes the list and suppresses group/parent headers.
	filteredRepos := populatedModel()
	filteredRepos.batch.filter = "a"
	cases = append(cases, screenCase{"batch_repo_select_filtered", ScreenBatchRepoSelect, filteredRepos})

	// Menu index 1 is Batch Mode, whose info panel names the scan directory —
	// the line that used to be a hardcoded one company's repo directory regardless
	// of what the user had configured.
	batchMenu := populatedModel()
	batchMenu.menuIndex = 1
	cases = append(cases, screenCase{"main_menu_batch_info", ScreenMainMenu, batchMenu})

	// Menu index 2 draws the release chain. Nothing rendered that panel before,
	// which is how it went on showing a hand-drawn dev/staging/main ladder
	// regardless of the configured steps.
	releaseMenu := populatedModel()
	releaseMenu.menuIndex = 2
	cases = append(cases, screenCase{"main_menu_release_info", ScreenMainMenu, releaseMenu})

	releaseMenu3 := threeStep
	releaseMenu3.menuIndex = 2
	cases = append(cases,
		screenCase{"main_menu_release_info_three_steps", ScreenMainMenu, releaseMenu3},
		screenCase{"pull_branch_select_three_steps", ScreenPullBranchSelect, releaseMenu3},
	)

	// First-run setup has three distinct states, and the two failure ones are
	// the reason the screen exists at all.
	frFresh := populatedModel()
	frFresh.firstRun.value = "~/Programming/example"
	cases = append(cases, screenCase{"first_run", ScreenFirstRun, frFresh})

	frFound := populatedModel()
	frFound.firstRun.value = "/tmp/prflow-golden"
	frFound.firstRun.preview = firstRunPreview{
		Path: "/tmp/prflow-golden", Ran: true, Repos: testRepos(),
	}
	cases = append(cases, screenCase{"first_run_found", ScreenFirstRun, frFound})

	frEmpty := populatedModel()
	frEmpty.firstRun.value = "/tmp/nothing-here"
	frEmpty.firstRun.preview = firstRunPreview{Path: "/tmp/nothing-here", Ran: true}
	cases = append(cases, screenCase{"first_run_empty", ScreenFirstRun, frEmpty})

	// The list editor renders one screen for four differently-shaped lists, so
	// record a two-cell list, a one-cell list, the empty state, mid-edit, and
	// the armed delete — the states that differ in the renderer.
	listGlob := populatedModel()
	listGlob.openListEditor(listGlobs)
	listGlob.list.cell = 1
	cases = append(cases, screenCase{"list_edit_globs", ScreenListEdit, listGlob})

	// The three-cell list. Everything else in the editor has one or two, so
	// this is what holds the shared row layout to a width that still fits.
	listFlow := populatedModel()
	listFlow.openListEditor(listFlows)
	listFlow.list.row, listFlow.list.cell = 1, 2
	cases = append(cases, screenCase{"list_edit_flows", ScreenListEdit, listFlow})

	listCol := populatedModel()
	listCol.openListEditor(listColumnsLeft)
	cases = append(cases, screenCase{"list_edit_columns", ScreenListEdit, listCol})

	listEmpty := populatedModel()
	listEmpty.config.Globs = nil
	listEmpty.openListEditor(listGlobs)
	cases = append(cases, screenCase{"list_edit_empty", ScreenListEdit, listEmpty})

	listEditing := populatedModel()
	listEditing.openListEditor(listGlobs)
	listEditing.list.row, listEditing.list.cell = 1, 1
	listEditing.list.editing = true
	listEditing.list.editValue = "Services"
	cases = append(cases, screenCase{"list_edit_editing", ScreenListEdit, listEditing})

	listDelete := populatedModel()
	listDelete.openListEditor(listGlobs)
	listDelete.list.pendingDelete = true
	listDelete.list.feedback = `Press d again to delete "frontend/*"`
	cases = append(cases, screenCase{"list_edit_pending_delete", ScreenListEdit, listDelete})

	// A list longer than the screen: the rows past the edge have to stay
	// reachable, or the editor would be worse than the file it replaces.
	listLong := populatedModel()
	for i := 0; i < 30; i++ {
		listLong.config.Repos = append(listLong.config.Repos,
			config.RepoEntry{Path: fmt.Sprintf("~/Projects/service-%02d", i), Group: "Services"})
	}
	listLong.openListEditor(listRepos)
	listLong.list.row = 24
	cases = append(cases, screenCase{"list_edit_scrolled", ScreenListEdit, listLong})

	// The help overlay draws over whatever screen is behind it, so record it
	// over both a boxed screen and a full-layout one.
	helpMenu := populatedModel()
	helpMenu.showHelp = true
	cases = append(cases, screenCase{"help_over_main_menu", ScreenMainMenu, helpMenu})

	helpActions := populatedModel()
	helpActions.showHelp = true
	cases = append(cases, screenCase{"help_over_actions", ScreenActionsOverview, helpActions})

	return cases
}

func testConfig() *config.Config {
	cfg := config.DefaultConfig()
	cfg.Paths.ReposDir = "/tmp/prflow-golden"
	return cfg
}

// repos deliberately include CJK and emoji names. Those are what exercise the
// display-width arithmetic in visPad/visRightAlign and ColumnBox — the code most
// sensitive to a go-runewidth or x/ansi change.
func testRepos() []models.RepoInfo {
	return []models.RepoInfo{
		models.NewRepoInfo("/r/web-app", "Frontend/web-app", "main", "Frontend"),
		models.NewRepoInfo("/r/api", "Backend/api-service", "main", "Backend"),
		models.NewRepoInfo("/r/jp", "Backend/日本語-サービス", "master", "Backend"),
		models.NewRepoInfo("/r/emoji", "Frontend/🚀-launcher", "main", "Frontend"),
	}
}

func emptyModel() Model {
	return Model{
		config:  testConfig(),
		version: "v1.2.3",
		ghUser:  "someone",
	}
}

func populatedModel() Model {
	repos := testRepos()
	flows := testConfig().FlowEntries()
	firstFlow := flows[0]
	repo := repos[0]

	commits := []models.CommitInfo{
		models.NewCommitInfo("abc1234", "ATT-1234 Fix the ⚠ redirect", []string{"ATT-1234"}),
		models.NewCommitInfo("def5678", "日本語のコミットメッセージ 🚀", nil),
		models.NewCommitInfo("aaa9999", "ATT-5678 Tidy up", []string{"ATT-5678"}),
	}

	m := Model{
		config:       testConfig(),
		version:      "v1.2.3",
		ghUser:       "someone",
		spinnerFrame: 3,
		pulsePhase:   1.25,

		repoInfo: &repo,
		flow:     &firstFlow,
		commits:  commits,
		tickets:  []string{"ATT-1234", "ATT-5678"},
		prTitle:  "Sprint # 42 — release ✦",
		prURL:    "https://github.com/acme/web-app/pull/101",

		errorMessage:     "gh pr create failed: authentication required",
		loadingMessage:   "Fetching commits...",
		confirmSelection: 0,
		typewriterPos:    100,
	}

	// Batch
	m.batch.repos = repos
	m.batch.selected = []bool{true, false, true, true}
	m.batch.repoCommits = make([]*[]models.CommitInfo, len(repos))
	for i := range repos {
		c := commits
		if i == 1 {
			c = nil // a repo with nothing to merge
		}
		cc := c
		m.batch.repoCommits[i] = &cc
	}
	m.batch.total = len(repos)
	m.batch.current = 2
	m.batch.currentRepo = "Backend/日本語-サービス"
	m.batch.currentStep = "Fetching branches..."
	m.batch.existingPRs = 1
	m.batch.reposWithCommits = 3
	m.batch.results = []models.BatchResult{
		{Repo: repos[0], Status: models.Created, PrURL: strptr("https://github.com/acme/web-app/pull/101"), PrNumber: 101, Tickets: []string{"ATT-1234"}},
		{Repo: repos[1], Status: models.Updated, PrURL: strptr("https://github.com/acme/api/pull/55"), PrNumber: 55},
		{Repo: repos[2], Status: models.Skipped("no commits"), PrNumber: 0},
		{Repo: repos[3], Status: models.Failed("branch not found on remote"), PrNumber: 0},
	}

	// Open PRs / merge
	m.merge.prs = []models.MergePrEntry{
		{Repo: repos[0], PrNumber: 101, PrTitle: "dev → staging", URL: "https://github.com/acme/web-app/pull/101", Flow: flows[0]},
		{Repo: repos[1], PrNumber: 55, PrTitle: "staging → main", URL: "https://github.com/acme/api/pull/55", Flow: flows[1]},
		{Repo: repos[2], PrNumber: 7, PrTitle: "日本語 リリース", URL: "https://github.com/acme/jp/pull/7", Flow: flows[0]},
	}
	m.merge.selected = []bool{true, false, true}
	m.merge.total = 3
	m.merge.current = 1
	m.merge.results = []models.MergeResult{
		{RepoName: "Frontend/web-app", PrNumber: 101, PrTitle: "dev → staging", Success: true, URL: "https://github.com/acme/web-app/pull/101"},
		{RepoName: "Backend/api-service", PrNumber: 55, PrTitle: "staging → main", Success: false, Error: strptr("merge conflict"), URL: "https://github.com/acme/api/pull/55"},
	}
	m.merge.repoErrors = []string{"Backend/broken-remote: no origin configured"}

	// Session history
	m.sessionPRs = []sessionPR{
		{repoName: "Frontend/web-app", url: "https://github.com/acme/web-app/pull/101", prType: "dev → staging", action: "created", prNumber: 101, createdAt: ago(12 * time.Minute)},
		{repoName: "Backend/日本語-サービス", url: "https://github.com/acme/jp/pull/7", prType: "staging → main", action: "merged", prNumber: 7, createdAt: ago(3 * time.Hour)},
	}

	// Pull
	m.pull.branch = "staging"
	m.pull.repos = repos
	m.pull.currentIdx = 2
	m.pull.results = []models.PullResult{
		{Repo: repos[0], Status: models.PullUpdated, CommitCount: 4},
		{Repo: repos[1], Status: models.PullUpToDate},
		{Repo: repos[2], Status: models.PullSkippedDirty},
		{Repo: repos[3], Status: models.PullFailed, Error: "network unreachable"},
	}

	// Update prompt
	m.updateAvailable = &update.Release{TagName: "v1.3.0"}
	m.updateSelection = 0

	// QA tagging
	m.qa.selected = []bool{true, false}
	m.qa.titles = map[string]string{"ATT-1234": "Fix login redirect", "ATT-5678": "日本語 のタイトル"}
	m.qa.results = []linear.QaTagResult{
		{Ticket: "ATT-1234", Success: true},
		{Ticket: "ATT-5678", Success: false, Error: "issue not found"},
	}

	// Actions
	m.actions.lastRefresh = ago(2 * time.Second)
	m.actions.autoRefresh = true
	m.actions.entries = []actionsEntry{
		{Repo: repos[0], Run: models.WorkflowRun{DatabaseID: 900, DisplayTitle: "Deploy preview", WorkflowName: "deploy-preview", Status: "in_progress", HeadBranch: "dev", Event: "push", UpdatedAt: ago(90 * time.Second)}},
		{Repo: repos[1], Run: models.WorkflowRun{DatabaseID: 901, DisplayTitle: "CI 日本語", WorkflowName: "ci", Status: "completed", Conclusion: "failure", HeadBranch: "staging", Event: "pull_request", UpdatedAt: ago(30 * time.Minute)}},
		{Repo: repos[2], Run: models.WorkflowRun{DatabaseID: 902, DisplayTitle: "E2E 🚀", WorkflowName: "e2e", Status: "completed", Conclusion: "success", HeadBranch: "main", Event: "schedule", UpdatedAt: ago(26 * time.Hour)}},
	}
	m.actions.pinned = []actionsPanel{{
		Run:  m.actions.entries[1].Run,
		Repo: repos[1],
		Jobs: []models.WorkflowJob{{
			Name: "build", Status: "completed", Conclusion: "failure",
			Steps: []models.WorkflowStep{
				{Name: "checkout", Number: 1, Status: "completed", Conclusion: "success"},
				{Name: "test", Number: 2, Status: "completed", Conclusion: "failure"},
			},
		}},
	}}

	// All open PRs
	m.allPRs.entries = []allPREntry{
		makeAllPREntry(repos[0], 101, "Add the ⚠ banner", "dev", "success", "success", "current", 12, 12, 0),
		makeAllPREntry(repos[2], 7, "日本語 のプルリクエスト", "staging", "failure", "pending", "unresponded", 3, 10, 7),
	}
	m.allPRs.autoRefresh = true

	return m
}

func makeAllPREntry(repo models.RepoInfo, num uint64, title, branch, ci, preview, review string, passed, total, failed int) allPREntry {
	var pr models.GhPr
	pr.Number = num
	pr.Title = title
	pr.HeadBranch = branch
	pr.URL = fmt.Sprintf("https://github.com/acme/x/pull/%d", num)
	pr.Author.Login = "someone"
	return allPREntry{
		Repo: repo, PR: pr,
		CommentCount: 4, UnrespondedCount: 1,
		CIStatus: ci, PreviewStatus: preview, ReviewStatus: review,
		E2EPassed: passed, E2ETotal: total, E2EFailed: failed,
	}
}
