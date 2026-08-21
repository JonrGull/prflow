package app

import (
	"fmt"
	"strings"

	"github.com/JonrGull/prflow/internal/linear"
	"github.com/JonrGull/prflow/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
)

// Tagging Linear tickets for QA after a merge: choosing which of the merged
// tickets to tag, and the Linear calls that do it.

// qaState is everything QA tagging owns. selected is parallel to m.tickets.
type qaState struct {
	selected []bool
	index    int
	results  []linear.QaTagResult
	titles   map[string]string // ticket identifier → title from Linear
}

func (m *Model) initQaTagState() tea.Cmd {
	m.qa.selected = make([]bool, len(m.tickets))
	for i := range m.qa.selected {
		m.qa.selected[i] = true
	}
	m.qa.index = 0
	m.qa.results = nil
	m.qa.titles = nil
	return fetchQaTicketTitlesCmd(m.config.LinearAPIKey(), m.tickets, m.dryRun)
}

func (m *Model) shouldShowQaTagScreen() bool {
	return m.config.Tickets.QaTagging && m.config.Tickets.QaPerson != "" && len(m.tickets) > 0
}

func (m Model) handleQaTagSelectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "t":
		// Disable QA tagging permanently and skip
		m.config.Tickets.QaTagging = false
		// Best-effort: saveConfig has surfaced any failure in the status bar,
		// and this screen is being left either way — the toggle still holds
		// for the rest of the session.
		_ = m.saveConfig()
		for i := range m.qa.selected {
			m.qa.selected[i] = false
		}
		return m.proceedFromQaTag()
	case "up", "k":
		if m.qa.index > 0 {
			m.qa.index--
		}
	case "down", "j":
		if m.qa.index < len(m.tickets)-1 {
			m.qa.index++
		}
	case " ":
		if m.qa.index < len(m.qa.selected) {
			m.qa.selected[m.qa.index] = !m.qa.selected[m.qa.index]
		}
	case "a":
		// Toggle all: if any selected, deselect all; otherwise select all
		anySelected := false
		for _, s := range m.qa.selected {
			if s {
				anySelected = true
				break
			}
		}
		for i := range m.qa.selected {
			m.qa.selected[i] = !anySelected
		}
	case "s":
		// Skip — deselect all and proceed
		for i := range m.qa.selected {
			m.qa.selected[i] = false
		}
		return m.proceedFromQaTag()
	case "enter", "tab":
		return m.proceedFromQaTag()
	case "esc":
		return m.goBack()
	case "q":
		m.shouldQuit = true
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) proceedFromQaTag() (tea.Model, tea.Cmd) {
	m.screen = ScreenMergeSummary
	if cmd := m.qaTagCmd(); cmd != nil {
		return m, cmd
	}
	return m, nil
}

type qaTagResultMsg struct {
	results []linear.QaTagResult
}

type qaTicketTitlesResult struct {
	titles map[string]string
}

type qaPersonLookupResult struct {
	name string
	id   string
	err  error
}

func lookupQaPersonCmd(apiKey, name string, dryRun bool) tea.Cmd {
	return func() tea.Msg {
		if dryRun {
			return dryRunQaPerson(name)
		}
		if apiKey == "" {
			return qaPersonLookupResult{name: name, err: fmt.Errorf("no LINEAR_API_KEY")}
		}
		id, err := linear.FindUserByDisplayName(apiKey, name)
		if err != nil {
			return qaPersonLookupResult{name: name, err: err}
		}
		return qaPersonLookupResult{name: name, id: id}
	}
}

func fetchQaTicketTitlesCmd(apiKey string, tickets []string, dryRun bool) tea.Cmd {
	return func() tea.Msg {
		if dryRun {
			return dryRunTicketTitles(tickets)
		}
		if apiKey == "" {
			return qaTicketTitlesResult{titles: map[string]string{}}
		}
		titles := linear.FetchTicketTitles(apiKey, tickets)
		if titles == nil {
			titles = map[string]string{}
		}
		return qaTicketTitlesResult{titles: titles}
	}
}

func (m *Model) qaTagCmd() tea.Cmd {
	// Collect selected tickets
	var toTag []string
	for i, t := range m.tickets {
		if i < len(m.qa.selected) && m.qa.selected[i] {
			toTag = append(toTag, t)
		}
	}
	if len(toTag) == 0 || m.config.Tickets.QaPerson == "" {
		return nil
	}

	env := m.qaEnvironment()

	// Collect URLs from successfully merged PRs
	var prURLs []string
	for _, result := range m.merge.results {
		if result.Success && result.URL != "" {
			prURLs = append(prURLs, result.URL)
		}
	}
	prURL := strings.Join(prURLs, "\n")

	apiKey := m.config.LinearAPIKey()
	qaPerson := m.config.Tickets.QaPerson
	qaPersonID := m.config.Tickets.QaPersonID
	dryRun := m.dryRun

	return func() tea.Msg {
		if dryRun {
			return dryRunQaTagResults(toTag)
		}
		if apiKey == "" {
			results := make([]linear.QaTagResult, len(toTag))
			for i, t := range toTag {
				results[i] = linear.QaTagResult{Ticket: t, Error: "no LINEAR_API_KEY"}
			}
			return qaTagResultMsg{results: results}
		}
		return qaTagResultMsg{
			results: linear.TagTicketsForQA(apiKey, toTag, qaPerson, qaPersonID, env, prURL),
		}
	}
}

func (m Model) renderQaTagResults() []string {
	var lines []string
	dimStyle := ui.Dim
	successStyle := ui.Green
	failStyle := ui.Red
	ticketStyle := ui.Yellow

	lines = append(lines, dimStyle.Render("   QA Tags:"))
	for _, r := range m.qa.results {
		if r.Success {
			lines = append(lines, fmt.Sprintf("   %s %s",
				successStyle.Render("✓"),
				ticketStyle.Render(r.Ticket)))
		} else {
			lines = append(lines, fmt.Sprintf("   %s %s %s",
				failStyle.Render("✗"),
				ticketStyle.Render(r.Ticket),
				dimStyle.Render(r.Error)))
		}
	}
	return lines
}

// qaEnvironment names the environment a merge shipped to, for the QA note on
// the tagged tickets.
//
// This used to test the merged PR against the StagingToMain enum value, which
// is how "production" came to mean one hardcoded branch pair. The rule now is
// that the *last* configured step is the production release; an earlier step
// is named after the branch it merged into, because only whoever wrote the
// chain knows what to call it.
func (m Model) qaEnvironment() string {
	flows := m.flows()
	env := "staging"
	for _, result := range m.merge.results {
		if !result.Success {
			continue
		}
		if len(flows) > 0 && result.Flow == flows[len(flows)-1] {
			return "production"
		}
		if base := result.Flow.BaseBranch(result.MainBranch); base != "" {
			env = base
		}
	}
	return env
}

func (m Model) renderQaTagSelect() string {
	var lines []string

	env := m.qaEnvironment()

	headerStyle := ui.CyanBold
	dimStyle := ui.Dim
	selectedStyle := ui.Green
	ticketStyle := ui.Yellow
	loadingStyle := ui.Dim.Italic(true)

	// Precompute loading suffix once (same for all rows)
	var loadingSuffix string
	if m.qa.titles == nil {
		loadingSuffix = "  " + loadingStyle.Render(ui.Spinner(m.spinnerFrame)+" Loading...")
	}

	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("  Notify %s about tickets on %s:",
		headerStyle.Render(m.config.Tickets.QaPerson),
		headerStyle.Render(env)))
	lines = append(lines, "")

	for i, ticket := range m.tickets {
		arrow := ui.Arrow(i == m.qa.index)
		checked := i < len(m.qa.selected) && m.qa.selected[i]
		checkbox := ui.Checkbox(checked)

		// Build ticket label with title
		label := ticket
		if m.qa.titles != nil {
			if title, ok := m.qa.titles[ticket]; ok {
				label = ticket + "  " + title
			}
		} else {
			label = ticket + loadingSuffix
		}

		labelStyle := ticketStyle
		checkStyle := selectedStyle
		if !checked {
			labelStyle = dimStyle
			checkStyle = dimStyle
		}
		line := fmt.Sprintf("  %s%s %s",
			arrow,
			checkStyle.Render(checkbox),
			labelStyle.Render(label))
		lines = append(lines, line)
	}

	// Count selected
	count := 0
	for _, s := range m.qa.selected {
		if s {
			count++
		}
	}
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("  %s",
		dimStyle.Render(fmt.Sprintf("%d of %d tickets selected", count, len(m.tickets)))))

	titleStyle := ui.CyanBold
	return panel(titleStyle, "🏷  Tag QA", lines)
}
