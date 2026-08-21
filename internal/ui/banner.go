package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The header, at whatever size the terminal can take.
//
// This used to be one piece of art 111 columns wide and nothing else, rendered
// with an Align that did nothing because no Width was set — so on anything
// narrower it simply ran off the edge, and the dry-run warning sat flush left
// while the art around it looked centred. A standard 80-column terminal never
// laid out correctly. There are three tiers now, and the widest of them fits
// inside 80 columns.

// Banner is the full-size wordmark, and the widest thing the app draws.
var Banner = []string{
	" ____   ____   _____  _       ___  __        __",
	"|  _ \\ |  _ \\ |  ___|| |     / _ \\ \\ \\      / /",
	"| |_) || |_) || |_   | |    | | | | \\ \\ /\\ / / ",
	"|  __/ |  _ < |  _|  | |___ | |_| |  \\ V  V /  ",
	"|_|    |_| \\_\\|_|    |_____| \\___/    \\_/\\_/   ",
}

// BannerCompact is the fallback for terminals the full art will not fit.
var BannerCompact = []string{
	"╔═╗╦═╗╔═╗╦  ╔═╗╦ ╦",
	"╠═╝╠╦╝╠╣ ║  ║ ║║║║",
	"╩  ╩╚═╚  ╩═╝╚═╝╚╩╝",
}

const bannerSubtitle = "Release PR manager"

// bannerWidth is the display width of the widest line in lines.
func bannerWidth(lines []string) int {
	w := 0
	for _, l := range lines {
		if n := lipgloss.Width(l); n > w {
			w = n
		}
	}
	return w
}

// RenderBanner returns the header sized to fit width.
//
// Three tiers, largest that fits: the full art, the compact wordmark with a
// subtitle, or the plain name. Every line is centred within width — including
// the dry-run warning, which is the one that visibly was not.
func RenderBanner(dryRun bool, width int) string {
	if width < 1 {
		width = 1
	}

	var art []string
	switch {
	case width >= bannerWidth(Banner):
		art = Banner
	case width >= BannerCompactWidth():
		// The subtitle is what names the product once the wordmark is gone, so
		// the tier is only worth choosing at a width that can show both.
		art = append(append([]string{}, BannerCompact...), "", bannerSubtitle)
	default:
		art = []string{truncateToWidth("PRFLOW", width)}
	}

	style := lipgloss.NewStyle().Foreground(ColorCyan)

	var lines []string
	for _, line := range art {
		lines = append(lines, lipgloss.PlaceHorizontal(width, lipgloss.Center, style.Render(line)))
	}

	if dryRun {
		text := truncateToWidth("⚠ DRY RUN MODE", width)
		warning := lipgloss.NewStyle().Foreground(ColorYellow).Bold(true).Render(text)
		lines = append(lines, "", lipgloss.PlaceHorizontal(width, lipgloss.Center, warning))
	}

	return strings.Join(lines, "\n")
}

// BannerCompactWidth is the narrowest terminal the compact tier is worth using
// on: wide enough for the wordmark and the subtitle that names it.
func BannerCompactWidth() int {
	if w := lipgloss.Width(bannerSubtitle); w > bannerWidth(BannerCompact) {
		return w
	}
	return bannerWidth(BannerCompact)
}

// truncateToWidth cuts s to fit width, by rune so multi-byte text is not split.
// Only reached on terminals too narrow for even the plain name.
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
