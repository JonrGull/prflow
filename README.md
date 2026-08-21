# prflow

TUI for creating and managing GitHub release PRs across multiple repositories.

Define the chain a release moves through — `dev → staging → main`, or whatever
your team actually uses — and prflow opens, tracks and merges the PRs for each
step across every repo at once.

## Install

### Prerequisites

- [GitHub CLI](https://cli.github.com/) (`gh`) authenticated

### Quick Install

```bash
curl -fsSL https://raw.githubusercontent.com/JonrGull/prflow/main/install.sh | bash
```

This installs to `~/.local/bin`.

### From Source

Requires [Go 1.25+](https://go.dev/doc/install).

```bash
git clone https://github.com/JonrGull/prflow.git
cd prflow
go build -o ~/.local/bin/prflow ./cmd/prflow
```

## Usage

```bash
prflow              # Normal mode
prflow --dry-run    # Test without GitHub access
```

`--dry-run` makes no changes anywhere: no GitHub or Linear calls, no update
check, and no config writes. Settings and list edits still take effect for the
session so the screens behave normally — they just say so instead of saving.
It is the right way to try a build you have not run before.

### Navigation

Press `?` in the app for the bindings on the current screen — that list is
generated from the same table the status bar uses, so it is always current.
The keys that work almost everywhere:

| Key | Action |
|-----|--------|
| `?` | Show keyboard shortcuts for the current screen |
| `↑/↓` | Navigate lists |
| `←/→` | Switch columns (batch/merge/actions views) |
| `Enter` | Select/Confirm |
| `Space` | Toggle selection / Pin run (actions) |
| `/` | Enter filter mode (actions) |
| `o` | Open in browser |
| `[` / `]` | Previous/next tab |
| `F` | Toggle fullscreen (hides banner and tabs) |
| `Esc` | Go back |
| `q` | Quit — except on the error screen, where it goes back |
| `Ctrl+C` | Quit from anywhere |

Main menu only: `a` Actions, `p` Pull all, `o` Settings, `c` open config
folder, `u` check for updates, `h` session history.

## Features

- **Single PR**: Create a release PR for one repo, for any step of your
  configured release chain
- **Batch PR**: Create release PRs across multiple repos at once
- **View/Merge PRs**: See open release PRs and merge them
- **GitHub Actions**: Monitor workflow runs across all repos with a split-panel view — pin runs to see job/step details, auto-refreshes every 5s
- **Ticket Extraction**: Automatically extracts ticket IDs from commit messages
- **Auto-Update**: Checks for updates on startup and prompts to install
- **Configurable release chain**: Define the steps a release moves through in
  `[[flows]]` — two, three or more — and every screen follows them
- **In-app settings**: Every config value is editable from the settings screen,
  including the release steps and the glob, repo and column lists — no need to
  quit and open the TOML
- **Config validation**: Flags a scan directory that doesn't exist, globs that
  match nothing, and group names assigned to neither column — problems that
  otherwise show up only as an unexplained empty list

## Configuration

Everything below can be edited in the app: press `o` from the main menu for
settings. The glob, repo and column lists open a row editor (`a` adds, `Enter`
edits a cell, `d` twice deletes), which shows the group names already in use so
a column entry can't drift from the globs that produce it. Changes are saved as
you confirm them, and validation re-runs immediately, so a warning about a
misconfigured group clears the moment you fix it.

The file is still there if you prefer it, and hand-written comments survive —
the app only rewrites the config when you deliberately change a setting.

Config is created on first run, which offers to scan a directory for repos and
shows what it found before saving:
- **Linux**: `~/.config/prflow.toml`
- **macOS**: `~/Library/Application Support/prflow.toml`

```toml
[paths]
# Parent directory containing your repositories
repos_dir = "~/Projects/my-org"

# Which groups appear in the left and right columns of the two-column views.
# A group not listed in `left` falls into the right column.
[columns]
left = ["Frontend"]
right = ["Backend", "Services"]

# Glob patterns for discovering repos, relative to repos_dir.
# Each glob assigns the repos it matches to a group.
[[globs]]
pattern = "frontend/*"
group = "Frontend"

[[globs]]
pattern = "backend/*"
group = "Backend"

# Explicit repos at arbitrary paths, for repos outside repos_dir
[[repos]]
path = "~/Projects/some-service"
group = "Services"

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
# Regex pattern for extracting ticket IDs from commits. Empty disables extraction.
# The default matches any ABC-123-style key, which also catches lookalikes such
# as UTF-8 — set your project's own prefix to avoid that.
pattern = "PROJ-[0-9]+"
# Linear organization slug (for PR body links)
linear_org = "my-org"
# Linear display name to tag for QA after a merge; empty disables QA tagging
qa_person = ""
# Linear user UUID — set automatically after a successful lookup, skips it next time
qa_person_id = ""
# Show the QA tagging screen after merging
qa_tagging = true

[update]
# Auto-update settings
enabled = true
# GitHub repo to check for new releases
repo = "JonrGull/prflow"
```

The Linear API key is read from `$LINEAR_API_KEY`, or from a
`LINEAR_API_KEY=` line in `~/.secrets`. It is never stored in the config file.

Values the app writes for itself — when it last checked for an update, and any
version you skipped — live in `prflow-state.toml` beside the config, so merely
launching prflow never rewrites the file you edited.

Older configs are migrated automatically on load: `attuned_dir` under `[paths]`
(the tool's former name), `frontend_glob`/`backend_glob` under `[paths]`,
`category` on a `[[repos]]` entry, and `last_check`/`skipped_version` under
`[update]`. A config still named `attpr.toml` is read as-is; the next
deliberate settings change writes `prflow.toml` and leaves the old file alone.

### Example Directory Structure

```
~/Projects/my-org/
├── frontend/
│   ├── web-app/
│   ├── mobile-app/
│   └── admin-portal/
└── backend/
    ├── api-service/
    ├── auth-service/
    └── worker-service/
```

With the globs above, prflow discovers every repo under `frontend/*` and
`backend/*`, grouping them as Frontend and Backend respectively.
