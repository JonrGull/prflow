package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/models"
	"github.com/JonrGull/prflow/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
)

// Editing the list-valued settings.
//
// [[globs]], [[repos]] and the two column assignments were the last reason to
// leave the app and open the TOML — and they are exactly the settings the
// diagnostics complain about, so the app could tell you a group was assigned to
// no column and then offer no way to fix it.
//
// All four are the same shape underneath — a table of rows, each row one or two
// text cells — so one editor covers them rather than four bespoke screens.

// listState is the row editor's cursor and buffer.
type listState struct {
	id            string // which listSetting is open
	rows          [][]string
	row           int
	cell          int
	editing       bool
	editValue     string
	pendingDelete bool // a first d press has armed the delete
	feedback      string
	success       bool
}

// listSetting describes one list-valued config setting as a table of rows.
type listSetting struct {
	Title string
	Help  string
	Cells []string // one header per cell

	Load  func(*config.Config) [][]string
	Store func(*config.Config, [][]string)
	Blank func() []string

	// Validate rejects a row before it is stored.
	Validate func(*config.Config, []string) error

	// Hint is a context line rendered under the help text. For the column
	// lists it names the groups that actually exist, which is the information
	// needed to avoid the typo the diagnostics would otherwise report later.
	Hint func(*config.Config) string
}

const (
	listColumnsLeft  = "columns.left"
	listColumnsRight = "columns.right"
	listGlobs        = "globs"
	listRepos        = "repos"
	listFlows        = "flows"
)

func knownGroupsHint(c *config.Config) string {
	groups := c.KnownGroups()
	if len(groups) == 0 {
		return "No groups exist yet — add a glob or a repo first."
	}
	return "Groups in use: " + strings.Join(groups, ", ")
}

// columnSetting builds the descriptor for one of the two column lists, which
// differ only in which slice they read and write.
func columnSetting(title, help string, get func(*config.Config) []string, set func(*config.Config, []string)) listSetting {
	return listSetting{
		Title: title,
		Help:  help,
		Cells: []string{"Group"},
		Load: func(c *config.Config) [][]string {
			rows := make([][]string, 0, len(get(c)))
			for _, g := range get(c) {
				rows = append(rows, []string{g})
			}
			return rows
		},
		Store: func(c *config.Config, rows [][]string) {
			names := make([]string, 0, len(rows))
			for _, r := range rows {
				if strings.TrimSpace(r[0]) != "" {
					names = append(names, strings.TrimSpace(r[0]))
				}
			}
			set(c, names)
		},
		Blank: func() []string { return []string{""} },
		Validate: func(c *config.Config, row []string) error {
			if strings.TrimSpace(row[0]) == "" {
				return fmt.Errorf("a group name is required")
			}
			return nil
		},
		Hint: knownGroupsHint,
	}
}

var listSettings = map[string]listSetting{
	listColumnsLeft: columnSetting(
		"Left column groups",
		"Groups shown in the left-hand column. Anything not listed here goes right.",
		func(c *config.Config) []string { return c.Columns.Left },
		func(c *config.Config, v []string) { c.Columns.Left = v },
	),

	listColumnsRight: columnSetting(
		"Right column groups",
		"Groups shown in the right-hand column.",
		func(c *config.Config) []string { return c.Columns.Right },
		func(c *config.Config, v []string) { c.Columns.Right = v },
	),

	listGlobs: {
		Title: "Repo globs",
		Help:  "Patterns searched under the repo directory, each assigned to a group.",
		Cells: []string{"Pattern", "Group"},
		Load: func(c *config.Config) [][]string {
			rows := make([][]string, 0, len(c.Globs))
			for _, g := range c.Globs {
				rows = append(rows, []string{g.Pattern, g.Group})
			}
			return rows
		},
		Store: func(c *config.Config, rows [][]string) {
			globs := make([]config.GlobEntry, 0, len(rows))
			for _, r := range rows {
				if strings.TrimSpace(r[0]) == "" {
					continue
				}
				globs = append(globs, config.GlobEntry{
					Pattern: strings.TrimSpace(r[0]),
					Group:   strings.TrimSpace(r[1]),
				})
			}
			c.Globs = globs
		},
		Blank: func() []string { return []string{"", ""} },
		Validate: func(c *config.Config, row []string) error {
			if strings.TrimSpace(row[0]) == "" {
				return fmt.Errorf("a pattern is required")
			}
			if strings.TrimSpace(row[1]) == "" {
				return fmt.Errorf("a group is required, or the repos cannot be assigned to a column")
			}
			return nil
		},
		Hint: knownGroupsHint,
	},

	listFlows: {
		Title: "Release steps",
		Help:  "Each step is one PR: HEAD merged into BASE, top to bottom.",
		Cells: []string{"Head", "Base", "PR title"},
		Load: func(c *config.Config) [][]string {
			rows := make([][]string, 0, len(c.Flows))
			for _, f := range c.Flows {
				rows = append(rows, []string{f.Head, f.Base, f.Title})
			}
			return rows
		},
		Store: func(c *config.Config, rows [][]string) {
			flows := make([]config.FlowEntry, 0, len(rows))
			for _, r := range rows {
				if strings.TrimSpace(r[0]) == "" || strings.TrimSpace(r[1]) == "" {
					continue
				}
				flows = append(flows, config.FlowEntry{
					Head:  strings.TrimSpace(r[0]),
					Base:  strings.TrimSpace(r[1]),
					Title: r[2], // kept verbatim: a trailing space is a prefix
				})
			}
			c.Flows = flows
		},
		Blank: func() []string { return []string{"", "", ""} },
		Validate: func(c *config.Config, row []string) error {
			head, base := strings.TrimSpace(row[0]), strings.TrimSpace(row[1])
			if head == "" {
				return fmt.Errorf("a head branch is required")
			}
			if base == "" {
				return fmt.Errorf("a base branch is required (%s for the repo default)", models.DefaultBranchToken)
			}
			if head == base {
				return fmt.Errorf("a step cannot merge %s into itself", head)
			}
			return nil
		},
		Hint: func(c *config.Config) string {
			return "Base " + models.DefaultBranchToken + " means each repo's own default branch."
		},
	},

	listRepos: {
		Title: "Explicit repos",
		Help:  "Individual repositories outside the repo directory.",
		Cells: []string{"Path", "Group"},
		Load: func(c *config.Config) [][]string {
			rows := make([][]string, 0, len(c.Repos))
			for _, r := range c.Repos {
				rows = append(rows, []string{r.Path, r.Group})
			}
			return rows
		},
		Store: func(c *config.Config, rows [][]string) {
			repos := make([]config.RepoEntry, 0, len(rows))
			for _, r := range rows {
				if strings.TrimSpace(r[0]) == "" {
					continue
				}
				repos = append(repos, config.RepoEntry{
					Path:  strings.TrimSpace(r[0]),
					Group: strings.TrimSpace(r[1]),
				})
			}
			c.Repos = repos
		},
		Blank: func() []string { return []string{"", ""} },
		Validate: func(c *config.Config, row []string) error {
			if strings.TrimSpace(row[0]) == "" {
				return fmt.Errorf("a path is required")
			}
			return nil
		},
		Hint: knownGroupsHint,
	},
}

// openListEditor switches to the editor for the named list.
func (m *Model) openListEditor(id string) {
	setting, ok := listSettings[id]
	if !ok {
		return
	}
	m.list.id = id
	m.list.rows = setting.Load(m.config)
	m.list.row, m.list.cell = 0, 0
	m.list.editing = false
	m.list.editValue = ""
	m.list.pendingDelete = false
	m.list.feedback = ""
	m.screen = ScreenListEdit
}

func (m Model) listSetting() (listSetting, bool) {
	s, ok := listSettings[m.list.id]
	return s, ok
}

// commitListRows validates the focused row, writes the table back to the config
// and persists it.
func (m *Model) commitListRows() {
	setting, ok := m.listSetting()
	if !ok {
		return
	}

	// Every row is checked, not just the one being edited: Store writes the
	// whole table, so validating only the focused row let an abandoned
	// half-typed entry ride along with an unrelated later edit — add a glob,
	// leave its group blank, dismiss the error, then edit a different row, and
	// the group-less glob reached the config.
	if err := m.validateListRows(setting); err != nil {
		m.list.feedback = err.Error()
		m.list.success = false
		return
	}

	setting.Store(m.config, m.list.rows)
	if err := m.applySettingsChange(); err != nil {
		m.list.feedback = err.Error()
		// A dry run is not a failure, so it must not render as a red ✗.
		m.list.success = errors.Is(err, errDryRun)
		return
	}
	m.list.feedback = "Saved"
	m.list.success = true
}

// validateListRows checks every row that has anything in it, naming the offender
// so a complaint about row 7 is not a mystery while the cursor sits on row 1.
//
// A wholly blank row is skipped: it is a row the user added and has not filled
// in yet, and Store drops it anyway.
func (m Model) validateListRows(setting listSetting) error {
	if setting.Validate == nil {
		return nil
	}
	for i, row := range m.list.rows {
		if isBlankRow(row) {
			continue
		}
		if err := setting.Validate(m.config, row); err != nil {
			return fmt.Errorf("row %d (%s): %w", i+1, rowLabel(row), err)
		}
	}
	return nil
}

func isBlankRow(row []string) bool {
	for _, c := range row {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

// rowLabel names a row by its first non-empty cell, which is the pattern or path
// the user typed.
func rowLabel(row []string) string {
	for _, c := range row {
		if v := strings.TrimSpace(c); v != "" {
			return v
		}
	}
	return "blank"
}

func (m Model) handleListEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	setting, ok := m.listSetting()
	if !ok {
		m.screen = ScreenSettings
		return m, nil
	}

	if m.list.editing {
		switch msg.Type {
		case tea.KeyEnter:
			m.list.rows[m.list.row][m.list.cell] = strings.TrimSpace(m.list.editValue)
			m.list.editing = false
			m.commitListRows()
		case tea.KeyEsc:
			m.list.editing = false
			m.list.editValue = ""
		case tea.KeyBackspace:
			m.list.editValue = trimLastRune(m.list.editValue)
		case tea.KeySpace:
			m.list.editValue += " "
		case tea.KeyRunes:
			m.list.editValue += string(msg.Runes)
		}
		return m, nil
	}

	// Any key other than a second d cancels a pending delete, so the guard
	// cannot be left armed and fire on a later, unrelated press.
	pendingDelete := m.list.pendingDelete
	m.list.pendingDelete = false

	switch msg.String() {
	case "up", "k":
		m.list.feedback = ""
		if len(m.list.rows) > 0 {
			navigateColumnIndex(&m.list.row, len(m.list.rows), true)
			m.list.cell = 0
		}
	case "down", "j":
		m.list.feedback = ""
		if len(m.list.rows) > 0 {
			navigateColumnIndex(&m.list.row, len(m.list.rows), false)
			m.list.cell = 0
		}
	case "left", "h":
		if m.list.cell > 0 {
			m.list.cell--
		}
	case "right", "l":
		if m.list.cell < len(setting.Cells)-1 {
			m.list.cell++
		}
	case "enter":
		if len(m.list.rows) == 0 {
			return m, nil
		}
		m.list.editing = true
		m.list.editValue = m.list.rows[m.list.row][m.list.cell]
		m.list.feedback = ""
	case "a":
		m.list.rows = append(m.list.rows, setting.Blank())
		m.list.row = len(m.list.rows) - 1
		m.list.cell = 0
		m.list.editing = true
		m.list.editValue = ""
		m.list.feedback = ""
	case "d":
		if len(m.list.rows) == 0 {
			return m, nil
		}
		if !pendingDelete {
			// Deleting is the one action here that retyping cannot undo, and d
			// sits next to the navigation keys — so make it take two presses.
			m.list.pendingDelete = true
			m.list.feedback = fmt.Sprintf("Press d again to delete %q", m.list.rows[m.list.row][0])
			m.list.success = false
			return m, nil
		}
		m.list.rows = append(m.list.rows[:m.list.row], m.list.rows[m.list.row+1:]...)
		if m.list.row >= len(m.list.rows) && m.list.row > 0 {
			m.list.row--
		}
		m.list.cell = 0
		// Deleting stores the whole table too, so it needs the same check —
		// otherwise removing a good row was a way to persist a bad one.
		if err := m.validateListRows(setting); err != nil {
			m.list.feedback = err.Error()
			m.list.success = false
			return m, nil
		}
		setting.Store(m.config, m.list.rows)
		if err := m.applySettingsChange(); err != nil {
			m.list.feedback = err.Error()
			m.list.success = errors.Is(err, errDryRun)
			return m, nil
		}
		m.list.feedback = "Deleted"
		m.list.success = true
	case "esc":
		m.screen = ScreenSettings
		m.list.feedback = ""
	}
	return m, nil
}

func (m Model) renderListEditWithHeight(availableHeight int) string {
	setting, ok := m.listSetting()
	if !ok {
		return ""
	}

	// Everything that is not a row, built first so the rows can be given what is
	// actually left rather than what a constant guesses is left.
	head := []string{ui.Dim.Render("  " + setting.Help)}
	if setting.Hint != nil {
		head = append(head, ui.Dim.Render("  "+setting.Hint(m.config)))
	}

	var tail []string
	if m.list.feedback != "" {
		// An armed delete is a prompt, not a failure — rendering it in red like a
		// rejected edit would misread as "something went wrong".
		icon, style := "✗", ui.Red
		switch {
		case m.list.pendingDelete:
			icon, style = "⚠", ui.Yellow
		case m.list.success:
			icon, style = "✓", ui.Green
		}
		tail = append(tail, "", "  "+style.Render(icon+" "+m.list.feedback))
	}

	// The blank lines either side of the preamble are the first thing to give up
	// when the terminal is short; losing them beats overflowing the frame and
	// pushing the status bar off screen.
	//   1 panel title + head + 1 column header + 2 scroll indicators + tail
	budget := availableHeight - len(head) - len(tail) - 4
	spaced := budget >= listMinVisibleRows+2
	if spaced {
		budget -= 2
	}

	lines := []string{}
	if spaced {
		lines = append(lines, "")
	}
	lines = append(lines, head...)
	if spaced {
		lines = append(lines, "")
	}

	if len(m.list.rows) == 0 {
		lines = append(lines,
			ui.Yellow.Render("  Nothing configured yet."),
			ui.Dim.Render("  Press a to add the first entry."))
	} else {
		lines = append(lines, m.renderListHeader(setting))
		first, last := m.listWindow(budget)
		if first > 0 {
			lines = append(lines, ui.Dim.Render(fmt.Sprintf("      ↑ %d more above", first)))
		}
		for i := first; i < last; i++ {
			lines = append(lines, m.renderListRow(setting, i))
		}
		if last < len(m.list.rows) {
			lines = append(lines, ui.Dim.Render(fmt.Sprintf("      ↓ %d more below", len(m.list.rows)-last)))
		}
	}

	lines = append(lines, tail...)

	return panel(ui.CyanBold, setting.Title, lines)
}

// listMinVisibleRows is the floor on how much of the list is shown. A single row
// is still navigable; below that there is nothing to steer by.
const listMinVisibleRows = 2

// listWindow returns the half-open range of rows to draw, scrolled to keep the
// cursor visible. Without this a long [[repos]] list would simply run off the
// bottom, and the rows past the edge would be unreachable — which would defeat
// the point of not having to open the file.
//
// visible is the row budget renderListEditWithHeight has left after laying out
// everything else. It is passed in rather than derived from the screen height a
// second time here: a constant counting the renderer's own chrome went stale the
// moment the layout changed, and a 25-line terminal showing feedback overflowed
// the frame by a line.
func (m Model) listWindow(visible int) (first, last int) {
	if visible < listMinVisibleRows {
		visible = listMinVisibleRows
	}
	if visible >= len(m.list.rows) {
		return 0, len(m.list.rows)
	}

	// Centre on the cursor, then clamp to the ends so the last page stays full.
	first = m.list.row - visible/2
	if first > len(m.list.rows)-visible {
		first = len(m.list.rows) - visible
	}
	if first < 0 {
		first = 0
	}
	return first, first + visible
}

// listCellWidth keeps the columns aligned. Two cells at this width is what the
// row was fixed at; a list with more cells divides the same total rather than
// growing the row past the frame.
const listCellWidth = 34

func listCellWidthFor(cells int) int {
	if cells <= 2 {
		return listCellWidth
	}
	return 2*(listCellWidth+2)/cells - 2
}

func (m Model) renderListHeader(setting listSetting) string {
	width := listCellWidthFor(len(setting.Cells))
	header := "     "
	for _, c := range setting.Cells {
		header += visPad(ui.Dim.Render(strings.ToUpper(c)), width+2)
	}
	return header
}

func (m Model) renderListRow(setting listSetting, i int) string {
	row := m.list.rows[i]
	selected := i == m.list.row
	width := listCellWidthFor(len(setting.Cells))

	out := "  " + ui.Arrow(selected && !m.list.editing)
	for cell := range setting.Cells {
		value := ""
		if cell < len(row) {
			value = row[cell]
		}

		focused := selected && cell == m.list.cell
		if focused && m.list.editing {
			out += visPad(ui.WhiteBold.Render(m.list.editValue)+ui.Cyan.Render("█"), width+2)
			continue
		}

		if value == "" {
			value = "—"
		}
		style := ui.Dim
		switch {
		case focused:
			style = ui.CyanBold
		case selected:
			style = ui.White
		}
		out += visPad(style.Render(truncateString(value, width)), width+2)
	}
	return out
}
