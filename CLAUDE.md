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
  - Screens: `mainmenu.go`, `single.go` (release step → review → title → create),
    `batch.go`, `merge.go` (open PRs + merging), `allprs.go`, `actions.go`,
    `pull.go`, `qatag.go`, `settings.go`, `listedit.go`, `firstrun.go`,
    `history.go`, `selfupdate.go`, `errorscreen.go`
  - `screens.go` - Screen enum (29 screens) and AppMode (Single/Batch)
  - `keys.go` - Per-screen key hints as data (status bar + `?` overlay)
  - `prstatus.go` - The derived review/CI/preview rules for the all-PRs table
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
MainMenu → PrTypeSelect → Loading → CommitReview → TitleInput → Confirmation → Creating → Complete
    ↓
(Batch) → BatchRepoSelect → BatchConfirmation → BatchProcessing → BatchSummary
    ↓
(View PRs) → ViewOpenPrs → MergeConfirmation → Merging → MergeSummary
    ↓
(Actions) → ActionsOverview (split-panel, no sub-screens)
```

## Key Patterns

- **Idempotent PRs:** `CreateOrUpdatePR` creates if missing, updates if exists
- **Parallel discovery:** Repos fetched concurrently, PRs processed sequentially
- **Ticket extraction:** `tickets.pattern` pulls ticket IDs out of commit messages; the default matches any `ABC-123`-style key
- **Release steps are config, not code:** `[[flows]]` lists the chain (`head` →
  `base`, `base = "@default"` for the repo's own default branch). There used to
  be a two-value `PrType` enum with `"dev"`, `"staging"` and `"main"` baked into
  its switches, which is why the tool only worked for one branching model. Every
  screen now asks `m.flows()`: the step menu lists one row per step, the open-PRs
  screen renders one *column* per step (windowing when more than fit), the pull
  menu offers `chainBranches()`, and `branchColor` colours a branch by its
  position in the chain rather than by matching its name
- **Dry-run mode:** Returns fake data with delays for testing without GitHub

**Async Message Pattern:** Commands return `tea.Cmd` functions that emit typed result messages (e.g., `fetchCommitsResult`, `batchRepoResult`). New async operations need: (1) a result type, (2) a command function, (3) a handler — all three in the screen's own file — plus (4) a case in `update.go`'s `dispatch`, which is the only part that is shared.

**Screens own their state:** each screen's fields live in a struct in its own file (`batchState`, `actionsState`, …) and hang off Model as one field. This is what makes `reset()` correct: it assigns zero values instead of listing fields. The previous per-field version had drifted to missing about twenty, including `existingPR` — which meant a second single-PR run in a session began believing the new repo's PR already existed. Fields genuinely shared across screens (`prType`, `tickets`, `prTitle`) stay flat on Model, and are named as shared there.

**Key hints are data, not code:** `keys.go` holds `staticKeyHints` (screens whose hints never vary) and `dynamicKeyHints` (screens whose hints depend on state). Both the status bar and the `?` overlay render `m.keyHints()`, so the help can't drift from what the screen actually does. Add a screen → add its entry here, or it shows no hints. Note that `q` does *not* mean the same thing everywhere — on `ScreenError` it means "back" — so there is deliberately no global quit handler.

**Adding a screen touches six places**, and missing one fails quietly: the `Screen` enum and its `String()` (`screens.go`), the `renderContentWithHeight` switch and `screenTitles` (`view.go`), the `handleKey` switch (`update.go`), and `dynamicKeyHints` or `staticKeyHints` (`keys.go`). A screen with a text input needs `isTextInputActive` too, or `?` and the tab keys steal keystrokes mid-word. Add a golden case while you are there.

**External commands:** every `gh`/`git`/clipboard/browser call goes through `internal/run`, which wraps `exec.CommandContext` with a deadline (`run.Network` 30s, `run.Local` 5s, `run.Slow` 5m). Never use `exec.Command` directly — the TUI blocks on these, so an unbounded call freezes the app.

**Repo discovery is cached:** call `discoverRepos(cfg)`, not `git.FindRepos` directly. The cache keys on the config values that affect discovery, so a settings change invalidates it automatically; call `invalidateRepoCache()` for an explicit user-driven refresh.

**Config writes:** every one goes through `Model.saveConfig`, which is also where `--dry-run` stops them — the flag promises to make no changes, and the config is the only thing the app writes outside a repo. It returns `errDryRun` in that case, which is not a failure: the in-memory change stands, so the editors keep working and simply report that nothing was written. `Config.Save()` marshals the whole struct and so loses user comments — call it only for deliberate settings changes. Machine-written values (update check time, skipped version) belong in `prflow-state.toml` via `Config.State()`, which is why merely launching the app no longer rewrites the user's TOML. All writes are atomic (temp file + rename).

**Config validation:** `Config.Validate()` returns `[]Diagnostic`. It exists because a bad path, an empty glob, and a group assigned to no column all used to produce the same symptom — an empty list. Diagnostics render on the settings screen and are flagged in the status bar elsewhere. Column names are compared case-insensitively, matching `LeftGroups()`; comparing them raw made `left = ['frontend']` against a `Frontend` glob report a warning about a config that worked.

**Settings are descriptors, not screens:** `settingsFields` in `settings.go` describes each row — a `Bool`/`Toggle` pair, a `Get`/`Set` pair, or an `Opens` naming a list. Adding a setting is one entry; there is no per-field code in the renderer or the key handler. `Set` may return an error, which leaves the config untouched and shows why (that is what stops an invalid ticket regex from silently disabling extraction).

**One editor for the five list settings:** `[[flows]]`, `[[globs]]`, `[[repos]]`, `columns.left` and `columns.right` are all a table of rows of one to three text cells, so `listSettings` in `listedit.go` describes them and `ScreenListEdit` renders all five. Its `Hint` shows `Config.KnownGroups()` while you type, which is what keeps a column entry from drifting from the globs that produce it. Deleting a row takes two `d` presses and any other key disarms it.

**Every settings write goes through `applySettingsChange`:** it saves, re-runs `Validate()` and calls `invalidateRepoCache()`. Editing a glob is exactly when that cache is stale, and the cache keys on these values, so skipping it shows up as a repo list that ignores the edit.

**Animation tick:** the 80ms tick chain stops when `needsAnimation()` is false and `Update` restarts it when state changes. If you add something that animates on an otherwise-static screen, add it to `needsAnimation()` or it will appear frozen.

**Tests are deliberately narrow.** There is no CI. The suite covers three things:
golden renders of every screen plus its interesting states (61 cases in
`internal/app/testdata/screens/`), regressions for bugs that actually occurred, and the
derived PR-status rules in `prstatus.go`. If a render changes intentionally, re-record
with `go test ./internal/app -update` and *read the diff* — an unexplained change in a
screen you didn't touch is the signal the goldens exist to give. `TestMain` pins the
lipgloss colour profile, the `timeNow` clock seam and the `configPathFn` path seam;
without all three, renders differ between runs, machines and operating systems.

**Two-Column Navigation:** Batch repo select, merge, and actions views share a pattern - separate indices per column, filter functions return indices into main slice, arrow keys navigate within column, left/right switches columns.

**Actions Split-Panel:** Single `ScreenActionsOverview` with left (run list) and right (pinned detail panels). Space pins/unpins runs, `/` enters filter mode, `o` opens in browser. Auto-refreshes every 5s via `actionsRefreshTickCmd` → `actionsRefreshTickMsg` tick chain. Pinned panels show job/step details, re-fetched on refresh for active runs. The tick chain is guarded to stop when navigating away. Key caveat: `adjustActionsPinnedScroll` estimates panel line heights — must stay in sync with `renderPinnedPanel` output.

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
# base = "@default" means the repo's own default branch (main or master).
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
downgrade still finds its config. `paths.attuned_dir` migrates to
`paths.repos_dir` on load and is dropped on the next write. `rename_test.go`
covers all of it.

**Backward compatibility:** A config written before `[[flows]]` existed gets the default two-step chain, the same way the globs defaults are restored — so an existing file keeps working untouched. Old configs with `frontend_glob`/`backend_glob` under `[paths]` auto-migrate to `[[globs]]` entries. The deprecated `category` field on `[[repos]]` maps to `group`.

**Column assignment:** Each repo's `Group` (from glob or explicit entry) is checked against `columns.left`. If it matches, the repo goes in the left column; otherwise right. When a column has multiple groups, group sub-headers appear automatically.

## Git

Commit directly to main - no PRs needed for this repo.

## Warp Terminal Fix (IMPORTANT)

Location: `internal/termfix/termfix.go` - imported as separate `import` statement at top of `cmd/prflow/main.go`

Warp on WSL2 causes 5-6s startup delay due to terminal capability queries. The fix sets `TERM=dumb` (skip queries) + `COLORTERM=truecolor` (keep colors) before lipgloss loads.

**Why a separate package?** The fix must run before lipgloss initializes. Using a separate import block prevents goimports from reordering it after `github.com/charmbracelet/bubbletea`.
