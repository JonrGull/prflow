package app

import (
	"fmt"
	"strings"

	"github.com/JonrGull/prflow/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
)

// The error screen.
//
// Note that q means "back" here and not "quit", which is why there is no global
// quit handler in handleKey — the status bar used to advertise "q Quit" on this
// screen, which was simply untrue.

func (m Model) renderError() string {
	var lines []string

	errorStyle := ui.RedBold

	lines = append(lines, "")
	lines = append(lines, errorStyle.Render("   ✗ Error"))
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("   %s", m.errorMessage))
	lines = append(lines, "")
	lines = append(lines, "   Press Enter to go back")

	return strings.Join(lines, "\n")
}

func (m Model) handleErrorKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", "esc", "q":
		m.errorMessage = ""
		if m.flow != nil {
			m.screen = ScreenPrTypeSelect
		} else {
			m.screen = ScreenMainMenu
			m.mode = nil
		}
		m.menuIndex = 0
	}
	return m, nil
}
