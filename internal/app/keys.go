package app

import (
	"fmt"

	"github.com/JonrGull/prflow/internal/ui"

	"github.com/charmbracelet/lipgloss"
)

// keyHint is one key binding as shown to the user.
//
// These used to live inline in a 210-line switch inside renderStatusBar, which
// made the status bar the de-facto record of what each screen responds to —
// easy to forget when adding a screen, and impossible to reuse. Holding them as
// data lets the status bar and (later) a help overlay read the same source.
type keyHint struct {
	Key   string
	Desc  string
	Color lipgloss.Color
}

// Hints shared across screens, named so the tables below stay readable and a
// label change lands everywhere at once.
var (
	hintNavigate = keyHint{"↑↓", "Navigate", ui.ColorWhite}
	hintColumn   = keyHint{"←→", "Column", ui.ColorWhite}
	hintSelect   = keyHint{"←→", "Select", ui.ColorWhite}
	hintToggle   = keyHint{"Space", "Toggle", ui.ColorGreen}
	hintContinue = keyHint{"Tab", "Continue", ui.ColorGreen}
	hintBack     = keyHint{"Esc", "Back", ui.ColorYellow}
	hintQuit     = keyHint{"q", "Quit", ui.ColorRed}
	hintDone     = keyHint{"Enter", "Done", ui.ColorGreen}
	hintRefresh  = keyHint{"r", "Refresh", ui.ColorBlue}
	hintOpen     = keyHint{"o", "Open", ui.ColorBlue}
	hintOpenURL  = keyHint{"o", "Open URL", ui.ColorBlue}
	hintOpenURLs = keyHint{"o", "Open URLs", ui.ColorBlue}
	hintCopyURL  = keyHint{"c", "Copy URL", ui.ColorBlue}
	hintCopyURLs = keyHint{"c", "Copy URLs", ui.ColorBlue}
	hintMergePRs = keyHint{"m", "Merge PRs", ui.ColorGreen}
	hintFilter   = keyHint{"Type", "Filter", ui.ColorYellow}
)

// staticKeyHints holds screens whose hints never change.
// Screens absent from both this and dynamicKeyHints show no hints — that is
// the case for the progress screens, which take no input.
var staticKeyHints = map[Screen][]keyHint{
	ScreenMainMenu: {
		{"1-6", "Select", ui.ColorYellow},
		hintNavigate,
		{"Enter", "Select", ui.ColorGreen},
		{"a", "Actions", ui.ColorOrange},
		{"p", "Pull", ui.ColorGreen},
		{"o", "Settings", ui.ColorCyan},
		{"c", "Config", ui.ColorMagenta},
		{"u", "Update", ui.ColorCyan},
		{"h", "History", ui.ColorBlue},
		hintQuit,
	},
	ScreenTitleInput: {
		{"Enter", "Submit", ui.ColorGreen},
		hintBack,
	},
	ScreenConfirmation:      confirmationHints,
	ScreenBatchConfirmation: confirmationHints,
	ScreenMergeConfirmation: confirmationHints,
	ScreenComplete: {
		hintOpenURL,
		hintCopyURL,
		hintMergePRs,
		hintDone,
	},
	ScreenBatchRepoSelect: {
		hintNavigate,
		hintColumn,
		hintToggle,
		hintContinue,
		hintFilter,
	},
	// handleErrorKey treats q as "go back", not quit — the status bar used to
	// advertise "q Quit" here, which was simply untrue.
	ScreenError: {
		{"Enter", "Back", ui.ColorGreen},
		{"q", "Back", ui.ColorYellow},
	},
	ScreenBatchSummary: {
		hintOpenURLs,
		hintCopyURLs,
		hintMergePRs,
		hintDone,
		hintQuit,
	},
	ScreenMergeSummary: {
		hintOpenURLs,
		hintCopyURLs,
		hintDone,
		hintQuit,
	},
	ScreenUpdatePrompt: {
		hintSelect,
		{"y", "Update", ui.ColorGreen},
		{"n", "Skip", ui.ColorYellow},
		{"s", "Skip version", ui.ColorRed},
		{"Enter", "Confirm", ui.ColorGreen},
	},
	ScreenSessionHistory: {
		hintNavigate,
		hintOpenURL,
		{"c", "Copy", ui.ColorBlue},
		{"⇧C", "Copy All", ui.ColorBlue},
		hintBack,
	},
	ScreenPullSummary: {
		hintDone,
		hintQuit,
	},
	ScreenQaTagSelect: {
		hintNavigate,
		hintToggle,
		{"a", "Toggle all", ui.ColorYellow},
		{"t", "Disable", ui.ColorMagenta},
		{"Enter", "Continue", ui.ColorGreen},
		{"s", "Skip", ui.ColorDarkGray},
		hintBack,
	},
}

// confirmationHints is shared by the three confirmation screens.
var confirmationHints = []keyHint{
	hintSelect,
	{"y/n", "Quick", ui.ColorGreen},
	{"Enter", "Confirm", ui.ColorGreen},
	hintBack,
}

// dynamicKeyHints holds screens whose hints depend on state — an empty list, a
// toggle's current position, or an active sub-mode.
var dynamicKeyHints = map[Screen]func(Model) []keyHint{
	// The number-key range follows the configured release steps, so a chain
	// with three steps says 1-3 rather than promising only two.
	ScreenPrTypeSelect: func(m Model) []keyHint {
		hints := []keyHint{}
		if n := len(m.flows()); n > 1 {
			hints = append(hints, keyHint{fmt.Sprintf("1-%d", min(n, 9)), "Select", ui.ColorYellow})
		}
		return append(hints,
			hintNavigate,
			keyHint{"Enter", "Select", ui.ColorGreen},
			hintBack,
		)
	},

	// Same as above: the pull menu lists the chain's branches, so the range
	// depends on how long the chain is.
	ScreenPullBranchSelect: func(m Model) []keyHint {
		hints := []keyHint{}
		if n := len(m.chainBranches()); n > 1 {
			hints = append(hints, keyHint{fmt.Sprintf("1-%d", min(n, 9)), "Select", ui.ColorYellow})
		}
		return append(hints,
			hintNavigate,
			keyHint{"Enter", "Pull", ui.ColorGreen},
			hintBack,
		)
	},

	ScreenCommitReview: func(m Model) []keyHint {
		if len(m.commits) == 0 {
			return []keyHint{hintBack}
		}
		return []keyHint{
			{"Type", "Edit title", ui.ColorYellow},
			{"Enter", "Create PR", ui.ColorGreen},
			hintBack,
		}
	},

	ScreenViewOpenPrs: func(m Model) []keyHint {
		if len(m.merge.prs) == 0 {
			return []keyHint{hintRefresh, hintBack}
		}
		return []keyHint{
			hintNavigate,
			hintColumn,
			hintToggle,
			hintContinue,
			hintRefresh,
			hintBack,
		}
	},

	ScreenViewAllPrs: func(m Model) []keyHint {
		auto := keyHint{"a", toggleLabel("Auto-refresh", m.allPRs.autoRefresh), ui.ColorBlue}
		if len(m.allPRs.entries) == 0 {
			return []keyHint{hintRefresh, auto, hintBack}
		}
		sortLabel := "Sort: newest"
		if m.allPRs.sortAsc {
			sortLabel = "Sort: oldest"
		}
		return []keyHint{
			hintNavigate,
			hintOpen,
			hintRefresh,
			auto,
			{"s", sortLabel, ui.ColorBlue},
			hintBack,
		}
	},

	ScreenActionsOverview: func(m Model) []keyHint {
		if m.actions.filterActive {
			return []keyHint{
				hintFilter,
				{"Esc", "Clear", ui.ColorYellow},
			}
		}
		auto := keyHint{"R", toggleLabel("Auto-refresh", m.actions.autoRefresh), ui.ColorBlue}
		if m.actions.column == 1 {
			return []keyHint{
				hintNavigate,
				{"←", "Runs", ui.ColorWhite},
				hintOpen,
				auto,
				hintBack,
			}
		}
		return []keyHint{
			hintNavigate,
			{"→", "Pinned", ui.ColorWhite},
			{"Space", "Pin", ui.ColorGreen},
			{"a", "All", ui.ColorCyan},
			{"n", "None", ui.ColorCyan},
			hintOpen,
			auto,
			{"/", "Filter", ui.ColorYellow},
			hintBack,
		}
	},

	ScreenFirstRun: func(m Model) []keyHint {
		if m.firstRun.preview.Ran && len(m.firstRun.preview.Repos) > 0 {
			return []keyHint{
				{"Enter", "Save and continue", ui.ColorGreen},
				{"Esc", "Skip setup", ui.ColorYellow},
			}
		}
		return []keyHint{
			{"Type", "Directory path", ui.ColorYellow},
			{"Enter", "Check", ui.ColorGreen},
			{"Esc", "Skip setup", ui.ColorYellow},
		}
	},

	ScreenListEdit: func(m Model) []keyHint {
		if m.list.editing {
			return []keyHint{
				{"Enter", "Save cell", ui.ColorGreen},
				{"Esc", "Cancel", ui.ColorYellow},
			}
		}
		hints := []keyHint{hintNavigate}
		if setting, ok := m.listSetting(); ok && len(setting.Cells) > 1 {
			hints = append(hints, keyHint{"←→", "Cell", ui.ColorWhite})
		}
		return append(hints,
			keyHint{"Enter", "Edit", ui.ColorGreen},
			keyHint{"a", "Add", ui.ColorCyan},
			keyHint{"d", "Delete", ui.ColorRed},
			hintBack,
		)
	},

	ScreenSettings: func(m Model) []keyHint {
		if m.settings.editing {
			return []keyHint{
				{"Enter", "Save", ui.ColorGreen},
				{"Esc", "Cancel", ui.ColorYellow},
			}
		}
		return []keyHint{
			hintNavigate,
			{"Enter", "Edit / toggle", ui.ColorGreen},
			hintBack,
		}
	},
}

// globalKeyHints are the bindings that work on (almost) every screen. They are
// handled in handleKey rather than per-screen, and were previously documented
// nowhere except the source.
var globalKeyHints = []keyHint{
	{"?", "This help", ui.ColorCyan},
	{"[ ]", "Switch tab", ui.ColorWhite},
	{"F", "Fullscreen", ui.ColorWhite},
	{"Ctrl+C", "Quit", ui.ColorRed},
}

// keyHints returns the hints for the current screen.
func (m Model) keyHints() []keyHint {
	if fn, ok := dynamicKeyHints[m.screen]; ok {
		return fn(m)
	}
	return staticKeyHints[m.screen]
}

func toggleLabel(name string, on bool) string {
	if on {
		return name + ": on"
	}
	return name + ": off"
}
