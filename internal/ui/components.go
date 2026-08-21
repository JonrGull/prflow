package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// TabNames are the labels for the top-level navigation tabs
var TabNames = []string{"Single", "Batch", "Release PRs", "All Open PRs", "Actions"}

// TabColors are the colors for each tab
var TabColors = []lipgloss.Color{ColorCyan, ColorMagenta, ColorYellow, ColorBlue, ColorOrange}

// RenderTabBar renders a horizontal tab bar with the active tab highlighted
func RenderTabBar(activeTab int, width int) string {
	dimStyle := lipgloss.NewStyle().Foreground(ColorDarkGray)
	var tabs []string
	for i, name := range TabNames {
		if i == activeTab {
			style := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#000000")).
				Background(TabColors[i]).
				Bold(true).
				Padding(0, 1)
			tabs = append(tabs, style.Render(name))
		} else {
			style := lipgloss.NewStyle().
				Foreground(TabColors[i]).
				Padding(0, 1)
			tabs = append(tabs, style.Render(name))
		}
	}
	bar := strings.Join(tabs, dimStyle.Render("│"))
	return lipgloss.PlaceHorizontal(width, lipgloss.Center, bar)
}

// SectionHeader creates a styled section header with a title and color
// Example: "─── TITLE ───────────"
func SectionHeader(title string, color lipgloss.Color) string {
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
func BranchFlowDiagram(head, base string, headColor, baseColor lipgloss.Color) string {
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
func colorForButton(active bool, accentColor lipgloss.Color) (lipgloss.Color, lipgloss.Color) {
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
func WorkflowStatusIcon(status, conclusion string, spinnerFrame int) (string, lipgloss.Color) {
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
func KeyBinding(key, description string, color lipgloss.Color) string {
	keyStyle := lipgloss.NewStyle().Foreground(color).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(ColorWhite)

	return fmt.Sprintf("%s %s",
		keyStyle.Render(key),
		descStyle.Render(description),
	)
}

// MenuInfoDetails carries the config values the info panel describes.
//
// The panel used to hardcode one company's repo directory and ticket prefix as
// strings, so it confidently described a setup the user might not have — it was
// wrong for anyone whose config differed from the defaults, which includes
// anyone who ever changed the scan directory or the ticket pattern.
type MenuInfoDetails struct {
	ScanDir       string // paths.repos_dir, as configured
	TicketExample string // an example ticket ID derived from tickets.pattern

	// Branches is the release chain in order, and Steps the moves between
	// them. Both used to be drawn as a fixed dev/staging/main ladder, which
	// described a branching model rather than the configured one.
	Branches []string
	Steps    []MenuInfoStep
}

// MenuInfoStep is one release step, with each branch's colour resolved by the
// caller — internal/ui does not read the config.
type MenuInfoStep struct {
	Head, Base string
	HeadColor  lipgloss.Color
	BaseColor  lipgloss.Color
}

// MenuInfoPanel returns the ASCII art and description for a menu item
func MenuInfoPanel(index int, details MenuInfoDetails) (title string, lines []string) {
	switch index {
	case 0: // Single Repo
		title = "Single Repo Mode"
		prBox := lipgloss.NewStyle().Foreground(ColorCyan)
		prText := lipgloss.NewStyle().Foreground(ColorCyan).Bold(true)
		lines = []string{
			"",
			prBox.Render("        ┌──────────┐"),
			prBox.Render("        │") + prText.Render("    PR  ") + prBox.Render("  │"),
			prBox.Render("        └──────────┘"),
			"",
			"  • Detects " + branchList(details.Branches) + " branches",
			"  • Shows commits to be merged",
			"  • Extracts tickets (" + details.TicketExample + ")",
			"  • Creates or updates existing PR",
		}
	case 1: // Batch Mode
		title = "Batch Mode"
		prBox := lipgloss.NewStyle().Foreground(ColorMagenta)
		prText := lipgloss.NewStyle().Foreground(ColorMagenta).Bold(true)
		lines = []string{
			"",
			prBox.Render("     ┌────┐") + prBox.Render(" ┌────┐") + prBox.Render(" ┌────┐"),
			prBox.Render("     │") + prText.Render(" PR ") + prBox.Render("│") + prBox.Render(" │") + prText.Render(" PR ") + prBox.Render("│") + prBox.Render(" │") + prText.Render(" PR ") + prBox.Render("│"),
			prBox.Render("     └────┘") + prBox.Render(" └────┘") + prBox.Render(" └────┘"),
			"",
			"  • Scans " + details.ScanDir,
			"  • Select repos with checkboxes",
			"  • Extracts tickets (" + details.TicketExample + ")",
			"  • Shows summary of results",
		}
	case 2: // Release PRs
		title = "Release PRs"
		lines = append([]string{""}, stepLadder(details.Steps)...)
		lines = append(lines,
			"",
			"  • Select and batch merge",
			"  • Smart ordering (earliest first)",
			"  • Open or copy URLs",
		)
	case 3: // All Open PRs
		title = "All Open PRs"
		prStyle := lipgloss.NewStyle().Foreground(ColorBlue)
		prBold := lipgloss.NewStyle().Foreground(ColorBlue).Bold(true)
		dimStyle := lipgloss.NewStyle().Foreground(ColorDarkGray)
		lines = []string{
			"",
			"   " + prBold.Render("#12") + dimStyle.Render(" feat/auth → dev"),
			"   " + prBold.Render("#34") + dimStyle.Render(" dev → staging"),
			"   " + prStyle.Render("#56") + dimStyle.Render(" staging → main"),
			"",
			"  • Shows every open PR",
			"  • Across all configured repos",
			"  • Open any PR in browser",
			"  • Not limited to release flow",
		}
	case 4: // GitHub Actions
		title = "GitHub Actions"
		runningStyle := lipgloss.NewStyle().Foreground(ColorYellow)
		successStyle := lipgloss.NewStyle().Foreground(ColorGreen)
		failStyle := lipgloss.NewStyle().Foreground(ColorRed)
		dimStyle := lipgloss.NewStyle().Foreground(ColorDarkGray)
		lines = []string{
			"",
			"       " + runningStyle.Render("⠹") + " CI  " + successStyle.Render("✓") + " Deploy",
			"       " + failStyle.Render("✗") + " Test  " + dimStyle.Render("◌") + " Lint",
			"",
			"  • Monitor all workflow runs",
			"  • Auto-refresh every 5s",
			"  • Drill into job details",
			"  • Open runs in browser",
		}
	default: // Quit
		title = "Quit"
		lines = []string{
			"",
			"  Exit the application",
		}
	}
	return title, lines
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
func UnifiedPanel(leftContent, rightContent string, leftWidth, rightWidth int, borderColor lipgloss.Color) string {
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
func ColumnBox(content string, title string, color lipgloss.Color, isActive bool, width int, height int) string {
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
func TitleBar(title string, color lipgloss.Color, width int) string {
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
func FilterInput(filter string, title string, color lipgloss.Color, width int) string {
	var filterDisplay string
	if filter == "" {
		filterDisplay = lipgloss.NewStyle().Foreground(ColorDarkGray).Render("Type to filter...")
	} else {
		filterDisplay = lipgloss.NewStyle().Foreground(ColorYellow).Render(filter)
	}

	cursor := lipgloss.NewStyle().Foreground(ColorYellow).Render("█")
	searchIcon := lipgloss.NewStyle().Foreground(ColorCyan).Render(" 🔍 ")

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

// RepoListItemWithCommits renders a repo item with checkbox and commit indicator
// commitCount: -1 = loading, 0 = no commits, >0 = has commits
func RepoListItemWithCommits(name string, selected bool, highlighted bool, color lipgloss.Color, indent string, commitCount int, spinnerFrame int) string {
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
	if commitCount < 0 {
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
func PRListItem(repoName string, prNumber uint64, selected bool, highlighted bool, color lipgloss.Color) string {
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

// MenuRow renders a menu row with optional highlight background
// width should be the inner width of the panel (excluding border)
func MenuRow(icon, title, desc string, color lipgloss.Color, selected bool, width int) []string {
	arrow := "  "
	if selected {
		arrow = "▶ "
	}

	if selected {
		// For selected items, render the whole line with background
		rowStyle := lipgloss.NewStyle().Background(ColorDarkGray).Width(width)
		arrowStyle := lipgloss.NewStyle().Foreground(color).Background(ColorDarkGray)
		iconStyle := lipgloss.NewStyle().Background(ColorDarkGray)
		titleStyle := lipgloss.NewStyle().Foreground(color).Bold(true).Background(ColorDarkGray)
		descStyle := lipgloss.NewStyle().Foreground(ColorWhite).Background(ColorDarkGray)

		line1 := rowStyle.Render(arrowStyle.Render(arrow) + iconStyle.Render(icon+"  ") + titleStyle.Render(title))
		line2 := rowStyle.Render("       " + descStyle.Render(desc))

		return []string{line1, line2}
	}

	// Non-selected items - no background
	arrowStyle := lipgloss.NewStyle().Foreground(color)
	titleStyle := lipgloss.NewStyle().Foreground(color).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(ColorWhite)

	line1 := arrowStyle.Render(arrow) + icon + "  " + titleStyle.Render(title)
	line2 := "       " + descStyle.Render(desc)

	return []string{line1, line2}
}

// branchList names the release branches for a one-line description, keeping it
// short enough for the panel when the chain is long.
func branchList(branches []string) string {
	if len(branches) == 0 {
		return "your release"
	}
	if len(branches) > 3 {
		return strings.Join(branches[:3], "/") + "/…"
	}
	return strings.Join(branches, "/")
}

// stepLadder draws the release chain as one coloured line per step. It replaces
// a hand-drawn three-branch ladder, which could only ever be right for a
// dev → staging → main setup.
func stepLadder(steps []MenuInfoStep) []string {
	if len(steps) == 0 {
		return []string{Red.Render("    No release steps configured")}
	}

	arrow := lipgloss.NewStyle().Foreground(ColorCyan)
	lines := make([]string, 0, len(steps))
	for _, st := range steps {
		head := lipgloss.NewStyle().Foreground(st.HeadColor).Bold(true)
		base := lipgloss.NewStyle().Foreground(st.BaseColor).Bold(true)
		lines = append(lines, "    "+head.Render(st.Head)+arrow.Render(" → ")+base.Render(st.Base))
	}
	return lines
}
