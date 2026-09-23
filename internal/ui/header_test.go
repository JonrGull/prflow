package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	m.Run()
}

// The header replaced a banner that was drawn at its natural 111 columns
// whatever the terminal was. It must fit whatever it is given.
func TestRenderHeaderFitsItsWidth(t *testing.T) {
	for _, width := range []int{1, 10, 30, 42, 54, 72, 92, 112, 152, 200} {
		for _, dry := range []bool{false, true} {
			out := RenderHeader(HeaderInfo{ActiveTab: 2, DryRun: dry, Meta: "gh: someone-with-a-long-login · v1.0.9"}, width)
			lines := strings.Split(out, "\n")
			if len(lines) != 2 {
				t.Fatalf("width %d: %d lines, want the header and its rule", width, len(lines))
			}
			for i, l := range lines {
				if got := lipgloss.Width(l); got > width {
					t.Errorf("width %d (dry %v): line %d is %d wide", width, dry, i+1, got)
				}
			}
		}
	}
}

// Content is shed in order: the metadata goes before the dry-run badge, which
// is the one thing that must not silently disappear on an ordinary terminal.
func TestRenderHeaderShedsMetadataBeforeTheBadge(t *testing.T) {
	info := HeaderInfo{ActiveTab: 0, DryRun: true, Meta: "gh: someone · v1.0.9"}

	wide := RenderHeader(info, 152)
	for _, want := range []string{"prflow", "All Open PRs", "DRY RUN", "gh: someone"} {
		if !strings.Contains(wide, want) {
			t.Errorf("at 152 columns the header is missing %q", want)
		}
	}

	// 60 columns is the narrowest width the screens are held to (54 inside the frame).
	narrow := RenderHeader(info, 54)
	if !strings.Contains(narrow, "DRY RUN") {
		t.Errorf("at 54 columns the dry-run badge was dropped: %q", narrow)
	}
	if strings.Contains(narrow, "gh: someone") {
		t.Errorf("at 54 columns the metadata should have gone first: %q", narrow)
	}
}
