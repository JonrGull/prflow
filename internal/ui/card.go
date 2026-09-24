package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Card draws a shaded panel with no border: a padding row, the title (with
// meta on the right), a blank row, the lines, and padding to height. Lines
// wider than the card are cut, never wrapped, and a line holding a newline is
// that many rows, so height is exact. A card shorter than 6 rows drops the
// blank row; CardRows says how many lines fit.
//
// Every styled span inside ends with a reset, which would punch a hole in the
// shading; OnBackground puts the panel colour back after each one, so callers
// style their text as usual.
func Card(title, meta string, lines []string, width, height int) string {
	inner := max(width-4, 1)
	head := lipgloss.NewStyle().Foreground(ColorDarkGray).Bold(true).Render(strings.ToUpper(title))
	if meta != "" {
		gap := inner - lipgloss.Width(head) - lipgloss.Width(meta)
		if gap >= 2 {
			head += strings.Repeat(" ", gap) + meta
		}
	}
	rows := []string{"", head}
	if height >= 6 {
		rows = append(rows, "")
	}
	for _, l := range lines {
		rows = append(rows, strings.Split(l, "\n")...)
	}
	for len(rows) < height-1 {
		rows = append(rows, "")
	}
	if len(rows) > height-1 {
		rows = rows[:max(height-1, 0)]
	}
	rows = append(rows, "")

	cut := lipgloss.NewStyle().MaxWidth(inner)
	for i, r := range rows {
		if lipgloss.Width(r) > inner {
			r = cut.Render(r)
		}
		rows[i] = OnBackground("  "+r+strings.Repeat(" ", inner-lipgloss.Width(r))+"  ", ColorPanel)
	}
	return strings.Join(rows, "\n")
}

// CardRows is how many lines a card of this height has room for.
func CardRows(height int) int {
	if height >= 6 {
		return height - 4
	}
	return max(height-3, 0)
}

// OnBackground shows s on bg, restoring bg after every reset inside s. With no
// colour profile it returns s unchanged.
func OnBackground(s string, bg lipgloss.TerminalColor) string {
	probe := lipgloss.NewStyle().Background(bg).Render(" ")
	i := strings.Index(probe, " ")
	if i <= 0 {
		return s
	}
	seq := probe[:i]
	const reset = "\x1b[0m"
	return seq + strings.ReplaceAll(s, reset, reset+seq) + reset
}
