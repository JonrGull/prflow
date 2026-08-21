package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	m.Run()
}

// The banner is the widest thing the app draws, and it used to be drawn at its
// natural 111 columns whatever the terminal was — so a standard 80-column
// terminal had it running off the edge. It must fit whatever it is given.
func TestRenderBannerFitsItsWidth(t *testing.T) {
	for _, width := range []int{1, 10, 15, 20, 40, 60, 72, 80, 110, 111, 120, 200} {
		for _, dryRun := range []bool{false, true} {
			out := RenderBanner(dryRun, width)
			for i, line := range strings.Split(out, "\n") {
				if got := lipgloss.Width(line); got > width {
					t.Errorf("width %d (dryRun %v): line %d is %d wide, overflowing by %d",
						width, dryRun, i+1, got, got-width)
				}
			}
		}
	}
}

// Largest tier that fits, so a wide terminal is not given the fallback and a
// narrow one is not given art it cannot show.
func TestRenderBannerPicksTheLargestTierThatFits(t *testing.T) {
	full := bannerWidth(Banner)
	compact := BannerCompactWidth()

	tests := []struct {
		width int
		want  string // a fragment only that tier produces
	}{
		{full, Banner[0][:8]},
		{full + 40, Banner[0][:8]},
		{full - 1, BannerCompact[0]},
		{compact, BannerCompact[0]},
		{compact - 1, "PRFLOW"},
	}

	for _, tc := range tests {
		out := stripANSI(RenderBanner(false, tc.width))
		if !strings.Contains(out, tc.want) {
			t.Errorf("width %d: expected the tier containing %q\ngot:\n%s", tc.width, tc.want, out)
		}
	}

	// Narrower than the name itself, the name is cut rather than allowed to
	// overflow — an absurd terminal, but it must not corrupt the layout.
	if got := strings.TrimSpace(stripANSI(RenderBanner(false, 1))); got != "P" {
		t.Errorf("width 1 rendered %q, want the name truncated to \"P\"", got)
	}
}

// The subtitle is what names the product once the full wordmark is gone, so the
// compact tier must not be chosen at a width that cannot show it.
func TestCompactBannerShowsItsSubtitle(t *testing.T) {
	width := bannerWidth(Banner) - 1 // just too narrow for the full art
	out := stripANSI(RenderBanner(false, width))
	if !strings.Contains(out, bannerSubtitle) {
		t.Errorf("compact banner at %d columns lost its subtitle:\n%s", width, out)
	}
}

// The full wordmark used to be 111 columns, so a standard 80-column terminal
// never saw it. Keep it inside 80.
func TestFullBannerFitsAStandardTerminal(t *testing.T) {
	if w := bannerWidth(Banner); w > 80 {
		t.Errorf("the full banner is %d columns wide, so an 80-column terminal falls back", w)
	}
	// Every line the same width, or the art is ragged once centred.
	for i, line := range Banner {
		if got := lipgloss.Width(line); got != bannerWidth(Banner) {
			t.Errorf("banner line %d is %d wide, want %d", i+1, got, bannerWidth(Banner))
		}
	}
	for i, line := range BannerCompact {
		if got := lipgloss.Width(line); got != bannerWidth(BannerCompact) {
			t.Errorf("compact line %d is %d wide, want %d", i+1, got, bannerWidth(BannerCompact))
		}
	}
}

// The dry-run warning is the line that was visibly wrong: Align without a Width
// is a no-op, so it sat flush left while the art around it looked centred.
func TestDryRunWarningIsCentred(t *testing.T) {
	const width = 100
	out := stripANSI(RenderBanner(true, width))

	var warning string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "DRY RUN") {
			warning = line
		}
	}
	if warning == "" {
		t.Fatal("no dry-run warning rendered")
	}

	lead := len(warning) - len(strings.TrimLeft(warning, " "))
	if lead == 0 {
		t.Error("the dry-run warning starts at column 0 — it is not centred")
	}
	trail := len(warning) - len(strings.TrimRight(warning, " "))
	if diff := lead - trail; diff > 1 || diff < -1 {
		t.Errorf("dry-run warning is off-centre: %d leading vs %d trailing spaces", lead, trail)
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
