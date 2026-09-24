package ui

import "github.com/charmbracelet/lipgloss"

// Note: Warp terminal fix is in internal/termfix package, imported first in main.go

// The palette. Each colour is a light/dark pair: lipgloss picks the variant for
// the terminal's background, so text that was pure #FFFFFF — invisible on a
// light theme — now has a dark counterpart. The names are the hues the screens
// have always asked for; only the values changed, from pure ANSI primaries to
// one harmonised set, which is what stopped every heading shouting.
var (
	ColorCyan       = lipgloss.AdaptiveColor{Dark: "#7DCFFF", Light: "#007197"}
	ColorGreen      = lipgloss.AdaptiveColor{Dark: "#9ECE6A", Light: "#3F7A1F"}
	ColorYellow     = lipgloss.AdaptiveColor{Dark: "#E0AF68", Light: "#8C6C3E"}
	ColorRed        = lipgloss.AdaptiveColor{Dark: "#F7768E", Light: "#C8284F"}
	ColorMagenta    = lipgloss.AdaptiveColor{Dark: "#BB9AF7", Light: "#7847BD"}
	ColorBlue       = lipgloss.AdaptiveColor{Dark: "#7AA2F7", Light: "#2E5FD1"}
	ColorOrange     = lipgloss.AdaptiveColor{Dark: "#FF9E64", Light: "#B15C00"}
	ColorLightGreen = lipgloss.AdaptiveColor{Dark: "#73DACA", Light: "#0F7F6D"}
	ColorWhite      = lipgloss.AdaptiveColor{Dark: "#C0CAF5", Light: "#343B58"} // primary text
	ColorDarkGray   = lipgloss.AdaptiveColor{Dark: "#737AA2", Light: "#6B7394"} // dim text

	// Roles rather than hues.
	ColorBorder    = lipgloss.AdaptiveColor{Dark: "#3B4261", Light: "#C4C8DA"} // frames and rules
	ColorSelection = lipgloss.AdaptiveColor{Dark: "#283457", Light: "#D5DDF5"} // highlighted row
	ColorOnAccent  = lipgloss.AdaptiveColor{Dark: "#16161E", Light: "#FFFFFF"} // text on an accent fill
	ColorPanel     = lipgloss.AdaptiveColor{Dark: "#1F2335", Light: "#EDEFF6"} // shaded dashboard cards
)

// Preset styles for the combinations used repeatedly across the views.
//
// These were previously constructed inline at ~290 sites in view.go alone —
// 49 of them the identical dim-grey — and rebuilt on every frame of the
// animation tick. lipgloss.Style is a plain value type (its Copy method just
// returns the receiver), so deriving from a preset, e.g. Dim.Width(20), yields
// an independent copy and never mutates the shared value.
var (
	Dim     = lipgloss.NewStyle().Foreground(ColorDarkGray)
	Cyan    = lipgloss.NewStyle().Foreground(ColorCyan)
	Yellow  = lipgloss.NewStyle().Foreground(ColorYellow)
	White   = lipgloss.NewStyle().Foreground(ColorWhite)
	Green   = lipgloss.NewStyle().Foreground(ColorGreen)
	Red     = lipgloss.NewStyle().Foreground(ColorRed)
	Magenta = lipgloss.NewStyle().Foreground(ColorMagenta)
	Blue    = lipgloss.NewStyle().Foreground(ColorBlue)
	Orange  = lipgloss.NewStyle().Foreground(ColorOrange)

	CyanBold    = Cyan.Bold(true)
	YellowBold  = Yellow.Bold(true)
	WhiteBold   = White.Bold(true)
	GreenBold   = Green.Bold(true)
	RedBold     = Red.Bold(true)
	MagentaBold = Magenta.Bold(true)
)
