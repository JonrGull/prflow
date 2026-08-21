package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/github"
	"github.com/JonrGull/prflow/internal/run"

	tea "github.com/charmbracelet/bubbletea"
)

// Reaching outside the app: the browser, the clipboard, the file manager, and
// the one-off `gh auth` check at startup.
//
// Everything here goes through internal/run rather than exec.Command, because
// the TUI blocks on these and an unbounded call freezes it.

type authCheckResult struct {
	user string
	err  error
}

// authCheckCmd runs gh auth check in the background
func authCheckCmd() tea.Cmd {
	return func() tea.Msg {
		user, err := github.CheckAuth()
		return authCheckResult{user: user, err: err}
	}
}

// openConfigCmd opens the config folder in the system file manager
func openConfigCmd() tea.Cmd {
	return func() tea.Msg {
		configPath, err := config.Path()
		if err != nil {
			return nil
		}
		configDir := filepath.Dir(configPath)

		switch runtime.GOOS {
		case "darwin":
			// macOS: open folder in Finder, select the file
			_ = run.Detached("open", "-R", configPath)
		case "linux":
			if isWSL() {
				// Convert Linux path to Windows path and open in Explorer
				winPath, err := run.Output(run.Local, "", "wslpath", "-w", configDir)
				if err == nil {
					_ = run.Detached("explorer.exe", strings.TrimSpace(string(winPath)))
				}
			} else {
				_ = run.Detached("xdg-open", configDir)
			}
		}
		return nil
	}
}

// openURL opens a URL in the default browser.
// Deliberately fire-and-forget: some launchers stay in the foreground for as
// long as the browser is open, and waiting on that would hang the UI.
func openURL(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return run.Detached("open", url)
	case "windows":
		return run.Detached("cmd", "/c", "start", url)
	default: // Linux and others
		return run.Detached("xdg-open", url)
	}
}

// isWSL checks if running under Windows Subsystem for Linux
func isWSL() bool {
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	version := strings.ToLower(string(data))
	return strings.Contains(version, "microsoft") || strings.Contains(version, "wsl")
}

// copyToClipboard copies text to the system clipboard
func copyToClipboard(text string) error {
	switch runtime.GOOS {
	case "darwin":
		return run.Input(run.Local, text, "pbcopy")
	case "windows":
		return run.Input(run.Local, text, "clip")
	default: // Linux
		if isWSL() {
			// WSL: use clip.exe to reach Windows clipboard
			return run.Input(run.Local, text, "clip.exe")
		}
		if run.LookPath("xclip") {
			return run.Input(run.Local, text, "xclip", "-selection", "clipboard")
		}
		return run.Input(run.Local, text, "xsel", "--clipboard", "--input")
	}
}

// openURLs opens multiple URLs in the default browser
func openURLs(urls []string) {
	for _, url := range urls {
		_ = openURL(url)
	}
}
