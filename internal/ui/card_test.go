package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// Cards sit side by side, so their size must be exact whatever the content.
func TestCardIsExactlyItsSize(t *testing.T) {
	long := strings.Repeat("wide ", 30)
	for _, tc := range []struct {
		lines  []string
		height int
	}{
		{nil, 8},
		{[]string{"a", "b"}, 8},
		{[]string{long, long, long, long, long, long}, 8}, // too many, too wide
		{[]string{"a", "b"}, 5},                           // short: no blank row
		// A two-line error, such as a missing repo directory's, made the card a
		// row taller than asked.
		{[]string{Dim.Render("not found: ~/x\nUpdate paths.repos_dir"), "b", "c", "d"}, 8},
	} {
		card := Card("Title", Dim.Render("meta"), tc.lines, 40, tc.height)
		rows := strings.Split(card, "\n")
		if len(rows) != tc.height {
			t.Errorf("%d lines at height %d: %d rows", len(tc.lines), tc.height, len(rows))
		}
		for i, r := range rows {
			if w := lipgloss.Width(r); w != 40 {
				t.Errorf("height %d row %d is %d wide, want 40", tc.height, i, w)
			}
		}
	}
}

// A styled span ends in a reset, which would leave the rest of the row
// unshaded. Every reset inside the card must be followed by the shading.
func TestCardShadingSurvivesStyledText(t *testing.T) {
	card := Card("Title", "", []string{Red.Render("x") + " plain " + Green.Render("y")}, 30, 6)
	probe := lipgloss.NewStyle().Background(ColorPanel).Render(" ")
	shade := probe[:strings.Index(probe, " ")]
	for i, row := range strings.Split(card, "\n") {
		body := strings.TrimSuffix(row, "\x1b[0m") // the row's own final reset
		for j := strings.Index(body, "\x1b[0m"); j >= 0; j = strings.Index(body, "\x1b[0m") {
			body = body[j+len("\x1b[0m"):]
			if !strings.HasPrefix(body, shade) {
				t.Fatalf("row %d: a reset is not followed by the shading: %q", i, row)
			}
		}
	}
}

func TestCardRowsMatchesCard(t *testing.T) {
	for h := 4; h <= 10; h++ {
		lines := make([]string, CardRows(h))
		for i := range lines {
			lines[i] = "x"
		}
		card := Card("T", "", lines, 20, h)
		if got := strings.Count(card, "x"); got != len(lines) {
			t.Errorf("height %d: CardRows says %d fit, %d shown", h, len(lines), got)
		}
	}
}
