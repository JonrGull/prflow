package app

import (
	"strings"
	"testing"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/models"

	tea "github.com/charmbracelet/bubbletea"
)

// The key hints used to be a 210-line switch inside renderStatusBar. They are
// now data, and Stage 2 moves them again — so pin what each screen advertises.

func hintString(m Model) string {
	var parts []string
	for _, h := range m.keyHints() {
		parts = append(parts, h.Key+":"+h.Desc)
	}
	return strings.Join(parts, " ")
}

func TestKeyHintsStatic(t *testing.T) {
	tests := map[Screen]string{
		ScreenMainMenu:    "1-6:Select ↑↓:Navigate Enter:Select a:Actions p:Pull o:Settings c:Config u:Update h:History q:Quit",
		ScreenTitleInput:  "Enter:Submit Esc:Back",
		ScreenComplete:    "o:Open URL c:Copy URL m:Merge PRs Enter:Done",
		ScreenPullSummary: "Enter:Done q:Quit",
	}
	for screen, want := range tests {
		if got := hintString(Model{screen: screen}); got != want {
			t.Errorf("%v\n got %q\nwant %q", screen, got, want)
		}
	}
}

// All three confirmation screens deliberately share one list.
func TestKeyHintsConfirmationScreensShared(t *testing.T) {
	want := "←→:Select y/n:Quick Enter:Confirm Esc:Back"
	for _, s := range []Screen{ScreenConfirmation, ScreenBatchConfirmation, ScreenMergeConfirmation} {
		if got := hintString(Model{screen: s}); got != want {
			t.Errorf("%v = %q, want %q", s, got, want)
		}
	}
}

// q means "back" on the error screen, not "quit" — the status bar used to claim
// otherwise. This is also why there is no global quit handler.
func TestKeyHintsErrorScreenSaysBackNotQuit(t *testing.T) {
	got := hintString(Model{screen: ScreenError})
	if strings.Contains(got, "q:Quit") {
		t.Errorf("error screen must not advertise q as Quit: %q", got)
	}
	if !strings.Contains(got, "q:Back") {
		t.Errorf("error screen should advertise q as Back: %q", got)
	}
}

// Screens that take no input show nothing, rather than a misleading bar.
func TestKeyHintsProgressScreensHaveNone(t *testing.T) {
	for _, s := range []Screen{
		ScreenLoading, ScreenCreating, ScreenBatchProcessing,
		ScreenMerging, ScreenUpdating, ScreenPullProgress,
	} {
		if got := hintString(Model{screen: s}); got != "" {
			t.Errorf("%v should have no hints, got %q", s, got)
		}
	}
}

func TestKeyHintsDynamic(t *testing.T) {
	// The number range follows the configured steps. It used to read a
	// hardcoded "1-2", which would have lied to anyone with a three-step chain.
	t.Run("PR type select counts the configured steps", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			flows []config.FlowEntry
			want  string
		}{
			{"two steps", nil, "1-2:Select ↑↓:Navigate Enter:Select Esc:Back"},
			{"three steps", []config.FlowEntry{
				{Head: "dev", Base: "qa"},
				{Head: "qa", Base: "staging"},
				{Head: "staging", Base: "main"},
			}, "1-3:Select ↑↓:Navigate Enter:Select Esc:Back"},
			{"one step offers no number keys", []config.FlowEntry{
				{Head: "dev", Base: "main"},
			}, "↑↓:Navigate Enter:Select Esc:Back"},
		} {
			cfg := testConfig()
			if tc.flows != nil {
				cfg.Flows = tc.flows
			}
			m := Model{screen: ScreenPrTypeSelect, config: cfg}
			if got := hintString(m); got != tc.want {
				t.Errorf("%s\n got %q\nwant %q", tc.name, got, tc.want)
			}
		}
	})

	t.Run("commit review collapses when there is nothing to commit", func(t *testing.T) {
		if got := hintString(Model{screen: ScreenCommitReview}); got != "Esc:Back" {
			t.Errorf("empty = %q", got)
		}
		withCommits := Model{screen: ScreenCommitReview}
		withCommits.commits = []models.CommitInfo{
			models.NewCommitInfo("abc1234", "ATT-1 do a thing", nil),
		}
		want := "Type:Edit title Enter:Create PR Esc:Back"
		if got := hintString(withCommits); got != want {
			t.Errorf("populated\n got %q\nwant %q", got, want)
		}
	})

	t.Run("open PRs collapses when the list is empty", func(t *testing.T) {
		if got := hintString(Model{screen: ScreenViewOpenPrs}); got != "r:Refresh Esc:Back" {
			t.Errorf("empty = %q", got)
		}
	})

	t.Run("toggle labels track state", func(t *testing.T) {
		on := hintString(Model{screen: ScreenViewAllPrs, allPRs: allPRsState{autoRefresh: true}})
		off := hintString(Model{screen: ScreenViewAllPrs})
		if !strings.Contains(on, "Auto-refresh: on") {
			t.Errorf("on = %q", on)
		}
		if !strings.Contains(off, "Auto-refresh: off") {
			t.Errorf("off = %q", off)
		}

		asc := hintString(Model{screen: ScreenViewAllPrs, allPRs: allPRsState{sortAsc: true, entries: []allPREntry{{}}}})
		desc := hintString(Model{screen: ScreenViewAllPrs, allPRs: allPRsState{entries: []allPREntry{{}}}})
		if !strings.Contains(asc, "Sort: oldest") || !strings.Contains(desc, "Sort: newest") {
			t.Errorf("sort labels:\n asc %q\ndesc %q", asc, desc)
		}
	})

	t.Run("actions has three distinct modes", func(t *testing.T) {
		filtering := hintString(Model{screen: ScreenActionsOverview, actions: actionsState{filterActive: true}})
		if filtering != "Type:Filter Esc:Clear" {
			t.Errorf("filtering = %q", filtering)
		}
		right := hintString(Model{screen: ScreenActionsOverview, actions: actionsState{column: 1}})
		if !strings.Contains(right, "←:Runs") || strings.Contains(right, "Space:Pin") {
			t.Errorf("pinned column = %q", right)
		}
		left := hintString(Model{screen: ScreenActionsOverview})
		if !strings.Contains(left, "Space:Pin") || !strings.Contains(left, "/:Filter") {
			t.Errorf("run column = %q", left)
		}
	})

	t.Run("settings switches to input mode", func(t *testing.T) {
		editing := hintString(Model{screen: ScreenSettings, settings: settingsState{editing: true}})
		if editing != "Enter:Save Esc:Cancel" {
			t.Errorf("editing = %q", editing)
		}
		if got := hintString(Model{screen: ScreenSettings}); got != "↑↓:Navigate Enter:Edit / toggle Esc:Back" {
			t.Errorf("browsing = %q", got)
		}
	})
}

func pressKey(m Model, key string) Model {
	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return next.(Model)
}

func TestHelpOverlayToggles(t *testing.T) {
	m := Model{screen: ScreenMainMenu, config: testConfig()}

	if m = pressKey(m, "?"); !m.showHelp {
		t.Fatal("? should open the help overlay")
	}

	// While open, the overlay swallows input so a stray key cannot act on the
	// screen behind it. menuIndex must not move.
	before := m.menuIndex
	m = pressKey(m, "j")
	if !m.showHelp {
		t.Error("an unrelated key should not close the overlay")
	}
	if m.menuIndex != before {
		t.Errorf("input leaked to the screen behind the overlay: menuIndex %d -> %d", before, m.menuIndex)
	}

	if m = pressKey(m, "q"); m.showHelp {
		t.Error("q should close the overlay")
	}
	// ...and closing it must not also quit the app.
	if m.shouldQuit {
		t.Error("q closed the overlay but also quit the app")
	}

	// Esc closes it too.
	m = pressKey(m, "?")
	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if next.(Model).showHelp {
		t.Error("Esc should close the overlay")
	}
}

// Typing "?" into a filter or title should insert it, not open help.
func TestHelpDoesNotOpenDuringTextInput(t *testing.T) {
	for _, s := range []Screen{ScreenTitleInput, ScreenCommitReview} {
		m := Model{screen: s, config: testConfig()}
		if pressKey(m, "?").showHelp {
			t.Errorf("%v: ? opened help while typing", s)
		}
	}
}

// Every screen that accepts input should advertise something; a screen with no
// entry in either table renders a bare status bar, which is how a new screen
// silently ends up undiscoverable.
func TestEveryInteractiveScreenHasHints(t *testing.T) {
	noInput := map[Screen]bool{
		ScreenLoading: true, ScreenCreating: true, ScreenBatchProcessing: true,
		ScreenMerging: true, ScreenUpdating: true, ScreenPullProgress: true,
	}
	for s := ScreenMainMenu; s <= ScreenSettings; s++ {
		if noInput[s] {
			continue
		}
		if len(Model{screen: s}.keyHints()) == 0 {
			t.Errorf("screen %v has no key hints — add it to keys.go", s)
		}
	}
}
