package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The settings screen as data.
//
// It used to hardcode exactly two items — the QA toggle and the QA person — so
// every other setting meant closing the app and hand-editing TOML, and the
// screen's navigation bounds were a `settingsCount = 2` constant that had to be
// kept in step by hand. Describing the fields makes adding one a single entry.

// settingsState is the settings screen's inline text editor.
type settingsState struct {
	editing   bool   // a text field is open
	editValue string // what has been typed so far
	looking   bool   // resolving a QA person against Linear
	feedback  string
	success   bool // true = the feedback is a success, false = an error
}

type settingsField struct {
	Label string
	Desc  string

	// Bool and Toggle are set together for on/off fields.
	Bool   func(*config.Config) bool
	Toggle func(*config.Config)

	// Get and Set are set together for text fields. Set may reject a value,
	// in which case the config is left untouched and the error is shown.
	Get func(*config.Config) string
	Set func(*config.Config, string) error

	// Placeholder is shown instead of an empty value.
	Placeholder string

	// EditHint replaces the description while the field is being edited.
	EditHint string

	// Submit runs after a successful Set, for fields needing extra work.
	// Returning a command lets the field kick off an async lookup.
	Submit func(m *Model, value string) tea.Cmd

	// Opens names a listSetting edited on its own screen, for the list-valued
	// settings that need row add/edit/delete rather than a single input.
	// Get still supplies a summary for the row.
	Opens string
}

func (f settingsField) isToggle() bool { return f.Toggle != nil }

func (f settingsField) isList() bool { return f.Opens != "" }

// display renders the field's current value, or its placeholder when empty.
func (f settingsField) display(cfg *config.Config) string {
	if f.Get == nil {
		return ""
	}
	if v := f.Get(cfg); v != "" {
		return v
	}
	if f.Placeholder != "" {
		return f.Placeholder
	}
	return "not set"
}

// settingsFields is the ordered list shown on the settings screen.
//
// The list-valued settings appear here too, but open their own editor rather
// than an inline input — they need row add/edit/delete, and squeezing them into
// comma-separated strings would be worse than the file they replace.
var settingsFields = []settingsField{
	{
		Label:       "Repo directory",
		Desc:        "Where to look for repositories",
		Get:         func(c *config.Config) string { return c.Paths.ReposDir },
		Set:         func(c *config.Config, v string) error { c.Paths.ReposDir = v; return nil },
		Placeholder: "not set",
		EditHint:    "Enter a path, then Enter to save (~ is expanded)",
	},
	{
		Label: "Ticket pattern",
		Desc:  "Regex for pulling ticket IDs out of commits",
		Get:   func(c *config.Config) string { return c.Tickets.Pattern },
		Set:   func(c *config.Config, v string) error { return c.SetTicketPattern(v) },
		// An empty pattern is meaningful: it disables extraction entirely.
		Placeholder: "disabled",
		EditHint:    "A regex, e.g. PROJ-[0-9]+. Empty disables extraction",
	},
	{
		Label:    "Linear org",
		Desc:     "Workspace slug used to build ticket links",
		Get:      func(c *config.Config) string { return c.Tickets.LinearOrg },
		Set:      func(c *config.Config, v string) error { c.Tickets.LinearOrg = v; return nil },
		EditHint: "The slug from linear.app/<org>",
	},
	{
		Label: "Release steps",
		Desc:  "The chain of PRs a release moves through",
		Get:   func(c *config.Config) string { return flowsSummary(c) },
		Opens: listFlows,
	},
	{
		Label: "Repo globs",
		Desc:  "Patterns searched under the repo directory",
		Get:   func(c *config.Config) string { return countLabel(len(c.Globs), "pattern", "patterns") },
		Opens: listGlobs,
	},
	{
		Label: "Explicit repos",
		Desc:  "Repositories outside the repo directory",
		Get:   func(c *config.Config) string { return countLabel(len(c.Repos), "repo", "repos") },
		Opens: listRepos,
	},
	{
		Label: "Left column",
		Desc:  "Groups shown in the left-hand column",
		Get:   func(c *config.Config) string { return joinOrNone(c.Columns.Left) },
		Opens: listColumnsLeft,
	},
	{
		Label: "Right column",
		Desc:  "Groups shown in the right-hand column",
		Get:   func(c *config.Config) string { return joinOrNone(c.Columns.Right) },
		Opens: listColumnsRight,
	},
	{
		Label:  "QA tagging",
		Desc:   "Tag tickets for QA after merging",
		Bool:   func(c *config.Config) bool { return c.Tickets.QaTagging },
		Toggle: func(c *config.Config) { c.Tickets.QaTagging = !c.Tickets.QaTagging },
	},
	{
		Label:    "QA person",
		Desc:     "Linear user to notify on tagged tickets",
		Get:      func(c *config.Config) string { return c.Tickets.QaPerson },
		EditHint: "Enter a display name, then Enter to look up",
		// Cleared directly; a non-empty name is resolved to a UUID by Submit,
		// so Set only handles the clearing case.
		Set: func(c *config.Config, v string) error {
			if v == "" {
				c.Tickets.QaPerson = ""
				c.Tickets.QaPersonID = ""
			}
			return nil
		},
		Submit: func(m *Model, value string) tea.Cmd {
			if value == "" {
				m.settings.feedback = "QA person cleared"
				m.settings.success = true
				return nil
			}
			m.settings.looking = true
			m.settings.feedback = ""
			return lookupQaPersonCmd(m.config.LinearAPIKey(), value, m.dryRun)
		},
	},
	{
		Label:  "Auto-update",
		Desc:   "Check for a new release on startup",
		Bool:   func(c *config.Config) bool { return c.Update.Enabled },
		Toggle: func(c *config.Config) { c.Update.Enabled = !c.Update.Enabled },
	},
	{
		Label:    "Update repo",
		Desc:     "GitHub repo to check for releases",
		Get:      func(c *config.Config) string { return c.Update.Repo },
		Set:      func(c *config.Config, v string) error { c.Update.Repo = v; return nil },
		EditHint: "owner/name",
	},
}

// countLabel renders "3 patterns" / "1 pattern" / "none".
func countLabel(n int, singular, plural string) string {
	switch n {
	case 0:
		return "none"
	case 1:
		return "1 " + singular
	default:
		return fmt.Sprintf("%d %s", n, plural)
	}
}

func joinOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

// activateSettingsField handles Enter or Space on the highlighted field:
// toggles a boolean, or opens the text editor.
func (m *Model) activateSettingsField() {
	if m.menuIndex < 0 || m.menuIndex >= len(settingsFields) {
		return
	}
	field := settingsFields[m.menuIndex]

	if field.isToggle() {
		field.Toggle(m.config)
		// No success message follows, so the failure applySettingsChange puts
		// in settings.feedback is what the user sees.
		_ = m.applySettingsChange()
		return
	}

	if field.isList() {
		m.openListEditor(field.Opens)
		return
	}

	m.settings.editing = true
	m.settings.editValue = field.Get(m.config)
	m.settings.feedback = ""
}

// commitSettingsEdit applies the edited value to the config.
func (m *Model) commitSettingsEdit() tea.Cmd {
	if m.menuIndex < 0 || m.menuIndex >= len(settingsFields) {
		return nil
	}
	field := settingsFields[m.menuIndex]
	value := strings.TrimSpace(m.settings.editValue)

	m.settings.editing = false

	if field.Set != nil {
		if err := field.Set(m.config, value); err != nil {
			// Rejected: the config is unchanged, so say why rather than
			// saving something the app cannot use.
			m.settings.feedback = err.Error()
			m.settings.success = false
			return nil
		}
	}

	err := m.applySettingsChange()
	switch {
	case err != nil && !errors.Is(err, errDryRun):
		// saveConfig has already put the failure in settings.feedback; saying
		// "saved" over the top of it is how this went unnoticed.
		return nil

	case field.Submit != nil:
		// Runs on a dry run too. For the QA person, Submit is what applies the
		// value — Set only handles clearing — so stopping short of it would
		// leave the edit with no effect at all, in memory or otherwise.
		return field.Submit(m, value)

	case err != nil:
		return nil // the dry run's "not written" message stands
	}

	m.settings.feedback = field.Label + " saved"
	m.settings.success = true
	return nil
}

// applySettingsChange persists the config and refreshes anything derived from
// it. Changing a path or pattern is exactly when the diagnostics and the repo
// cache go stale, so they are refreshed here rather than at each call site.
//
// The error is returned rather than swallowed: the in-memory config has still
// changed, so the diagnostics and cache must still be refreshed, but the caller
// must not go on to claim it was saved.
func (m *Model) applySettingsChange() error {
	err := m.saveConfig()
	m.configDiagnostics = m.config.Validate()
	invalidateRepoCache()
	return err
}

// renderSettingsFields renders fields [first, last) of the list.
func (m Model) renderSettingsFields(first, last int) []string {
	var lines []string

	for i, field := range settingsFields {
		if i < first || i >= last {
			continue
		}
		selected := i == m.menuIndex
		editing := selected && m.settings.editing
		arrow := ui.Arrow(selected && !m.settings.editing)

		labelStyle := ui.Dim
		if selected {
			labelStyle = ui.Green
		}

		switch {
		case field.isToggle():
			toggle := ui.RedBold.Render("[OFF]")
			if field.Bool(m.config) {
				toggle = ui.GreenBold.Render("[ON] ")
			}
			lines = append(lines, fmt.Sprintf("  %s%s  %s", arrow, toggle, labelStyle.Render(field.Label)))

		case editing:
			lines = append(lines, fmt.Sprintf("  %s%s  %s%s",
				arrow, ui.Green.Render(field.Label+":"),
				ui.WhiteBold.Render(m.settings.editValue), ui.Cyan.Render("█")))

		default:
			valueStyle := ui.Dim
			if selected {
				valueStyle = ui.Cyan
			}
			value := field.display(m.config)
			if field.isList() {
				value += ui.Dim.Render("  ›")
			}
			lines = append(lines, fmt.Sprintf("  %s%s  %s",
				arrow, labelStyle.Render(field.Label+":"),
				valueStyle.Render(value)))
		}

		desc := field.Desc
		if editing && field.EditHint != "" {
			desc = field.EditHint
		}
		lines = append(lines, fmt.Sprintf("         %s", ui.Dim.Render(desc)))
		lines = append(lines, "")
	}

	return lines
}

func (m Model) handleSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Text input mode
	if m.settings.editing {
		switch msg.Type {
		case tea.KeyEnter:
			return m, m.commitSettingsEdit()
		case tea.KeyEsc:
			m.settings.editing = false
			m.settings.editValue = ""
		case tea.KeyBackspace:
			m.settings.editValue = trimLastRune(m.settings.editValue)
		case tea.KeySpace:
			m.settings.editValue += " "
		case tea.KeyRunes:
			m.settings.editValue += string(msg.Runes)
		}
		return m, nil
	}

	switch msg.String() {
	case "q":
		m.shouldQuit = true
		return m, tea.Quit
	case "up", "k":
		m.settings.feedback = ""
		navigateColumnIndex(&m.menuIndex, len(settingsFields), true)
	case "down", "j":
		m.settings.feedback = ""
		navigateColumnIndex(&m.menuIndex, len(settingsFields), false)
	case " ", "enter":
		m.activateSettingsField()
	case "esc":
		m.settings.feedback = ""
		m.screen = ScreenMainMenu
		m.menuIndex = 0
	}
	return m, nil
}

// settingsFieldLines is how many lines renderSettingsFields spends per field:
// the value row, its description, and a blank separator.
const settingsFieldLines = 3

func (m Model) renderSettingsWithHeight(availableHeight int) string {
	// Everything that is not a field, so the fields can be given what is left.
	// The screen used to render all eleven unconditionally and ignore the height
	// entirely — 50 lines, 57 with diagnostics, into whatever the terminal had.
	// Below about a 50-row terminal the last fields were simply off the bottom,
	// and the cursor went on moving through them invisibly.
	var tail []string
	if m.settings.looking {
		tail = append(tail, fmt.Sprintf("  %s Looking up user...",
			ui.Cyan.Render(ui.Spinner(m.spinnerFrame))))
	} else if m.settings.feedback != "" {
		icon, color := "✗", ui.ColorRed
		if m.settings.success {
			icon, color = "✓", ui.ColorGreen
		}
		fbStyle := lipgloss.NewStyle().Foreground(color)
		tail = append(tail, fmt.Sprintf("  %s", fbStyle.Render(icon+" "+m.settings.feedback)))
	}

	diagnostics := m.renderConfigDiagnostics()

	// 1 panel title + 1 leading blank + 2 scroll indicators.
	budget := availableHeight - len(tail) - 4
	// Diagnostics are worth losing before the fields are: they name a problem,
	// but the fields are how you fix it.
	if len(diagnostics) > 0 && budget-len(diagnostics) >= settingsFieldLines*2 {
		budget -= len(diagnostics)
	} else {
		diagnostics = nil
	}

	visible := budget / settingsFieldLines
	if visible < 1 {
		visible = 1
	}
	first, last := settingsWindow(m.menuIndex, len(settingsFields), visible)

	lines := []string{""}
	if first > 0 {
		lines = append(lines, ui.Dim.Render(fmt.Sprintf("      ↑ %d more above", first)))
	}
	lines = append(lines, m.renderSettingsFields(first, last)...)
	if last < len(settingsFields) {
		lines = append(lines, ui.Dim.Render(fmt.Sprintf("      ↓ %d more below", len(settingsFields)-last)))
	}

	lines = append(lines, tail...)
	lines = append(lines, diagnostics...)

	return panel(ui.CyanBold, "⚙  Settings", lines)
}

// settingsWindow centres visible fields on the cursor, clamped to the ends.
func settingsWindow(cursor, total, visible int) (first, last int) {
	if visible >= total {
		return 0, total
	}
	first = cursor - visible/2
	if first > total-visible {
		first = total - visible
	}
	if first < 0 {
		first = 0
	}
	return first, first + visible
}

// renderConfigDiagnostics lists config problems on the settings screen.
//
// These previously had no symptom other than an empty repo list — a mistyped
// path, a glob matching nothing, or a group assigned to no column all looked
// identical from the UI.
func (m Model) renderConfigDiagnostics() []string {
	diags := m.configDiagnostics
	if len(diags) == 0 {
		return nil
	}

	lines := []string{
		"",
		ui.SectionHeader("CONFIG", ui.ColorYellow),
	}
	for _, d := range diags {
		icon, style := "⚠", ui.Yellow
		if d.Severity == config.SeverityError {
			icon, style = "✗", ui.Red
		}
		lines = append(lines, fmt.Sprintf("  %s %s",
			style.Render(icon), ui.White.Render(d.Field+": "+d.Message)))
		if d.Fix != "" {
			lines = append(lines, fmt.Sprintf("     %s", ui.Dim.Render(d.Fix)))
		}
	}
	if path, err := configPathFn(); err == nil {
		lines = append(lines, "", fmt.Sprintf("  %s", ui.Dim.Render("Edit: "+path)))
	}
	return lines
}

// flowsSummary describes the release chain on one settings row: "dev → staging
// → main" while it fits, a count once it does not.
func flowsSummary(c *config.Config) string {
	flows := c.FlowEntries()
	if len(flows) == 0 {
		return "none configured"
	}

	parts := []string{flows[0].HeadBranch()}
	for _, f := range flows {
		parts = append(parts, f.BaseBranch("main"))
	}
	chain := strings.Join(parts, " → ")
	if len(chain) > 44 {
		return countLabel(len(flows), "step", "steps")
	}
	return chain
}
