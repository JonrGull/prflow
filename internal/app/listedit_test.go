package app

import (
	"testing"

	"github.com/JonrGull/prflow/internal/config"

	tea "github.com/charmbracelet/bubbletea"
)

func listModel(t *testing.T, id string) Model {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	m := Model{config: config.DefaultConfig()}
	m.openListEditor(id)
	return m
}

func listKey(m Model, s string) Model {
	var msg tea.KeyMsg
	switch s {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	next, _ := m.handleListEditKey(msg)
	return next.(Model)
}

func typeInto(m Model, s string) Model {
	for _, r := range s {
		m = listKey(m, string(r))
	}
	return m
}

func TestListEditorLoadsAndStores(t *testing.T) {
	m := listModel(t, listGlobs)
	if m.screen != ScreenListEdit {
		t.Fatalf("screen = %v, want ListEdit", m.screen)
	}
	// DefaultConfig ships two globs.
	if len(m.list.rows) != 2 {
		t.Fatalf("loaded %d rows, want 2", len(m.list.rows))
	}
	if m.list.rows[0][0] != "frontend/*" || m.list.rows[0][1] != "Frontend" {
		t.Errorf("row 0 = %v", m.list.rows[0])
	}

	// Edit the group cell of the first row.
	m = listKey(m, "right")
	if m.list.cell != 1 {
		t.Fatalf("cell = %d, want 1", m.list.cell)
	}
	m = listKey(m, "enter")
	if !m.list.editing {
		t.Fatal("Enter should open the cell for editing")
	}
	m.list.editValue = "Web"
	m = listKey(m, "enter")

	if got := m.config.Globs[0].Group; got != "Web" {
		t.Errorf("stored group = %q, want Web", got)
	}
	if !m.list.success {
		t.Errorf("expected success, got %q", m.list.feedback)
	}
}

// A row missing a required cell must not reach the config — a half-typed entry
// should not silently become a config line.
func TestListEditorRejectsIncompleteRow(t *testing.T) {
	m := listModel(t, listGlobs)
	before := len(m.config.Globs)

	m = listKey(m, "a") // add opens an empty row for editing
	if !m.list.editing {
		t.Fatal("adding a row should open it for editing")
	}
	m.list.editValue = "services/*"
	m = listKey(m, "enter") // commits the pattern, group still blank

	if m.list.success {
		t.Error("a row with no group should not report success")
	}
	if m.list.feedback == "" {
		t.Error("a rejected row should explain why")
	}
	if len(m.config.Globs) != before {
		t.Errorf("incomplete row was stored: %+v", m.config.Globs)
	}
	// The row stays on screen so the user can finish it.
	if len(m.list.rows) != before+1 {
		t.Errorf("the incomplete row should stay visible, got %d rows", len(m.list.rows))
	}
}

// Store persists the whole table, so validating only the focused row let an
// abandoned partial row ride along with an unrelated later edit. This is the
// sequence: add a glob, fill in the pattern, leave the group blank, dismiss the
// complaint, then go and edit a row that was already fine.
func TestListEditorWillNotSmuggleAPartialRow(t *testing.T) {
	m := listModel(t, listGlobs)
	before := len(m.config.Globs)

	m = listKey(m, "a")
	m.list.editValue = "services/*"
	m = listKey(m, "enter") // rejected: no group
	if len(m.config.Globs) != before {
		t.Fatalf("setup: the partial row was stored immediately")
	}

	// Move to an existing, complete row and re-save it.
	m.list.row, m.list.cell = 0, 1
	m = listKey(m, "enter")
	m.list.editValue = "Web"
	m = listKey(m, "enter")

	for _, g := range m.config.Globs {
		if g.Pattern == "services/*" {
			t.Errorf("the partial row reached the config: %+v", m.config.Globs)
		}
		if g.Group == "" {
			t.Errorf("a glob with no group reached the config: %+v", g)
		}
	}
	if m.list.success {
		t.Errorf("saving while a row is incomplete should not report success, got %q", m.list.feedback)
	}
	// The complaint has to name the row, or it is a mystery from the cursor.
	if !contains(m.list.feedback, "services/*") {
		t.Errorf("feedback %q does not name the offending row", m.list.feedback)
	}
}

// Deleting stores the table as well, so it needs the same guard: removing a good
// row must not be a way to persist a bad one.
func TestListEditorDeleteAlsoValidates(t *testing.T) {
	m := listModel(t, listGlobs)

	m = listKey(m, "a")
	m.list.editValue = "services/*"
	m = listKey(m, "enter") // partial row parked on screen

	m.list.row = 0
	m = listKey(m, "d")
	m = listKey(m, "d")

	for _, g := range m.config.Globs {
		if g.Group == "" {
			t.Errorf("delete persisted a group-less glob: %+v", m.config.Globs)
		}
	}
}

// Deleting is the one action retyping cannot undo, so it takes two presses.
func TestListEditorDeleteNeedsTwoPresses(t *testing.T) {
	m := listModel(t, listGlobs)
	before := len(m.config.Globs)

	m = listKey(m, "d")
	if !m.list.pendingDelete {
		t.Fatal("first d should arm the delete")
	}
	if len(m.config.Globs) != before {
		t.Fatal("first d deleted immediately")
	}
	if m.list.feedback == "" {
		t.Error("first d should say what will be deleted")
	}

	m = listKey(m, "d")
	if m.list.pendingDelete {
		t.Error("pending flag should clear after the delete")
	}
	if len(m.config.Globs) != before-1 {
		t.Errorf("globs = %d, want %d", len(m.config.Globs), before-1)
	}
}

// An unrelated keypress must disarm it, or the guard could fire much later.
func TestListEditorDeleteGuardIsCancelled(t *testing.T) {
	m := listModel(t, listGlobs)
	before := len(m.config.Globs)

	m = listKey(m, "d")
	m = listKey(m, "down") // moves, and should cancel
	if m.list.pendingDelete {
		t.Fatal("navigating should cancel a pending delete")
	}

	m = listKey(m, "d") // this is a first press again, not a confirm
	if len(m.config.Globs) != before {
		t.Errorf("a stale guard deleted a row: %d globs, want %d", len(m.config.Globs), before)
	}
}

// Editing these lists is exactly when the diagnostics and repo cache go stale.
func TestListEditorRefreshesDiagnostics(t *testing.T) {
	m := listModel(t, listColumnsLeft)
	// DefaultConfig has Frontend left, Backend right — no column diagnostics.
	if hasColumnDiagnostic(m.configDiagnostics) {
		t.Fatalf("setup: unexpected column diagnostics %v", m.configDiagnostics)
	}

	// Remove Frontend from the left column; Backend is still assigned, so
	// Frontend becomes the unassigned group.
	m = listKey(m, "d")
	m = listKey(m, "d")

	if !hasColumnDiagnostic(m.configDiagnostics) {
		t.Errorf("removing a column entry should raise a diagnostic, got %v", m.configDiagnostics)
	}

	// Putting it back should clear it again — the loop the editor exists to close.
	m = listKey(m, "a")
	m.list.editValue = "Frontend"
	m = listKey(m, "enter")

	if hasColumnDiagnostic(m.configDiagnostics) {
		t.Errorf("restoring the group should clear the diagnostic, got %v", m.configDiagnostics)
	}
}

func hasColumnDiagnostic(diags []config.Diagnostic) bool {
	for _, d := range diags {
		if d.Field == "columns" {
			return true
		}
	}
	return false
}

// Single-cell lists must not let the cursor wander into a column that is not there.
func TestListEditorCellNavigationRespectsWidth(t *testing.T) {
	m := listModel(t, listColumnsLeft) // one cell per row
	m = listKey(m, "right")
	if m.list.cell != 0 {
		t.Errorf("cell = %d, want 0 for a single-cell list", m.list.cell)
	}

	m = listModel(t, listGlobs) // two cells
	m = listKey(m, "right")
	m = listKey(m, "right")
	if m.list.cell != 1 {
		t.Errorf("cell = %d, want 1 (clamped to the last cell)", m.list.cell)
	}
	m = listKey(m, "left")
	m = listKey(m, "left")
	if m.list.cell != 0 {
		t.Errorf("cell = %d, want 0", m.list.cell)
	}
}

// A row the cursor is on must always be drawn. If the window can leave it off
// screen the user is editing a row they cannot see.
func TestListEditorWindowKeepsCursorVisible(t *testing.T) {
	m := listModel(t, listRepos)
	m.list.rows = make([][]string, 40)
	for i := range m.list.rows {
		m.list.rows[i] = []string{"path", "group"}
	}

	for _, height := range []int{4, 12, 24, 40, 100} {
		for row := 0; row < len(m.list.rows); row++ {
			m.list.row = row
			first, last := m.listWindow(height)
			if first < 0 || last > len(m.list.rows) || first > last {
				t.Fatalf("height %d row %d: window [%d,%d) is out of range", height, row, first, last)
			}
			if row < first || row >= last {
				t.Errorf("height %d: cursor row %d outside window [%d,%d)", height, row, first, last)
			}
		}
	}

	// A list that fits is shown whole, with no scroll indicators.
	m.list.rows = m.list.rows[:3]
	m.list.row = 0
	if first, last := m.listWindow(40); first != 0 || last != 3 {
		t.Errorf("short list windowed to [%d,%d), want [0,3)", first, last)
	}
}

// The editor has to fit the height it is handed. It used to size itself against
// a constant counting its own chrome, which is a second statement of the layout
// and went wrong the way those do: on a 25-line terminal with a feedback message
// showing it drew 13 lines into 12, pushing the status bar off screen. The
// goldens could not catch that, because they record a single height.
//
// minContentHeight is the smallest height View will ever pass down, so it is the
// bottom of the range worth asserting — and lowering that floor should bring the
// test with it.
func TestListEditorFitsItsHeight(t *testing.T) {
	for _, id := range []string{listGlobs, listRepos, listColumnsLeft, listColumnsRight} {
		m := listModel(t, id)
		m.width, m.height = 120, 40
		setting, _ := m.listSetting()

		m.list.rows = make([][]string, 50)
		for i := range m.list.rows {
			m.list.rows[i] = make([]string, len(setting.Cells))
			for c := range m.list.rows[i] {
				m.list.rows[i][c] = "value"
			}
		}

		for _, height := range []int{minContentHeight, 12, 24, 40} {
			for _, feedback := range []string{"", "Saved"} {
				m.list.feedback = feedback
				// Mid-list, so both scroll indicators are drawn — the widest case.
				m.list.row = len(m.list.rows) / 2
				if got := lineCount(m.renderListEditWithHeight(height)); got > height {
					t.Errorf("%s at height %d (feedback %q): rendered %d lines, overflowing by %d",
						id, height, feedback, got, got-height)
				}
			}
		}
	}
}

// Every descriptor has to be usable, and the hint is the whole point for the
// column lists — it is what prevents the typo the diagnostics would report.
func TestListSettingsAreWellFormed(t *testing.T) {
	cfg := config.DefaultConfig()
	for id, s := range listSettings {
		if s.Title == "" || s.Help == "" {
			t.Errorf("%s: missing title or help", id)
		}
		// Three is the widest the row fits; more would push it past the frame.
		if len(s.Cells) == 0 || len(s.Cells) > 3 {
			t.Errorf("%s: %d cells, expected 1 to 3", id, len(s.Cells))
		}
		if s.Load == nil || s.Store == nil || s.Blank == nil {
			t.Errorf("%s: missing Load/Store/Blank", id)
		}
		if got := len(s.Blank()); got != len(s.Cells) {
			t.Errorf("%s: Blank has %d cells, headers have %d", id, got, len(s.Cells))
		}
		for _, row := range s.Load(cfg) {
			if len(row) != len(s.Cells) {
				t.Errorf("%s: loaded row %v does not match %d headers", id, row, len(s.Cells))
			}
		}
	}

	// The hint names the groups that actually exist.
	hint := knownGroupsHint(cfg)
	for _, g := range cfg.KnownGroups() {
		if !contains(hint, g) {
			t.Errorf("hint %q does not mention known group %q", hint, g)
		}
	}
	if got := knownGroupsHint(&config.Config{}); !contains(got, "No groups") {
		t.Errorf("empty config hint = %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
