package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JonrGull/prflow/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const historyMaxAge = 24 * time.Hour

// historyEntry is the persisted form of sessionPR
type historyEntry struct {
	RepoName  string    `json:"repo_name"`
	URL       string    `json:"url"`
	PrType    string    `json:"pr_type"`
	Action    string    `json:"action,omitempty"`
	PrNumber  uint64    `json:"pr_number,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func historyPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "prflow-history.json"), nil
}

// loadHistory loads and prunes old entries from the history file
func loadHistory() []sessionPR {
	path, err := historyPath()
	if err != nil {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var entries []historyEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil
	}

	// Filter to entries within 24h
	cutoff := time.Now().Add(-historyMaxAge)
	var valid []historyEntry
	for _, e := range entries {
		if e.CreatedAt.After(cutoff) {
			valid = append(valid, e)
		}
	}

	// Rewrite file if we pruned anything
	if len(valid) != len(entries) {
		saveHistoryEntries(valid)
	}

	// Convert to sessionPR
	var result []sessionPR
	for _, e := range valid {
		result = append(result, sessionPR{
			repoName:  e.RepoName,
			url:       e.URL,
			prType:    e.PrType,
			action:    e.Action,
			prNumber:  e.PrNumber,
			createdAt: e.CreatedAt,
		})
	}
	return result
}

// saveHistory saves the current session PRs to disk
func saveHistory(prs []sessionPR) {
	var entries []historyEntry
	for _, pr := range prs {
		entries = append(entries, historyEntry{
			RepoName:  pr.repoName,
			URL:       pr.url,
			PrType:    pr.prType,
			Action:    pr.action,
			PrNumber:  pr.prNumber,
			CreatedAt: pr.createdAt,
		})
	}
	saveHistoryEntries(entries)
}

func saveHistoryEntries(entries []historyEntry) {
	path, err := historyPath()
	if err != nil {
		return
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return
	}

	_ = os.WriteFile(path, data, 0644)
}

// sessionPR holds info about a PR created during this session
type sessionPR struct {
	repoName  string
	url       string
	prType    string // the step as displayed, e.g. "dev → staging"
	action    string // "created", "updated", "merged"
	prNumber  uint64
	createdAt time.Time
}

// recordSessionPR adds a PR to session history
func (m *Model) recordSessionPR(repoName, url, prType, action string, prNumber uint64) {
	m.sessionPRs = append(m.sessionPRs, sessionPR{
		repoName:  repoName,
		url:       url,
		prType:    prType,
		action:    action,
		prNumber:  prNumber,
		createdAt: time.Now(),
	})
	// The session list still gets the entry so the history screen works, but a
	// dry run must not leave fake PR URLs in prflow-history.json for the next
	// real launch to show.
	if m.dryRun {
		return
	}
	saveHistory(m.sessionPRs)
}

func (m Model) handleSessionHistoryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.shouldQuit = true
		return m, tea.Quit
	case "up", "k":
		if m.historyIndex > 0 {
			m.historyIndex--
		}
	case "down", "j":
		if m.historyIndex < len(m.sessionPRs)-1 {
			m.historyIndex++
		}
	case "o":
		// Open selected URL
		if m.historyIndex < len(m.sessionPRs) {
			_ = openURL(m.sessionPRs[m.historyIndex].url)
		}
	case "c":
		// Copy selected URL as markdown
		if m.historyIndex < len(m.sessionPRs) {
			pr := m.sessionPRs[m.historyIndex]
			formatted := fmt.Sprintf("- %s: %s", pr.repoName, pr.url)
			m.copyWithFeedback(formatted, "Copied!")
		}
		return m, nil
	case "C":
		// Copy all as markdown list
		if len(m.sessionPRs) > 0 {
			var items []string
			for _, pr := range m.sessionPRs {
				label := pr.prType
				if pr.action != "" {
					label = pr.action + " " + label
				}
				if pr.prNumber > 0 {
					label += fmt.Sprintf(" #%d", pr.prNumber)
				}
				items = append(items, fmt.Sprintf("- %s (%s): %s", pr.repoName, label, pr.url))
			}
			m.copyWithFeedback(strings.Join(items, "\n"), fmt.Sprintf("Copied %d entries!", len(m.sessionPRs)))
		}
		return m, nil
	case "esc", "enter":
		m.screen = ScreenMainMenu
		m.menuIndex = 0
	}
	return m, nil
}

func (m Model) renderSessionHistory() string {
	var lines []string
	lines = append(lines, "")

	if len(m.sessionPRs) == 0 {
		dimStyle := ui.Dim
		lines = append(lines, dimStyle.Render("  No PRs created this session"))
		lines = append(lines, "")
	} else {
		for i, pr := range m.sessionPRs {
			isSelected := i == m.historyIndex
			arrow := "  "
			if isSelected {
				arrow = "▶ "
			}

			var repoStyle, typeStyle, urlStyle, arrowStyle, dimStyle lipgloss.Style
			if isSelected {
				repoStyle = ui.CyanBold.Background(ui.ColorDarkGray)
				typeStyle = ui.Yellow.Background(ui.ColorDarkGray)
				urlStyle = ui.White.Background(ui.ColorDarkGray)
				arrowStyle = ui.Cyan.Background(ui.ColorDarkGray)
				dimStyle = ui.White.Background(ui.ColorDarkGray)
			} else {
				repoStyle = ui.CyanBold
				typeStyle = ui.Yellow
				urlStyle = ui.Dim
				arrowStyle = ui.Cyan
				dimStyle = ui.Dim
			}

			// Build label: "created dev → staging #123"
			label := pr.prType
			if pr.action != "" {
				label = pr.action + " " + label
			}
			if pr.prNumber > 0 {
				label += fmt.Sprintf(" #%d", pr.prNumber)
			}

			line := arrowStyle.Render(arrow) + repoStyle.Render(pr.repoName) + " " + typeStyle.Render("("+label+")") + " " + dimStyle.Render(relativeTime(pr.createdAt))
			lines = append(lines, line)
			lines = append(lines, "   "+urlStyle.Render(pr.url))
			lines = append(lines, "")
		}
	}

	titleStyle := ui.MagentaBold
	return titleStyle.Render(fmt.Sprintf(" 📋 Session History (%d) ", len(m.sessionPRs))) + "\n" + strings.Join(lines, "\n")
}
