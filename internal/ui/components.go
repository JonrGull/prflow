package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// SectionHeader creates a styled section header with a title and color
// Example: "─── TITLE ───────────"
func SectionHeader(title string, color lipgloss.TerminalColor) string {
	dashes := strings.Repeat("─", max(25-len(title), 0))
	headerStyle := lipgloss.NewStyle().Foreground(color)
	titleStyle := lipgloss.NewStyle().Foreground(color).Bold(true)

	return fmt.Sprintf("%s%s%s",
		headerStyle.Render("  ─── "),
		titleStyle.Render(title),
		headerStyle.Render(" "+dashes),
	)
}

// BranchFlowDiagram creates a visual diagram showing branch flow
// Example: dev ====> staging
func BranchFlowDiagram(head, base string, headColor, baseColor lipgloss.TerminalColor) string {
	headStyle := lipgloss.NewStyle().Foreground(headColor)
	headBoldStyle := lipgloss.NewStyle().Foreground(headColor).Bold(true)
	baseStyle := lipgloss.NewStyle().Foreground(baseColor)
	baseBoldStyle := lipgloss.NewStyle().Foreground(baseColor).Bold(true)
	arrowStyle := lipgloss.NewStyle().Foreground(ColorCyan)

	// The boxes are as wide as the wider of the two names, and never narrower
	// than the 7 characters they were fixed at — which fit "staging" and
	// nothing longer, so a "production" or "release-candidate" branch used to
	// burst the box.
	inner := max(7, max(lipgloss.Width(head), lipgloss.Width(base)))
	headText := centerText(head, inner)
	baseText := centerText(base, inner)

	// Create box components (border is the text plus one space of padding
	// either side)
	rule := strings.Repeat("─", inner+2)
	topLeft := headStyle.Render("  ┌" + rule + "┐")
	topRight := baseStyle.Render("┌" + rule + "┐")

	middleLeft := headStyle.Render("  │ ") + headBoldStyle.Render(headText) + headStyle.Render(" │")
	arrow := arrowStyle.Render("  ====>  ")
	middleRight := baseStyle.Render("│ ") + baseBoldStyle.Render(baseText) + baseStyle.Render(" │")

	bottomLeft := headStyle.Render("  └" + rule + "┘")
	bottomRight := baseStyle.Render("└" + rule + "┘")

	// Combine into lines
	line1 := topLeft + "         " + topRight
	line2 := middleLeft + arrow + middleRight
	line3 := bottomLeft + "         " + bottomRight

	return line1 + "\n" + line2 + "\n" + line3
}

// centerText centers a string within a given width
func centerText(s string, width int) string {
	if len(s) >= width {
		return s[:width]
	}
	leftPad := (width - len(s)) / 2
	rightPad := width - len(s) - leftPad
	return strings.Repeat(" ", leftPad) + s + strings.Repeat(" ", rightPad)
}

// YesNoButtons creates interactive Yes/No buttons
// selection: 0 for Yes, 1 for No
func YesNoButtons(selection int) string {
	// When selected: button uses its accent color throughout; when not: dim border, white text
	yesActive := selection == 0
	noActive := selection == 1

	yesColor, yesTextColor := colorForButton(yesActive, ColorGreen)
	noColor, noTextColor := colorForButton(noActive, ColorRed)

	yesStyle := lipgloss.NewStyle().Foreground(yesColor)
	noStyle := lipgloss.NewStyle().Foreground(noColor)

	iconYes, iconNo := " ", " "
	if yesActive {
		iconYes = ">"
	}
	if noActive {
		iconNo = ">"
	}

	line1 := yesStyle.Render("  ┌────────┐") + " " + noStyle.Render("┌───────┐")
	line2 := fmt.Sprintf("%s%s%s %s%s%s",
		yesStyle.Render("  │"),
		lipgloss.NewStyle().Foreground(yesTextColor).Bold(true).Render(fmt.Sprintf(" %s  YES ", lipgloss.NewStyle().Foreground(yesColor).Render(iconYes))),
		yesStyle.Render("│"),
		noStyle.Render("│"),
		lipgloss.NewStyle().Foreground(noTextColor).Bold(true).Render(fmt.Sprintf(" %s  NO ", lipgloss.NewStyle().Foreground(noColor).Render(iconNo))),
		noStyle.Render("│"),
	)
	line3 := yesStyle.Render("  └────────┘") + " " + noStyle.Render("└───────┘")

	return line1 + "\n" + line2 + "\n" + line3
}

// colorForButton returns (borderColor, textColor) based on whether the button is active
func colorForButton(active bool, accentColor lipgloss.TerminalColor) (lipgloss.TerminalColor, lipgloss.TerminalColor) {
	if active {
		return accentColor, accentColor
	}
	return ColorDarkGray, ColorWhite
}

// Spinner frames using braille characters (matching Rust app)
var SpinnerFrames = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

// Spinner returns the spinner character at the given frame index
func Spinner(frame int) string {
	return string(SpinnerFrames[frame%len(SpinnerFrames)])
}

// WorkflowStatusIcon returns the icon and color for a GitHub Actions status/conclusion pair
func WorkflowStatusIcon(status, conclusion string, spinnerFrame int) (string, lipgloss.TerminalColor) {
	switch {
	case status == "in_progress":
		return Spinner(spinnerFrame), ColorYellow
	case status == "queued":
		return "◌", ColorDarkGray
	case conclusion == "success":
		return "✓", ColorGreen
	case conclusion == "failure":
		return "✗", ColorRed
	case conclusion == "cancelled" || conclusion == "skipped":
		return "⊘", ColorDarkGray
	default:
		return "?", ColorDarkGray
	}
}

// Checkbox renders a checkbox in the given state
func Checkbox(checked bool) string {
	if checked {
		return "[✓]"
	}
	return "[ ]"
}

// Arrow returns an arrow indicator for selection
func Arrow(selected bool) string {
	if selected {
		return "▶ "
	}
	return "  "
}

// KeyBinding renders a key binding hint
func KeyBinding(key, description string, color lipgloss.TerminalColor) string {
	keyStyle := lipgloss.NewStyle().Foreground(color).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(ColorDarkGray)

	return fmt.Sprintf("%s %s",
		keyStyle.Render(key),
		descStyle.Render(description),
	)
}

// TwoColumns renders two columns side by side
func TwoColumns(left, right string, gap int) string {
	return JoinColumns([]string{left, right}, gap)
}

// JoinColumns lays out any number of columns side by side with gap spaces
// between them. The open-PRs screen renders one column per configured release
// step, so the count is not known until runtime.
func JoinColumns(columns []string, gap int) string {
	if len(columns) == 0 {
		return ""
	}
	gapStr := strings.Repeat(" ", gap)
	parts := make([]string, 0, len(columns)*2-1)
	for i, c := range columns {
		if i > 0 {
			parts = append(parts, gapStr)
		}
		parts = append(parts, c)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

// UnifiedPanel creates two columns with a vertical separator (no border - outer border is in View)
func UnifiedPanel(leftContent, rightContent string, leftWidth, rightWidth int, borderColor lipgloss.TerminalColor) string {
	leftStyle := lipgloss.NewStyle().Width(leftWidth).Padding(0, 1)
	rightStyle := lipgloss.NewStyle().Width(rightWidth).Padding(0, 1)

	leftCol := leftStyle.Render(leftContent)
	rightCol := rightStyle.Render(rightContent)

	// Build vertical separator to match column height
	separatorStyle := lipgloss.NewStyle().Foreground(ColorDarkGray)
	separator := separatorStyle.Render("│")

	leftLines := strings.Split(leftCol, "\n")
	rightLines := strings.Split(rightCol, "\n")
	maxLines := len(leftLines)
	if len(rightLines) > maxLines {
		maxLines = len(rightLines)
	}
	var sepLines []string
	for i := 0; i < maxLines; i++ {
		sepLines = append(sepLines, separator)
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, leftCol, strings.Join(sepLines, "\n"), rightCol)
}

// ColumnBox creates a bordered column with title for two-column layouts
// If height > 0, content is padded/truncated to exactly that many lines
func ColumnBox(content string, title string, color lipgloss.TerminalColor, isActive bool, width int, height int) string {
	borderColor := color
	if !isActive {
		borderColor = ColorDarkGray
	}

	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(width)

	var fullContent string
	if title != "" {
		titleStyle := lipgloss.NewStyle().Bold(true).Foreground(color)
		fullContent = titleStyle.Render(" "+title+" ") + "\n" + content
	} else {
		fullContent = content
	}

	// With no fixed height, still cut lines to the width rather than let the
	// border wrap them: callers count one row per line (the pinned Actions
	// panels scroll by that count), and a wrapped line broke it.
	if height <= 0 {
		lines := strings.Split(fullContent, "\n")
		for i, line := range lines {
			if lipgloss.Width(line) > width {
				lines[i] = lipgloss.NewStyle().MaxWidth(width).Render(line)
			}
		}
		fullContent = strings.Join(lines, "\n")
	}

	// Manually pad/truncate to fixed height and prevent line wrapping
	if height > 0 {
		lines := strings.Split(fullContent, "\n")
		// Truncate lines that exceed column width to prevent wrapping
		for i, line := range lines {
			if lipgloss.Width(line) > width {
				lines[i] = lipgloss.NewStyle().MaxWidth(width).Render(line)
			}
		}
		if len(lines) < height {
			for len(lines) < height {
				lines = append(lines, "")
			}
		} else if len(lines) > height {
			lines = lines[:height]
		}
		fullContent = strings.Join(lines, "\n")
	}

	return style.Render(fullContent)
}

// FilterInput renders a search/filter input box
// If width > 0, the box will have a fixed width
// TitleBar renders a bordered title for a screen that has no filter.
//
// Screens without a filter used to borrow FilterInput by passing an empty
// string, which meant they drew a search icon, a "Type to filter..." prompt and
// a cursor for a feature they did not have — the Release PRs screen invited you
// to type and then ignored every keystroke.
func TitleBar(title string, color lipgloss.TerminalColor, width int) string {
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(color).
		Padding(0, 1)
	if width > 0 {
		style = style.Width(width)
	}
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(color)
	return style.Render(titleStyle.Render(title))
}

// FilterInput renders a bordered title above a live filter prompt. Only for
// screens that actually handle typing — see TitleBar otherwise.
func FilterInput(filter string, title string, color lipgloss.TerminalColor, width int) string {
	var filterDisplay string
	if filter == "" {
		filterDisplay = lipgloss.NewStyle().Foreground(ColorDarkGray).Render("Type to filter...")
	} else {
		filterDisplay = lipgloss.NewStyle().Foreground(ColorYellow).Render(filter)
	}

	cursor := lipgloss.NewStyle().Foreground(ColorYellow).Render("█")
	searchIcon := lipgloss.NewStyle().Foreground(ColorCyan).Render(" / ")

	content := searchIcon + filterDisplay + cursor

	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(color).
		Padding(0, 1)

	if width > 0 {
		style = style.Width(width)
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(color)
	return style.Render(titleStyle.Render(title) + "\n" + content)
}

// Commit counts for RepoListItemWithCommits that aren't counts.
const (
	CommitsLoading = -1
	CommitsFailed  = -2
)

// RepoListItemWithCommits renders a repo item with checkbox and commit indicator
// commitCount: -1 = loading, 0 = no commits, >0 = has commits
func RepoListItemWithCommits(name string, selected bool, highlighted bool, color lipgloss.TerminalColor, indent string, commitCount int, spinnerFrame int) string {
	checkbox := Checkbox(selected)
	arrow := Arrow(highlighted)

	var style lipgloss.Style
	if highlighted {
		style = lipgloss.NewStyle().Foreground(color).Bold(true)
	} else if selected {
		style = lipgloss.NewStyle().Foreground(color)
	} else {
		style = lipgloss.NewStyle().Foreground(ColorWhite)
	}

	indentStyle := lipgloss.NewStyle().Foreground(ColorDarkGray)
	checkStyle := lipgloss.NewStyle().Foreground(color)

	// Show indicator based on state
	var indicator string
	if commitCount == CommitsFailed {
		indicator = lipgloss.NewStyle().Foreground(ColorRed).Render(" ✗")
	} else if commitCount < 0 {
		// Loading - show spinner
		spinner := string(SpinnerFrames[spinnerFrame%len(SpinnerFrames)])
		indicator = lipgloss.NewStyle().Foreground(ColorYellow).Render(" " + spinner)
	} else if commitCount > 0 {
		// Has commits - green dot
		indicator = lipgloss.NewStyle().Foreground(ColorGreen).Render(" ●")
	} else {
		// No commits - dim dot
		indicator = lipgloss.NewStyle().Foreground(ColorDarkGray).Render(" ○")
	}

	return fmt.Sprintf("%s%s%s %s%s",
		style.Render(arrow),
		indentStyle.Render(indent),
		checkStyle.Render(checkbox),
		name,
		indicator,
	)
}

// PRListItem renders a compact single-line PR item for the merge view
func PRListItem(repoName string, prNumber uint64, selected bool, highlighted bool, color lipgloss.TerminalColor) string {
	checkbox := Checkbox(selected)
	arrow := Arrow(highlighted)

	var style lipgloss.Style
	if highlighted {
		style = lipgloss.NewStyle().Foreground(color).Bold(true)
	} else if selected {
		style = lipgloss.NewStyle().Foreground(color)
	} else {
		style = lipgloss.NewStyle().Foreground(ColorWhite)
	}

	checkStyle := lipgloss.NewStyle().Foreground(color)
	prNumStyle := lipgloss.NewStyle().Foreground(ColorDarkGray)

	return fmt.Sprintf("%s%s %s %s",
		style.Render(arrow),
		checkStyle.Render(checkbox),
		repoName,
		prNumStyle.Render(fmt.Sprintf("#%d", prNumber)),
	)
}

// ParentHeader renders a parent repo header for nested repos
func ParentHeader(name string) string {
	style := lipgloss.NewStyle().Foreground(ColorYellow).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(ColorDarkGray)
	return fmt.Sprintf("  %s%s",
		style.Render(fmt.Sprintf("┌─ %s ", name)),
		dimStyle.Render(strings.Repeat("─", 15)),
	)
}
