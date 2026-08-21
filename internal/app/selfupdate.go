package app

import (
	"fmt"
	"strings"

	"github.com/JonrGull/prflow/internal/ui"
	"github.com/JonrGull/prflow/internal/update"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The self-update prompt and download.

// Update check messages
type updateCheckResult struct {
	release *update.Release
	err     error
}

type updateDownloadResult struct {
	success bool
	version string
	err     error
}

// checkUpdateCmd checks for available updates
func checkUpdateCmd(currentVersion, repo string) tea.Cmd {
	return func() tea.Msg {
		release, err := update.CheckForUpdate(currentVersion, repo)
		return updateCheckResult{release: release, err: err}
	}
}

// downloadUpdateCmd downloads and installs an update
func downloadUpdateCmd(release *update.Release, repo string) tea.Cmd {
	return func() tea.Msg {
		err := update.DownloadAndInstall(release, repo)
		if err != nil {
			return updateDownloadResult{success: false, err: err}
		}
		return updateDownloadResult{success: true, version: update.VersionDisplay(release.TagName)}
	}
}

func (m Model) handleUpdateCheckResult(msg updateCheckResult) (tea.Model, tea.Cmd) {
	m.updateCheckInProgress = false

	// Record that we checked (regardless of result). This writes the separate
	// state file, not the user's config, so it happens on every launch without
	// touching their TOML — except under --dry-run, which promises no writes at
	// all and can still reach here via --test-update.
	if !m.dryRun {
		_ = m.config.RecordUpdateCheck()
	}

	if msg.err != nil {
		// Silently ignore update check errors
		return m, nil
	}

	if msg.release == nil {
		// No update available
		return m, nil
	}

	// Check if this version was skipped
	if m.config.SkippedVersion() == msg.release.TagName {
		return m, nil
	}

	// Only show update prompt if user is still on main menu
	// (don't interrupt if they've started navigating)
	if m.screen != ScreenMainMenu {
		return m, nil
	}

	// Update available - show prompt
	m.updateAvailable = msg.release
	m.screen = ScreenUpdatePrompt
	m.updateSelection = 0
	return m, nil
}

func (m Model) handleUpdateDownloadResult(msg updateDownloadResult) (tea.Model, tea.Cmd) {
	if !msg.success {
		m.errorMessage = fmt.Sprintf("Update failed: %v", msg.err)
		m.screen = ScreenError
		return m, nil
	}

	// Update successful - quit so user restarts with new version
	m.shouldQuit = true
	// Return a quit command with a message
	return m, tea.Sequence(
		tea.Printf("\nUpdated to %s! Run prflow again.\n", msg.version),
		tea.Quit,
	)
}

func (m Model) handleUpdatePromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "left", "h":
		if m.updateSelection > 0 {
			m.updateSelection--
		}
	case "right", "l":
		if m.updateSelection < 2 {
			m.updateSelection++
		}
	case "y", "1":
		m.updateSelection = 0
		return m.executeUpdateSelection()
	case "n", "2":
		m.updateSelection = 1
		return m.executeUpdateSelection()
	case "s", "3":
		m.updateSelection = 2
		return m.executeUpdateSelection()
	case "enter":
		return m.executeUpdateSelection()
	case "q", "esc":
		m.updateAvailable = nil
		m.screen = ScreenMainMenu
	}
	return m, nil
}

func (m Model) executeUpdateSelection() (tea.Model, tea.Cmd) {
	switch m.updateSelection {
	case 0: // Update now
		// Guarded here as well as at the key, because --test-update puts this
		// prompt on screen without going through a check. Replacing the running
		// binary is the least reversible thing the app does.
		if m.dryRun {
			m.updateAvailable = nil
			m.screen = ScreenMainMenu
			m.copyFeedback = "Update skipped (dry run)"
			return m, nil
		}
		m.screen = ScreenUpdating
		return m, downloadUpdateCmd(m.updateAvailable, m.config.Update.Repo)
	case 1: // Skip for now
		m.updateAvailable = nil
		m.screen = ScreenMainMenu
	case 2: // Skip this version
		if m.updateAvailable != nil {
			if err := m.config.SetSkippedVersion(m.updateAvailable.TagName); err != nil {
				m.copyFeedback = "✗ Could not record skipped version: " + err.Error()
			}
		}
		m.updateAvailable = nil
		m.screen = ScreenMainMenu
	}
	return m, nil
}

func (m Model) renderUpdatePrompt() string {
	var lines []string

	lines = append(lines, "")
	lines = append(lines, ui.SectionHeader("Update Available!", ui.ColorCyan))
	lines = append(lines, "")

	if m.updateAvailable != nil {
		versionStyle := ui.GreenBold
		currentStyle := ui.Yellow

		lines = append(lines, fmt.Sprintf("   Current version: %s", currentStyle.Render(m.version)))
		lines = append(lines, fmt.Sprintf("   New version:     %s", versionStyle.Render(update.VersionDisplay(m.updateAvailable.TagName))))
		lines = append(lines, "")
	}

	lines = append(lines, "   What would you like to do?")
	lines = append(lines, "")

	// Option buttons - fixed width for alignment
	options := []struct {
		key   string
		label string
		color lipgloss.Color
	}{
		{"y", "Update now", ui.ColorGreen},
		{"n", "Skip for now", ui.ColorYellow},
		{"s", "Skip this version", ui.ColorRed},
	}

	var buttons []string
	for i, opt := range options {
		text := fmt.Sprintf("[%s] %s", opt.key, opt.label)
		var style lipgloss.Style
		if i == m.updateSelection {
			style = lipgloss.NewStyle().
				Background(opt.color).
				Foreground(lipgloss.Color("#000000")).
				Padding(0, 1).
				Bold(true)
		} else {
			style = lipgloss.NewStyle().
				Foreground(opt.color).
				Padding(0, 1)
		}
		buttons = append(buttons, style.Render(text))
	}

	lines = append(lines, "   "+strings.Join(buttons, "   "))
	lines = append(lines, "")

	return strings.Join(lines, "\n")
}

func (m Model) renderUpdating() string {
	var lines []string

	lines = append(lines, "")
	lines = append(lines, ui.SectionHeader("Updating...", ui.ColorCyan))
	lines = append(lines, "")

	spinner := ui.Spinner(m.spinnerFrame)
	spinnerStyle := ui.Cyan
	statusStyle := ui.Yellow

	lines = append(lines, fmt.Sprintf("   %s %s",
		spinnerStyle.Render(spinner),
		statusStyle.Render("Downloading and installing update..."),
	))
	lines = append(lines, "")

	if m.updateAvailable != nil {
		dimStyle := ui.Dim
		lines = append(lines, dimStyle.Render(fmt.Sprintf("   Installing version %s", update.VersionDisplay(m.updateAvailable.TagName))))
	}

	return strings.Join(lines, "\n")
}
