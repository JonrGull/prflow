package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/update"

	tea "github.com/charmbracelet/bubbletea"
)

func settingsModel(t *testing.T) Model {
	t.Helper()
	// Redirect config writes into a temp dir so saving does not touch the real
	// config while testing.
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	return Model{screen: ScreenSettings, config: config.DefaultConfig()}
}

func fieldIndex(t *testing.T, label string) int {
	t.Helper()
	for i, f := range settingsFields {
		if f.Label == label {
			return i
		}
	}
	t.Fatalf("no settings field labelled %q", label)
	return -1
}

// A bad regex must not be saved. Accepting it would silently disable ticket
// extraction until the user noticed tickets had stopped appearing in PR bodies.
func TestSettingsRejectsInvalidTicketPattern(t *testing.T) {
	m := settingsModel(t)
	m.menuIndex = fieldIndex(t, "Ticket pattern")
	original := m.config.Tickets.Pattern

	m.settings.editing = true
	m.settings.editValue = "ATT-[0-9" // unclosed class
	m.commitSettingsEdit()

	if m.config.Tickets.Pattern != original {
		t.Errorf("pattern changed to %q despite being invalid", m.config.Tickets.Pattern)
	}
	if m.settings.success {
		t.Error("an invalid pattern should not report success")
	}
	if m.settings.feedback == "" {
		t.Error("an invalid pattern should explain itself")
	}
	// The compiled regex must still match the pattern that is actually set.
	if m.config.TicketRegex() == nil {
		t.Error("ticket extraction was left disabled after a rejected edit")
	}
}

func TestSettingsAcceptsValidEdits(t *testing.T) {
	m := settingsModel(t)
	m.menuIndex = fieldIndex(t, "Ticket pattern")

	m.settings.editing = true
	m.settings.editValue = "PROJ-[0-9]+"
	m.commitSettingsEdit()

	if got := m.config.Tickets.Pattern; got != "PROJ-[0-9]+" {
		t.Errorf("pattern = %q, want PROJ-[0-9]+", got)
	}
	if !m.settings.success {
		t.Errorf("expected success, got feedback %q", m.settings.feedback)
	}
	// The derived regex must track the new pattern, not the old one.
	if re := m.config.TicketRegex(); re == nil || !re.MatchString("PROJ-42") {
		t.Error("compiled regex did not follow the pattern change")
	}
}

// An empty pattern is a meaningful setting, not a mistake: it turns extraction
// off. It must be accepted rather than rejected as invalid.
func TestSettingsEmptyPatternDisablesExtraction(t *testing.T) {
	m := settingsModel(t)
	m.menuIndex = fieldIndex(t, "Ticket pattern")

	m.settings.editing = true
	m.settings.editValue = ""
	m.commitSettingsEdit()

	if m.config.Tickets.Pattern != "" {
		t.Errorf("pattern = %q, want empty", m.config.Tickets.Pattern)
	}
	if m.config.TicketRegex() != nil {
		t.Error("an empty pattern should leave no compiled regex")
	}
	if !m.settings.success {
		t.Error("an empty pattern is valid and should report success")
	}
}

func TestSettingsToggles(t *testing.T) {
	m := settingsModel(t)
	m.menuIndex = fieldIndex(t, "QA tagging")
	before := m.config.Tickets.QaTagging

	m.activateSettingsField()

	if m.config.Tickets.QaTagging == before {
		t.Error("toggling did not flip the value")
	}
	if m.settings.editing {
		t.Error("a toggle should not open the text editor")
	}
}

// Changing a path must refresh the diagnostics, or the settings screen would go
// on reporting a problem the user has just fixed (or miss one they introduced).
func TestSettingsRefreshesDiagnostics(t *testing.T) {
	m := settingsModel(t)
	m.menuIndex = fieldIndex(t, "Repo directory")

	m.settings.editing = true
	m.settings.editValue = "/definitely/not/a/real/path"
	m.commitSettingsEdit()

	if len(m.configDiagnostics) == 0 {
		t.Fatal("expected a diagnostic for a missing directory")
	}
	if !config.HasErrors(m.configDiagnostics) {
		t.Errorf("expected an error-severity diagnostic, got %v", m.configDiagnostics)
	}
}

// The global keys are handled before the per-screen dispatch, so a screen with a
// text input has to declare itself in isTextInputActive or they eat the
// keystrokes. ScreenSettings did not, and the field that suffers is the one most
// likely to be typed by hand: the ticket pattern wants a regex, and the
// documented default contains a [.
func TestSettingsEditingSwallowsGlobalKeys(t *testing.T) {
	typeRune := func(m Model, r rune) Model {
		next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		return next.(Model)
	}

	t.Run("the documented pattern can be typed", func(t *testing.T) {
		m := settingsModel(t)
		m.menuIndex = fieldIndex(t, "Ticket pattern")
		m.settings.editing = true
		m.settings.editValue = ""

		const want = "ATT-[0-9]+"
		for _, r := range want {
			m = typeRune(m, r)
		}

		if m.screen != ScreenSettings {
			t.Fatalf("typing %q navigated to %v — [ was taken as a tab switch", want, m.screen)
		}
		if m.settings.editValue != want {
			t.Errorf("editValue = %q, want %q", m.settings.editValue, want)
		}
	})

	// ? and F are the other two global keys, and both are legal in a Linear org
	// slug or a repo name.
	for _, r := range []rune{'?', 'F', '[', ']'} {
		t.Run(string(r), func(t *testing.T) {
			m := settingsModel(t)
			m.menuIndex = fieldIndex(t, "Ticket pattern")
			m.settings.editing = true

			m = typeRune(m, r)

			if m.showHelp {
				t.Errorf("%q opened the help overlay mid-edit", r)
			}
			if m.fullscreen {
				t.Errorf("%q toggled fullscreen mid-edit", r)
			}
			if m.screen != ScreenSettings {
				t.Errorf("%q navigated to %v mid-edit", r, m.screen)
			}
			if m.settings.editValue != string(r) {
				t.Errorf("editValue = %q, want %q", m.settings.editValue, string(r))
			}
		})
	}

	// The same keys must still work when merely navigating the field list.
	t.Run("still global when not editing", func(t *testing.T) {
		m := settingsModel(t)
		if next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}}); !next.(Model).showHelp {
			t.Error("? should still open help when no field is being edited")
		}
	})
}

// saveConfig surfaced write failures in settings.feedback, and then
// commitSettingsEdit overwrote that with "<field> saved" — so the commit that
// set out to stop silent save failures was defeated by its own caller. The same
// held for the list editor's "Saved"/"Deleted" and for first-run leaving the
// setup screen.
func TestSaveFailureIsNotReportedAsSuccess(t *testing.T) {
	// Config.Save resolves its path through os.UserConfigDir, which honours
	// XDG_CONFIG_HOME. Pointing that inside a regular file makes the MkdirAll
	// fail, which is the same shape as a read-only directory or a full disk.
	unwritable := func(t *testing.T) {
		t.Helper()
		blocker := filepath.Join(t.TempDir(), "blocker")
		if err := os.WriteFile(blocker, []byte("not a directory"), 0644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(blocker, "config"))
	}

	t.Run("settings field", func(t *testing.T) {
		m := settingsModel(t)
		unwritable(t)
		m.menuIndex = fieldIndex(t, "Linear org")

		m.settings.editing = true
		m.settings.editValue = "somewhere"
		m.commitSettingsEdit()

		if m.settings.success {
			t.Error("a failed write reported success")
		}
		if !strings.Contains(m.settings.feedback, "failed") {
			t.Errorf("feedback = %q, want it to name the failure", m.settings.feedback)
		}
	})

	t.Run("list editor", func(t *testing.T) {
		m := listModel(t, listColumnsLeft)
		unwritable(t)

		m.list.row = 0
		m.list.rows[0][0] = "Frontend"
		m.commitListRows()

		if m.list.success {
			t.Errorf("a failed write reported success: %q", m.list.feedback)
		}
	})

	t.Run("first run stays put", func(t *testing.T) {
		m := Model{screen: ScreenFirstRun, config: config.DefaultConfig()}
		unwritable(t)
		m.firstRun.value = "/tmp"
		m.firstRun.preview = firstRunPreview{Path: "/tmp", Ran: true, Repos: testRepos()}

		next, _ := m.handleFirstRunKey(tea.KeyMsg{Type: tea.KeyEnter})
		got := next.(Model)

		if got.screen != ScreenFirstRun {
			t.Error("first run left the setup screen after a failed write")
		}
		if got.firstRun.preview.Err == nil {
			t.Error("first run did not report why it stayed")
		}
	})
}

// --dry-run promises to "simulate operations without making changes", and the
// config editors are the main thing anyone would use it to try — but nothing
// stopped them rewriting the real prflow.toml, which also discards whatever
// comments the user had put in it.
func TestDryRunDoesNotWriteTheConfig(t *testing.T) {
	// configPath is os.UserConfigDir + prflow.toml, so this is where a write
	// would land.
	configFile := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", dir)
		t.Setenv("HOME", dir)
		return filepath.Join(dir, "prflow.toml")
	}

	t.Run("settings field", func(t *testing.T) {
		path := configFile(t)
		m := Model{screen: ScreenSettings, config: config.DefaultConfig(), dryRun: true}
		m.menuIndex = fieldIndex(t, "Linear org")

		m.settings.editing = true
		m.settings.editValue = "somewhere"
		m.commitSettingsEdit()

		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("a dry run wrote %s", path)
		}
		if !strings.Contains(m.settings.feedback, "not written") {
			t.Errorf("feedback = %q, want it to say the config was not written", m.settings.feedback)
		}
		// The in-memory change still stands, so the screen keeps working.
		if m.config.Tickets.LinearOrg != "somewhere" {
			t.Error("the dry run also discarded the in-memory change")
		}
	})

	t.Run("list editor", func(t *testing.T) {
		path := configFile(t)
		m := Model{config: config.DefaultConfig(), dryRun: true}
		m.openListEditor(listColumnsLeft)

		m.list.row = 0
		m.list.rows[0][0] = "Frontend"
		m.commitListRows()

		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("a dry run wrote %s", path)
		}
		if !strings.Contains(m.list.feedback, "not written") {
			t.Errorf("feedback = %q, want it to say the config was not written", m.list.feedback)
		}
		// A dry run is not a failure and must not render as a red ✗.
		if !m.list.success {
			t.Error("a dry run was reported as a failure")
		}
	})

	// The setup screen must not strand the user: it writes nothing, but the
	// directory still holds for the rest of the session.
	t.Run("first run still continues", func(t *testing.T) {
		path := configFile(t)
		m := Model{screen: ScreenFirstRun, config: config.DefaultConfig(), dryRun: true}
		m.firstRun.value = "/tmp"
		m.firstRun.preview = firstRunPreview{Path: "/tmp", Ran: true, Repos: testRepos()}

		next, _ := m.handleFirstRunKey(tea.KeyMsg{Type: tea.KeyEnter})
		got := next.(Model)

		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("a dry run wrote %s", path)
		}
		if got.screen != ScreenMainMenu {
			t.Errorf("screen = %v, want MainMenu — a dry run must not trap the user in setup", got.screen)
		}
	})

	// Without the flag the write still happens, or the guard has gone too far.
	t.Run("a real run still writes", func(t *testing.T) {
		path := configFile(t)
		m := Model{screen: ScreenSettings, config: config.DefaultConfig()}
		m.menuIndex = fieldIndex(t, "Linear org")

		m.settings.editing = true
		m.settings.editValue = "somewhere"
		m.commitSettingsEdit()

		if _, err := os.Stat(path); err != nil {
			t.Errorf("a real run did not write the config: %v", err)
		}
		if !m.settings.success {
			t.Errorf("a real run reported failure: %q", m.settings.feedback)
		}
	})
}

// --dry-run says it makes no changes, and the ways it broke that promise were
// all separate: the config, the history file, the update state, and the binary
// itself. This holds the whole promise in one place, because the next thing
// that writes something will not be any of these four.
func TestDryRunTouchesNothing(t *testing.T) {
	// A dev build reports Version "dev", which CheckForUpdate treats as older
	// than every release — so the update prompt always appears, and accepting it
	// renames a download over the binary being tested.
	t.Run("the manual update key does not check", func(t *testing.T) {
		m := Model{screen: ScreenMainMenu, config: config.DefaultConfig(), dryRun: true}
		next, cmd := m.handleMainMenuKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
		got := next.(Model)

		if cmd != nil {
			t.Error("a dry run started an update check")
		}
		if got.updateCheckInProgress {
			t.Error("a dry run marked an update check in progress")
		}
		if !strings.Contains(got.copyFeedback, "dry run") {
			t.Errorf("copyFeedback = %q, want it to say why nothing happened", got.copyFeedback)
		}
	})

	// --test-update puts the prompt on screen without a check, so the download
	// needs its own guard.
	t.Run("accepting an update does not download", func(t *testing.T) {
		m := Model{screen: ScreenUpdatePrompt, config: config.DefaultConfig(), dryRun: true}
		m.updateAvailable = &update.Release{TagName: "v99.0.0"}
		m.updateSelection = 0

		next, cmd := m.executeUpdateSelection()
		got := next.(Model)

		if cmd != nil {
			t.Error("a dry run started a download — this replaces the running binary")
		}
		if got.screen == ScreenUpdating {
			t.Error("a dry run entered the updating screen")
		}
	})

	// The fixtures return successful creates and merges, so the ordinary result
	// handlers record them; without a guard they land in the real history file.
	t.Run("simulated PRs stay out of the history file", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", dir)
		t.Setenv("HOME", dir)

		m := Model{config: config.DefaultConfig(), dryRun: true}
		m.recordSessionPR("Frontend/web", "https://example.test/pull/1", "dev → staging", "created", 1)

		if len(m.sessionPRs) != 1 {
			t.Error("the entry should still exist for this session's history screen")
		}
		if entries, _ := filepath.Glob(filepath.Join(dir, "*history*")); len(entries) > 0 {
			t.Errorf("a dry run wrote %v", entries)
		}
	})

	// Set only handles clearing for this field; Submit is what applies a name.
	// Returning early on errDryRun skipped it, so the edit did nothing at all.
	t.Run("a QA person edit still takes effect in memory", func(t *testing.T) {
		m := settingsModel(t)
		m.dryRun = true
		m.menuIndex = fieldIndex(t, "QA person")

		m.settings.editing = true
		m.settings.editValue = "some.person"
		cmd := m.commitSettingsEdit()

		if cmd == nil {
			t.Fatal("the lookup command was skipped, so the edit had no effect at all")
		}
		if !m.settings.looking {
			t.Error("the field did not enter its looking-up state")
		}
	})
}

// Fitting the height is only half of it: the field under the cursor has to be
// one of the ones drawn. Otherwise navigating past the bottom of the window
// moves an invisible cursor, which is what the screen did before it had one.
func TestSettingsWindowFollowsCursor(t *testing.T) {
	total := len(settingsFields)

	for _, visible := range []int{1, 2, 3, 5, total, total + 4} {
		for cursor := 0; cursor < total; cursor++ {
			first, last := settingsWindow(cursor, total, visible)
			if first < 0 || last > total || first > last {
				t.Fatalf("visible %d cursor %d: window [%d,%d) out of range", visible, cursor, first, last)
			}
			if cursor < first || cursor >= last {
				t.Errorf("visible %d: cursor %d outside window [%d,%d)", visible, cursor, first, last)
			}
			if want := min(visible, total); last-first != want {
				t.Errorf("visible %d cursor %d: window holds %d fields, want %d", visible, cursor, last-first, want)
			}
		}
	}
}

// The last field has to be reachable and rendered at a realistic terminal size —
// "Update repo" was simply off the bottom before, on anything under ~50 rows.
func TestSettingsLastFieldIsVisible(t *testing.T) {
	m := settingsModel(t)
	m.width, m.height = 120, 24
	m.menuIndex = len(settingsFields) - 1

	out := m.renderSettingsWithHeight(minContentHeight)
	if last := settingsFields[len(settingsFields)-1].Label; !strings.Contains(stripANSI(out), last) {
		t.Errorf("field %q is not rendered when it is the one selected", last)
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Every field must be operable: a text field with no Set could be edited but
// never saved, and a toggle with no reader cannot render its state.
func TestSettingsFieldsAreWellFormed(t *testing.T) {
	for _, f := range settingsFields {
		if f.Label == "" || f.Desc == "" {
			t.Errorf("field %+v is missing a label or description", f)
		}
		if f.isToggle() {
			if f.Bool == nil {
				t.Errorf("%s: toggle without a Bool reader", f.Label)
			}
			continue
		}
		if f.isList() {
			// List fields open their own editor, so they need a summary to
			// display and a descriptor to open — but no inline Set.
			if f.Get == nil {
				t.Errorf("%s: list field without a Get for its summary", f.Label)
			}
			if _, ok := listSettings[f.Opens]; !ok {
				t.Errorf("%s: Opens %q has no listSetting", f.Label, f.Opens)
			}
			continue
		}
		if f.Get == nil {
			t.Errorf("%s: text field without a Get", f.Label)
		}
		if f.Set == nil {
			t.Errorf("%s: text field without a Set — it could be edited but never saved", f.Label)
		}
	}
}
