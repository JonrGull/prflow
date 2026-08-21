package app

import (
	"strings"

	"github.com/JonrGull/prflow/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The main menu and the info panel beside it.
//
// That panel describes what each entry will do using the user's actual config —
// it used to name a hardcoded repo directory and ticket prefix regardless of
// what was configured, which made it a confident description of someone else's
// setup.

func (m Model) handleMainMenuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.shouldQuit = true
		return m, tea.Quit
	case "up", "k":
		if m.menuIndex > 0 {
			m.menuIndex--
		} else {
			m.menuIndex = 5 // Wrap to bottom
		}
	case "down", "j":
		if m.menuIndex < 5 {
			m.menuIndex++
		} else {
			m.menuIndex = 0 // Wrap to top
		}
	case "enter", "1", "2", "3", "4", "5", "6":
		if idx, ok := numKeyIndex(msg.String(), 6); ok {
			m.menuIndex = idx
		}
		return m.selectMainMenuItem()
	case "a":
		m.menuIndex = 4
		return m.selectMainMenuItem()
	case "u":
		// Manual update check. Blocked under --dry-run, which promises no
		// network and no changes: a local build reports Version "dev", which
		// CheckForUpdate treats as older than everything, so this prompt always
		// appears — and accepting it renames a download over the binary being
		// tested.
		if m.dryRun {
			m.copyFeedback = "Update check skipped (dry run)"
			return m, nil
		}
		if m.updateCheckInProgress {
			return m, nil
		}
		m.updateCheckInProgress = true
		return m, checkUpdateCmd(m.version, m.config.Update.Repo)
	case "c":
		// Open config in editor
		return m, openConfigCmd()
	case "h":
		m.screen = ScreenSessionHistory
		m.historyIndex = 0
	case "p":
		// Pull all repos
		m.screen = ScreenPullBranchSelect
		m.menuIndex = 0
	case "o":
		// Settings
		m.screen = ScreenSettings
		m.menuIndex = 0
	}
	return m, nil
}

func (m Model) selectMainMenuItem() (tea.Model, tea.Cmd) {
	// Check for auth error before any GitHub operation (except Quit)
	if m.authError != nil && m.menuIndex != 5 {
		m.screen = ScreenError
		m.errorMessage = m.authError.Error()
		return m, nil
	}

	// Sync activeTab with menu selection (for tabs 0-4)
	if m.menuIndex >= 0 && m.menuIndex <= 4 {
		m.activeTab = m.menuIndex
	}

	switch m.menuIndex {
	case 0: // Single Repo
		mode := ModeSingle
		m.mode = &mode
		m.screen = ScreenLoading
		m.loadingMessage = "Detecting repository..."
		return m, loadCurrentRepoCmd()
	case 1: // Batch Mode
		mode := ModeBatch
		m.mode = &mode
		m.screen = ScreenPrTypeSelect
		m.menuIndex = 0
	case 2: // View Release PRs
		return m.navigateToMergePRs()
	case 3: // All Open PRs
		m.screen = ScreenLoading
		m.loadingMessage = "Fetching all open PRs..."
		return m, fetchAllOpenPRsCmd(m.config, m.dryRun)
	case 4: // GitHub Actions
		m.actions.loading = true
		m.screen = ScreenLoading
		m.loadingMessage = "Fetching workflow runs..."
		return m, fetchActionsRunsCmd(m.config, m.dryRun)
	case 5: // Quit
		m.shouldQuit = true
		return m, tea.Quit
	}
	return m, nil
}

// menuInfoDetails supplies the main menu's info panel with the user's actual
// configuration, rather than the defaults it used to assert as fact.
func (m Model) menuInfoDetails() ui.MenuInfoDetails {
	scanDir := m.config.Paths.ReposDir
	if scanDir == "" {
		scanDir = "(not configured)"
	}
	steps := make([]ui.MenuInfoStep, 0, len(m.flows()))
	for i, f := range m.flows() {
		head, base := f.HeadBranch(), f.BaseBranch(m.mainBranch())
		steps = append(steps, ui.MenuInfoStep{
			Head: head, Base: base,
			HeadColor: flowChainColor(i), BaseColor: flowChainColor(i + 1),
		})
	}

	return ui.MenuInfoDetails{
		ScanDir:       scanDir,
		TicketExample: ticketExample(m.config.Tickets.Pattern),
		Branches:      m.chainBranches(),
		Steps:         steps,
	}
}

// ticketExample turns a ticket regex into something readable for the info
// panel — "ATT-[0-9]+" reads better as "ATT-123" than as the raw pattern.
func ticketExample(pattern string) string {
	if pattern == "" {
		return "disabled"
	}
	// Longest patterns first, so "[A-Z][A-Z0-9]+" is not half-substituted by
	// the shorter "[A-Z]" rule and left looking like a regex.
	replacements := []struct{ from, to string }{
		{"[A-Z][A-Z0-9]+", "ABC"},
		{"[A-Za-z]+", "ABC"},
		{"[A-Z]+", "ABC"},
		{"[A-Z]", "A"},
		{"[0-9]+", "123"},
		{"[0-9]{1,}", "123"},
		{`\d+`, "123"},
		{`\w+`, "ABC"},
	}
	example := pattern
	for _, r := range replacements {
		example = strings.ReplaceAll(example, r.from, r.to)
	}
	// If the pattern still looks like a regex, show it as-is rather than
	// inventing something misleading.
	if strings.ContainsAny(example, `[]{}()\|*+?^$`) {
		return pattern
	}
	return example
}

func (m Model) renderMainMenu() string {
	menuItems := []struct {
		icon  string
		title string
		desc  string
		color lipgloss.Color
	}{
		{"1.", "SINGLE REPO", "Create PR for current repo", ui.ColorCyan},
		{"2.", "BATCH MODE", "Create PRs for multiple repos", ui.ColorMagenta},
		{"3.", "RELEASE PRS", "View open release PRs", ui.ColorYellow},
		{"4.", "ALL OPEN PRS", "See all open PRs across repos", ui.ColorBlue},
		{"5.", "GITHUB ACTIONS", "Monitor workflow runs", ui.ColorOrange},
		{"6.", "QUIT", "Exit application", ui.ColorRed},
	}

	// Build left column (menu) content
	var menuLines []string
	menuLines = append(menuLines, "")
	for i, item := range menuItems {
		rows := ui.MenuRow(item.icon, item.title, item.desc, item.color, i == m.menuIndex, 46)
		menuLines = append(menuLines, rows...)
		menuLines = append(menuLines, "")
	}

	menuTitleStyle := lipgloss.NewStyle().Bold(true).Foreground(ui.ColorOrange)
	menuContent := panel(menuTitleStyle, "Select Mode", menuLines)

	// Build right column (info panel)
	infoTitle, infoLines := ui.MenuInfoPanel(m.menuIndex, m.menuInfoDetails())
	titleStyle := ui.WhiteBold
	infoContent := panel(titleStyle, infoTitle, infoLines)

	return ui.UnifiedPanel(menuContent, infoContent, 48, 48, ui.ColorCyan)
}
