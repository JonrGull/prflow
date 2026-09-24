package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The header: brand, tabs and metadata on one line, over a rule.
//
// It replaced a five-row ASCII wordmark, a dry-run line and a separate tab line,
// which took eight rows from every screen before it drew anything. What does not
// fit is shed in a fixed order — the metadata, then the long tab names, then the
// brand — so the line never runs off the edge, and the dry-run badge is kept
// until almost nothing else is.

// TabNames are the labels for the top-level navigation tabs.
var TabNames = []string{"Single", "Batch", "Release PRs", "All Open PRs", "Actions"}

// tabShortNames stand in for TabNames when the full labels do not fit.
var tabShortNames = []string{"Single", "Batch", "Release", "All PRs", "Actions"}

// TabColors are each tab's accent, used to fill the active one.
var TabColors = []lipgloss.TerminalColor{ColorCyan, ColorMagenta, ColorYellow, ColorBlue, ColorOrange}

// HomeTab is the ActiveTab of the dashboard, drawn as a "Home" tab ahead of
// the others.
const HomeTab = -1

// HeaderInfo is what the header shows around the tabs.
type HeaderInfo struct {
	ActiveTab int    // HomeTab (-1) for the dashboard, else an index into TabNames
	DryRun    bool   // shows the DRY RUN badge
	Meta      string // e.g. "gh: jon · v1.0.9"; the first thing dropped
}

// RenderHeader returns the header line and the rule under it, width wide.
func RenderHeader(info HeaderInfo, width int) string {
	brand := lipgloss.NewStyle().Foreground(ColorMagenta).Bold(true).Render("◆") + " " + WhiteBold.Render("prflow")
	badge := ""
	if info.DryRun {
		badge = lipgloss.NewStyle().Background(ColorYellow).Foreground(ColorOnAccent).Bold(true).Padding(0, 1).Render("DRY RUN")
	}
	meta := ""
	if info.Meta != "" {
		meta = Dim.Render(info.Meta)
	}

	join := func(parts ...string) string {
		var kept []string
		for _, p := range parts {
			if p != "" {
				kept = append(kept, p)
			}
		}
		return strings.Join(kept, "  ")
	}
	long, short := renderTabs(TabNames, info.ActiveTab), renderTabs(tabShortNames, info.ActiveTab)
	home := lipgloss.NewStyle().Padding(0, 1).Foreground(ColorDarkGray).Render("Home")
	if info.ActiveTab == HomeTab {
		home = lipgloss.NewStyle().Padding(0, 1).Background(ColorLightGreen).Foreground(ColorOnAccent).Bold(true).Render("Home")
	}
	// Home goes last among the tabs, just before the badge would.
	bare := short
	long, short = home+long, home+short

	candidates := [][2]string{
		{join(brand, long), join(badge, meta)},
		{join(brand, long), badge},
		{join(brand, short), badge},
		{short, badge},
		{bare, badge},
		{short, ""},
		{bare, ""},
	}
	line := ""
	for _, c := range candidates {
		if l, ok := spread(c[0], c[1], width); ok {
			line = l
			break
		}
	}
	if line == "" {
		// Narrower than even the short tabs: the plain names, cut to fit.
		line = Dim.Render(truncateToWidth(strings.Join(tabShortNames, " "), width))
	}

	rule := lipgloss.NewStyle().Foreground(ColorBorder).Render(strings.Repeat("─", max(width, 0)))
	return line + "\n" + rule
}

// renderTabs draws the tab labels: the active one filled with its accent, the
// rest dim.
func renderTabs(names []string, active int) string {
	tabs := make([]string, len(names))
	for i, name := range names {
		style := lipgloss.NewStyle().Padding(0, 1)
		if i == active {
			style = style.Background(TabColors[i]).Foreground(ColorOnAccent).Bold(true)
		} else {
			style = style.Foreground(ColorDarkGray)
		}
		tabs[i] = style.Render(name)
	}
	return strings.Join(tabs, "")
}

// spread places left and right at either end of width, with at least two
// spaces between them, or reports that they do not fit.
func spread(left, right string, width int) (string, bool) {
	lw, rw := lipgloss.Width(left), lipgloss.Width(right)
	gap := width - lw - rw
	if right == "" {
		return left + strings.Repeat(" ", max(gap, 0)), gap >= 0
	}
	if gap < 2 {
		return "", false
	}
	return left + strings.Repeat(" ", gap) + right, true
}

// truncateToWidth cuts s to fit width, by rune so multi-byte text is not split.
func truncateToWidth(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r)) > width {
		r = r[:len(r)-1]
	}
	return string(r)
}
