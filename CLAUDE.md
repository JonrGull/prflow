# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Go TUI application for creating GitHub release PRs across multiple repositories. Uses bubbletea (Elm-architecture TUI framework) and lipgloss for styling. Requires `gh` CLI to be installed and authenticated.

## Build Commands

```bash
go build -o ~/.local/bin/prflow ./cmd/prflow   # Build and install
go build ./cmd/prflow                         # Dev build
go run ./cmd/prflow --dry-run                 # Test without GitHub access
go vet ./...                                 # Check for common issues
go test ./...                                # Run the test suite (~0.2s)
go test ./internal/app -update               # Re-record screen goldens after an intentional UI change
```

**After making changes:** Always run `go build -o ~/.local/bin/prflow ./cmd/prflow` to install the updated binary so the user can use it outside the repo.

## Architecture

**Bubbletea Pattern:** Model holds all state, Update handles messages/input, View renders, Cmd returns async operations as messages.

- `cmd/prflow/main.go` - Entry point, cobra CLI (`--dry-run` flag)
- `internal/app/` - State machine. **Each screen owns a file** holding its
  state struct, its commands, its key handler and its rendering. The shared
  shell is only three files:
  - `app.go` - The Model, `New`, and the animation tick
  - `update.go` - The message loop: global keys, then dispatch to the screen
  - `view.go` - The frame every screen draws inside, plus shared render helpers
  - Screens: `mainmenu.go` (the Home dashboard), `single.go` (release step → review → title → create),
    `batch.go`, `merge.go` (open PRs + merging), `allprs.go`, `actions.go`,
    `pull.go`, `qatag.go`, `settings.go`, `shipped.go`, `listedit.go`, `firstrun.go`,
    `history.go`, `selfupdate.go`, `errorscreen.go`
  - `screens.go` - Screen enum (30 screens) and AppMode (Single/Batch)
  - `keys.go` - Per-screen key hints as data (footer + `?` overlay)
  - `home.go` - The dashboard's data: one fetch, three requests, a pure `buildHomeData`
  - `prstatus.go` - The derived review/CI/preview rules for the all-PRs table
  - `practions.go` - The all-PRs table's merge, re-run and worktree actions
  - `flows.go` - The configured release steps, and the colour palettes every
    screen indexes them by
  - `dryrun.go` - The fake data `--dry-run` returns, and its delays
  - `external.go` - Browser, clipboard, file manager, `gh auth` check
  - `confetti.go` - Shared by the single and batch completion screens
  - `links.go` - PR link building for the open-all / copy-as-markdown actions
  - `repos.go` - Memoised repo discovery + generic `parallelMap`
- `internal/ui/` - Styling and reusable components
- `internal/models/` - Data types (RepoInfo, CommitInfo, BatchResult, etc.)
- `internal/config/` - TOML config (`~/.config/prflow.toml`, `~/Library/Application Support/prflow.toml` on macOS)
- `internal/git/` - go-git for repo/commit reading, CLI for fetch (inherits SSH agent)
- `internal/github/` - Wraps `gh` CLI for PR operations
- `internal/run/` - Runs external commands with a deadline (all `gh`/`git`/clipboard calls go through this)

## Screen Flow

```
MainMenu (Home dashboard) → PrTypeSelect → Loading → CommitReview → TitleInput → Confirmation → Creating → Complete
    ↓
(Batch) → BatchRepoSelect → BatchConfirmation → BatchProcessing → BatchSummary
    ↓
(View PRs) → ViewOpenPrs → MergeConfirmation → Merging → MergeSummary
    ↓
(Actions) → ActionsOverview (run list and detail, no sub-screens)
    ↓
(Shipped) → Shipped (releases and what each shipped, no sub-screens)
```

## Key Patterns

- **Idempotent PRs:** `CreateOrUpdatePR` creates if missing, updates if exists
- **Parallel discovery:** Repos fetched concurrently, PRs processed sequentially
- **Ticket extraction:** `tickets.pattern` pulls ticket IDs out of commit messages; the default matches any `ABC-123`-style key. Letters typed literally match either case and classes like `[A-Z]` match only what they say (`compileTicketPattern`), and a match cut out of a longer word is skipped (`git.findTickets`). Highlight tickets with `git.HighlightTickets`, not the raw regex, so the screen marks what goes into the PR
- **Release steps are config, not code:** `[[flows]]` lists the chain (`head` →
  `base`, `base = "@default"` for the repo's own default branch). `@default` is
  `RepoInfo.MainBranch`: origin/HEAD, except that `releaseTargets` (in
  `discoverRepos` and the single-repo load) swaps a default that is one of the
  chain's heads for main or master (`git.GuessMainBranch`). Most of one team's
  repos default to `dev`, and releasing staging into dev was the result. There used to
  be a two-value `PrType` enum with `"dev"`, `"staging"` and `"main"` baked into
  its switches, which is why the tool only worked for one branching model. Every
  screen now asks `m.flows()`: the step menu lists one row per step, the open-PRs
  screen renders one *column* per step (windowing when more than fit), the pull
  menu offers `chainBranches()`, and `branchColor` colours a branch by its
  position in the chain rather than by matching its name
- **Dry-run mode:** Returns fake data with delays for testing without GitHub
- **Self-update verifies before it replaces:** `update.install` downloads the binary and the release's `SHA256SUMS` into a private temp dir and installs only on a match. A release without `SHA256SUMS` is refused, so `release.yml` must keep publishing it

**Async Message Pattern:** Commands return `tea.Cmd` functions that emit typed result messages (e.g., `fetchCommitsResult`, `batchRepoResult`). New async operations need: (1) a result type, (2) a command function, (3) a handler — all three in the screen's own file — plus (4) a case in `update.go`'s `dispatch`, which is the only part that is shared.

**Stale results are dropped, not handled:** a result that answers a flow's request gets `func (x) flowResult() {}` beside its type. `Update` stamps it with the current `epoch` (through `tea.Batch` and `tea.Sequence` too) and drops it if the epoch has moved on. The epoch moves on when you arrive at the main menu, switch tab, or pick a new release step (`newEpoch`). So a marked result's handler can assume the screen that asked is still showing. Forget the marker and the result is applied whenever it lands, which is how a late batch result used to start a PR in a repo ticked afterwards.
- **Results that stay unmarked:** only update data keyed to what they fetched, never move the screen, and are harmless late. That covers a run's Actions jobs, where dropping left the panel loading forever; the settings QA-person lookup; and the first-run preview.
- **A result holding something to release,** like a cancel func, also implements `abandon()`, which runs when it is dropped.
- **A new "result is coming" flag** (a spinner or refresh guard) must be cleared in `newEpoch`, or it waits forever for a result that was dropped.
- **No tabs while a multi-step write runs.** The tab keys are off on `isBusy` screens (creating, batch processing, merging, pulling, updating), because leaving would drop the results that start each next step. QA tagging is one command, so leaving the summary loses only its display, not the write.

**Screens own their state:** each screen's fields live in a struct in its own file (`batchState`, `actionsState`, …) and hang off Model as one field. This is what makes `reset()` correct: it assigns zero values instead of listing fields. The previous per-field version had drifted to missing about twenty, including `existingPR` — which meant a second single-PR run in a session began believing the new repo's PR already existed. Fields genuinely shared across screens (`prType`, `tickets`, `prTitle`) stay flat on Model, and are named as shared there.

**Key hints are data, not code:** `keys.go` holds `staticKeyHints` (screens whose hints never vary) and `dynamicKeyHints` (screens whose hints depend on state). Both the footer and the `?` overlay render `m.keyHints()`, so the help can't drift from what the screen actually does. Add a screen → add its entry here, or it shows no hints. Note that `q` does *not* mean the same thing everywhere — on `ScreenError` it means "back" — so there is deliberately no global quit handler.

**Adding a screen touches six places**, and missing one fails quietly: the `Screen` enum and its `String()` (`screens.go`), the `renderContentWithHeight` switch and `screenTitles` (`view.go`), the `handleKey` switch (`update.go`), and `dynamicKeyHints` or `staticKeyHints` (`keys.go`). A screen with a text input needs `isTextInputActive` too, or `?` and the tab keys steal keystrokes mid-word. Add a golden case while you are there.

**Polled GitHub calls are conditional requests:** the Actions runs and jobs fetches go through `github.cachedGet`, which sends the last ETag and returns the stored body on a 304. A 304 does not count against the REST rate limit (5,000 an hour); polling 29 repos every 5 seconds without it used the limit up in about 15 minutes, after which PR creation failed too. Anything new that polls should use it; a path that changes every call (a timestamp in it) should not, since every distinct path keeps its body.

**The open-PR search runs in groups:** `SearchAllOpenPRs` and `SearchOpenPRsForDashboard` search `searchRepoChunk` (4) repos per request, all at once, in pages of 25. A search's time grows with the PRs it returns, and one search over 31 repos and 106 PRs timed out (HTTP 504) every time; grouped, all 106 arrive in about 5 seconds. GraphQL charges by the page size asked for (2 points for 25), so keep pages small rather than raising them to save round trips.

**External commands:** every `gh`/`git`/clipboard/browser call goes through `internal/run`, which wraps `exec.CommandContext` with a deadline (`run.Network` 30s, `run.Local` 5s, `run.Slow` 5m). Never use `exec.Command` directly — the TUI blocks on these, so an unbounded call freezes the app.

**Repo discovery is cached:** call `discoverRepos(cfg)`, not `git.FindRepos` directly. The cache keys on the config values that affect discovery, so a settings change invalidates it automatically; call `invalidateRepoCache()` for an explicit user-driven refresh.

**Config writes:** every one goes through `Model.saveConfig`, which is also where `--dry-run` stops them — the flag promises to make no changes, and the config is the only thing the app writes outside a repo. It returns `errDryRun` in that case, which is not a failure: the in-memory change stands, so the editors keep working and simply report that nothing was written. `Config.Save()` marshals the whole struct and so loses user comments — call it only for deliberate settings changes. Machine-written values (update check time, skipped version) belong in `prflow-state.toml` via `Config.State()`, which is why merely launching the app no longer rewrites the user's TOML. All writes are atomic (temp file + rename).

**Config validation:** `Config.Validate()` returns `[]Diagnostic`. It exists because a bad path, an empty glob, and a group assigned to no column all used to produce the same symptom — an empty list. Diagnostics render on the settings screen and are flagged in the footer elsewhere. Column names are compared case-insensitively, matching `LeftGroups()`; comparing them raw made `left = ['frontend']` against a `Frontend` glob report a warning about a config that worked.

**Settings are descriptors, not screens:** `settingsFields` in `settings.go` describes each row — a `Bool`/`Toggle` pair, a `Get`/`Set` pair, or an `Opens` naming a list. Adding a setting is one entry; there is no per-field code in the renderer or the key handler. `Set` may return an error, which leaves the config untouched and shows why (that is what stops an invalid ticket regex from silently disabling extraction).

**One editor for the five list settings:** `[[flows]]`, `[[globs]]`, `[[repos]]`, `columns.left` and `columns.right` are all a table of rows of one to three text cells, so `listSettings` in `listedit.go` describes them and `ScreenListEdit` renders all five. Its `Hint` shows `Config.KnownGroups()` while you type, which is what keeps a column entry from drifting from the globs that produce it. Deleting a row takes two `d` presses and any other key disarms it.

**Every settings write goes through `applySettingsChange`:** it saves, re-runs `Validate()` and calls `invalidateRepoCache()`. Editing a glob is exactly when that cache is stale, and the cache keys on these values, so skipping it shows up as a repo list that ignores the edit.

**The Home dashboard (`home.go` data, `mainmenu.go` cards):** the main menu is
a dashboard whose Start card is the old menu. `fetchHomeCmd` makes three
requests for every repo at once: the open-PR search (`SearchOpenPRsForDashboard`,
only the fields the cards use), one GraphQL branch
comparison (`github.CompareBranches`, `Ref.compare(headRef:).aheadBy`) and each
repo's Actions runs from the last 24 hours. Repos are deduped by owner/repo
first, since a worktree under the repos dir is a second checkout of the same
repo. `buildHomeData` turns them into cards and is pure,
so tests and `--dry-run` drive it with fixtures. Its result is deliberately
*not* a `flowResult`: it only replaces the cache, and `home.gen` drops a fetch
that a newer one replaced. It loads on launch (not first run), on `r`, and on
arriving home when older than `homeStaleAfter` or after a failed fetch;
`applySettingsChange` replaces the cache with an empty one and bumps the gen,
so nothing from the old settings is shown and the way home refetches. A source
that fails goes into `Problems` and shows on its card instead of an empty
list. Home is also a tab: `ui.HomeTab` (-1) comes before the first tab in the `[`/`]` cycle.
- **Ready to merge** needs GitHub's `mergeStateStatus` to be `CLEAN` (or
  `HAS_HOOKS`, the same on a server with pre-receive hooks):
  `mergeable` only rules out conflicts, so a PR still needing a required review
  passed it. The state is not in the open-PR search, which GitHub timed out
  (HTTP 504) with it, since it works the state out for every PR in the page.
  `github.MergeStates` asks for the PRs the cards show, 10 per request, all at
  once (one request for 84 PRs timed out), and asks once more after
  `homeMergeStateRetry` for the ones GitHub answered `UNKNOWN`, which the
  first request for a PR always gets. A group that fails leaves its PRs
  unchecked and goes into `Problems` as "merge states": the PR cards stay, with
  "conflicts unchecked" in the corner.
- **Cards** are `ui.Card`: shaded, borderless and exactly the size asked.
  lipgloss ends every styled span with a reset, which would punch a hole in the
  shading, so `ui.OnBackground` puts the panel colour back after each one.
  Below 6 rows, or when its lines need the room, a card drops the blank row
  under its title; `ui.CardRows` says how many lines fit with it, and each
  card cuts its own spacing before its content. Start relies on the second:
  six tabs are a row more than the 30-row layout's cards hold, and the last
  row was cut (`TestStartCardListsEveryTab`).
- **Layout** (`renderHome`): two columns from `twoColumnMinWidth`, else
  stacked. When a layout is too tall it tries a tighter one, then drops cards
  (pipeline and the middle row go, Start never does). It is handed
  `unboxedHeight()`, the room a full-layout screen really has, computed the way
  `View` spends it rather than from `chrome`'s floored figure: at 80 columns
  the footer wraps to four rows, and the floor made Home overflow at 80x19. The
  boxed height-aware screens (settings, list editor, history) get
  `boxedHeight()`, the same figure less the box's four rows; the list editor
  overflowed 80x19 the same way. The height test runs every `heightAwareScreens`
  case at 80 and 120 columns, normal and fullscreen.

**Trunk-based mode** (`branching = "trunk"`, the Trunk-based row in Settings):
for teams that work straight on the default branch and release by deploying
it. The `[[flows]]` stay saved but unused, so switching back restores them.
- **`m.flows()` returns nothing** in trunk mode, so nothing compares, fetches or
  colours by the unused chain; `chainBranches()` follows. `resolveTargets`
  skips the `@default` swap, since a trunk-based repo releases its real default
  branch, and the repo cache key includes the mode.
- **Tabs have IDs** (`ui.TabSingle` … `ui.TabShipped`), and `m.visibleTabs()`
  lists the ones shown: Single, Batch and Release PRs go in trunk mode. A tab's
  ID is never its position. `activeTab` holds an ID; `menuIndex` on Home is a
  Start row, turned into a tab by `visibleTabs()[row]` and back by `startRow`;
  the `[`/`]` cycle steps through `HomeTab` then `visibleTabs()` by position;
  `navigateToTab` refuses a hidden tab; and `ui.RenderHeader` draws only
  `HeaderInfo.Tabs` at every width. When they were the same number, `]` from
  Home added one and landed on a hidden tab.
- **Home** (`buildTrunkHomeData`, `renderTrunkHome`): no pipeline, and the PR
  cards cover every open PR into each repo's default branch, forks included.
  Drafts are counted, not flagged. Attention is one row per PR with every
  reason, filed under the most blocking: CI failing, conflict, changes
  requested, review required (`reviewDecision`, in the dashboard search).
- **Pull** (`p`) pulls each repo's default branch directly: there is no chain
  to pick from.

**Animation tick:** the 80ms tick chain stops when `needsAnimation()` is false and `Update` restarts it when state changes. If you add something that animates on an otherwise-static screen, add it to `needsAnimation()` or it will appear frozen.

**Tests are deliberately narrow.** They gate releases: `.github/workflows/test.yml` (gofmt, vet,
`go test -race`) runs before `auto-tag.yml` tags a push to main, again in `release.yml`, and on
pull requests. A push that only touches `*.md` or `docs/` does not release. The suite covers three things:
golden renders of every screen plus its interesting states (86 cases in
`internal/app/testdata/screens/`, recorded at 120x40 except those in `goldenSizes`), regressions for bugs that actually occurred, and the
derived PR-status rules in `prstatus.go`. If a render changes intentionally, re-record
with `go test ./internal/app -update` and *read the diff* — an unexplained change in a
screen you didn't touch is the signal the goldens exist to give. `TestMain` pins the
lipgloss colour profile, the `timeNow` clock seam and the `configPathFn` path seam;
without all three, renders differ between runs, machines and operating systems.

**Two-Column Navigation:** Batch repo select and merge views share a pattern - separate indices per column, filter functions return indices into main slice, arrow keys navigate within column, left/right switches columns.

**Actions** (`ScreenActionsOverview`): one flat list of runs, newest first, one line per run, with the highlighted run's jobs in a detail pane beside it and watched runs (`Space`) in a box below that. It used to group the list by repo while sorting it by time, so a header was drawn every time the repo changed, and a run's jobs only showed once it was pinned and the pinned column entered.
- **Jobs** are cached by run ID in `actions.jobs`, and tied to the run's `UpdatedAt`, because a rerun keeps the ID. Moving the cursor sends `actionsPreviewMsg` after `actionsPreviewDelay`, and only a preview for the run still highlighted fetches, so holding ↓ starts no fetch per run. Go through `fetchRunJobs`: it skips a fetch already out and jobs that are final (fetched once the run had completed, for the `UpdatedAt` it still has). An answer older than the newest request for its run is dropped. Each runs refresh re-anchors the cursor on its run ID and refetches the highlighted and watched runs' jobs through it.
- **Watched runs** always stay in the list, but the fetch only sees each repo's latest ten runs, so a watched run pushed out of them is fetched by ID (`unfetchedWatched`, `github.GetWorkflowRunByNWO`). Without that it stayed as last seen, running for good.
- **Filter:** `/` gives the keyboard to the filter (`filterTyping`, which is what `isTextInputActive` reports); Enter keeps the filter and returns the keys, Esc clears it. Esc with a kept filter clears it; otherwise it goes Home the way the tab keys do, keeping the list and the watched runs.
- **Refresh chains (Actions, All PRs):** start one only with `startActionsRefresh`/`startAllPRsRefresh`, which bump a generation that older ticks no longer match, and only a tick schedules the next tick. Fetch results used to schedule ticks too, so a toggle, a manual refresh or a tab round trip each added a chain and the `gh` calls multiplied. The chain stops when navigating away. Both screens use `r` to refresh now and `a` to toggle auto-refresh.

**All Open PRs** (`ScreenViewAllPrs`, `allprs.go`):
- **One row per PR:** the fetch goes through `githubRepos`, one entry per GitHub repo. It searched every checkout, so a repo with a worktree under the repos dir had its PRs listed twice, under the worktree's folder name.
- **Review requests** come from one REST list per repo (`github.PRsAwaitingReviewers`), since GraphQL leaves out bot reviewers. Asking once per PR, one after another, made the screen take 35s.
- **Mine** (`@`) shows the PRs the viewer wrote, reviewed, or was asked to review, directly or through a team (`involvesViewer`). The fetch returns the viewer and their teams (`github.Viewer`). Everything that acts on "the highlighted PR" goes through `highlightedPR`, and the cursor is held by PR URL across refreshes, sorting and the toggle (`selectPR`): a refresh used to keep the row, so a PR merged above the cursor moved it onto another.
- **Actions on the highlighted PR** (`practions.go`): `m` merges, `R` re-runs failed checks, `w` checks it out into a worktree.
  - Merge and re-run are checked first (`prCheckResult`, a flowResult), then wait in `allPRs.pending` for `y`. While a prompt is up, `isTextInputActive` gives every key to it, and any key but `y` only cancels.
  - A merge needs `CLEAN` or `HAS_HOOKS` and the head the list showed, and uses the viewer's default method (`viewerDefaultMergeMethod`): the repos differ, squash in one and merge commits in another. `--match-head-commit` makes GitHub refuse it if the branch moves after the check.
  - A re-run takes the newest run per workflow and event on the head commit (`github.FailedRunsFor`); an older failure a later run replaced is not what the PR shows.
  - The worktree goes in `<main checkout>/.worktrees/pr-N` (`git.MainWorktree` finds the main one from any worktree), with `/.worktrees/` added to `.git/info/exclude`. Anywhere under the repos dir the globs would find it as another repo. `gh pr checkout` runs inside it, and a failed checkout removes it again.
  - The results (`prActionDoneResult`) are not flowResults: the write has happened wherever the user now is, so the footer reports it anyway.

**Shipped** (`ScreenShipped`, `shipped.go`, a tab in both modes): every repo's GitHub releases, newest first across the repos, with what the highlighted one shipped since the release before it in its repo.
- **A release is the deploy record.** The trunk-based app this was built for publishes one when a prod deploy succeeds. Its tags are claimed before that deploy, which does not always happen, and its prod deployment records come several to a release. So `github.Releases` lists published, non-draft releases, `shippedPerRepo` (10) per repo plus one, so the oldest listed has one to compare with (`shippedEntries`).
- **The comparison** (`github.CompareReleases`) asks both directions in one request. A rollback's release is behind the one before it, so its commits are `Removed` and show as taken out, tickets included; a diverged pair has both. It is fetched on a rested cursor like a run's jobs (`shippedPreviewMsg`), once per release and commit, since it never changes.
- **A commit's PR** is the one whose merge commit it is: GitHub associates a commit with every PR that later carried it, a release PR included. Release PRs from the chain's heads are left out of the list (`summarize`), since their commits come with their own PRs.
- `c` copies the release's PRs and tickets as Markdown (`shippedMarkdown`).

**Adding a tab** is a `ui.Tab*` ID with its `TabNames`, `tabShortNames`, `tabTinyNames` and `TabColors` entries, a `startItems` row, `openTab` and `navigateToTab` cases, and `visibleTabs`. The header sheds long names, then short, then tiny ones before it drops the dry-run badge; six short names did not fit beside it at 60 columns.

## Configuration (`~/.config/prflow.toml`)

Repos are discovered via glob patterns under `repos_dir` and/or explicit `[[repos]]` entries. Repos are assigned to named groups, and groups are assigned to left/right columns.

Every field below is editable in-app from the settings screen (`o` on the main menu), including the four lists — the TOML is a format, not the interface. Hand-editing still works, and comments survive, because the app only rewrites the file on a deliberate settings change.

```toml
[paths]
repos_dir = '~/Projects'                  # Base dir for glob discovery

[columns]
left = ['Frontend']                       # Groups shown in left column
right = ['Backend', 'Services']           # Groups shown in right column

[[globs]]
pattern = 'frontend/*'                    # Glob relative to repos_dir
group = 'Frontend'

[[globs]]
pattern = 'backend/*'
group = 'Backend'

# Explicit repos at arbitrary paths (for scattered repos)
[[repos]]
path = '~/Projects/some-service'
group = 'Services'

# The chain a release moves through, in order. Each entry is one PR.
# base = "@default" means the repo's default branch on GitHub, unless that is
# a branch the chain releases from (a repo defaulting to dev): then main or master.
[[flows]]
head = 'dev'
base = 'staging'

[[flows]]
head = 'staging'
base = '@default'
title = 'Sprint # '                       # seeds the PR title input

[tickets]
pattern = '[A-Z][A-Z0-9]+-[0-9]+'         # Regex for ticket extraction from commits
linear_org = 'example'                    # Linear workspace slug (optional)
qa_person = 'name.here'                   # Linear display name for QA tagging
qa_person_id = 'uuid-here'               # Linear user UUID (skips lookup)
qa_tagging = true                         # Show QA tagging screen after merge

[update]
enabled = true
repo = 'JonrGull/prflow'                  # GitHub repo for self-update checks
```

**Machine-written state** lives beside the config in `prflow-state.toml`
(`last_check`, `skipped_version`). It is deliberately *not* in `prflow.toml`:
those values change on nearly every launch, and rewriting the config to store
them destroyed any comments the user had added. Older configs that still carry
them under `[update]` are migrated on load.

**The rename:** the tool used to be called `attpr`. The config and state
files, the binary, the release assets and the module path all changed name. `internal/config` reads `attpr.toml` and `attpr-state.toml` when
the current names are absent, and the read is one-way: the next deliberate
settings change writes `prflow.toml` and leaves the old file alone, so a
downgrade still finds its config. The one value not carried over from that
file is `update.repo`: it names the release repo of the tool prflow was forked
from, whose releases are a different tool on a higher version line, so trusting
it offered an "update" to the predecessor on every launch. The old state
file's `skipped_version` is dropped for the same reason: it names one of those
releases. `rename_test.go` covers all of it.

**Backward compatibility:** A config written before `[[flows]]` existed gets the default two-step chain, the same way the globs defaults are restored — so an existing file keeps working untouched. Defaults fill only keys the file *omits* (`hasKey` in `Load`); a key set to empty (`repos_dir = ''`, `globs = []`) stays empty, or clearing it in settings undoes itself on the next launch. Old configs with `frontend_glob`/`backend_glob` under `[paths]` auto-migrate to `[[globs]]` entries. The deprecated `category` field on `[[repos]]` maps to `group`.

**Column assignment:** Each repo's `Group` (from glob or explicit entry) is checked against `columns.left`. If it matches, the repo goes in the left column; otherwise right. When a column has multiple groups, group sub-headers appear automatically.

## Git

Commit directly to main - no PRs needed for this repo.

## Warp Terminal Fix (IMPORTANT)

Location: `internal/termfix/termfix.go` - imported as separate `import` statement at top of `cmd/prflow/main.go`

Warp on WSL2 causes 5-6s startup delay due to terminal capability queries. The fix sets `TERM=dumb` (skip queries) + `COLORTERM=truecolor` (keep colors) before lipgloss loads.

**Why a separate package?** The fix must run before lipgloss initializes. Using a separate import block prevents goimports from reordering it after `github.com/charmbracelet/bubbletea`.
