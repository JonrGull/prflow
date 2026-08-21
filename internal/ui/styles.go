package ui

import "github.com/charmbracelet/lipgloss"

// Note: Warp terminal fix is in internal/termfix package, imported first in main.go

var (
	ColorCyan       = lipgloss.Color("#00FFFF")
	ColorGreen      = lipgloss.Color("#00FF00")
	ColorYellow     = lipgloss.Color("#FFFF00")
	ColorRed        = lipgloss.Color("#FF0000")
	ColorMagenta    = lipgloss.Color("#FF00FF")
	ColorBlue       = lipgloss.Color("#5555FF")
	ColorPurple     = lipgloss.Color("#AA55FF")
	ColorOrange     = lipgloss.Color("#FFA500")
	ColorLightGreen = lipgloss.Color("#90EE90")
	ColorWhite      = lipgloss.Color("#FFFFFF")
	ColorDarkGray   = lipgloss.Color("#6C6C6C") // Readable dim text on both light and dark backgrounds
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
