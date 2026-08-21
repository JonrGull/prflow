// Package termfix sets environment variables to fix Warp terminal delays.
// Import this package FIRST (before any lipgloss/termenv imports) using:
//
//	_ "github.com/JonrGull/prflow/internal/termfix"
package termfix

import "os"

func init() {
	if os.Getenv("TERM_PROGRAM") == "WarpTerminal" {
		os.Setenv("TERM", "dumb")
		os.Setenv("COLORTERM", "truecolor")
	}
}
