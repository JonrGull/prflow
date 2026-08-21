package main

// Must be first import - fixes Warp terminal delay before lipgloss loads
import _ "github.com/JonrGull/prflow/internal/termfix"

import (
	"fmt"
	"os"

	"github.com/JonrGull/prflow/internal/app"
	"github.com/JonrGull/prflow/internal/config"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags "-X main.Version=vX.X.X"
var Version = "dev"

var (
	dryRun     bool
	testUpdate bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "prflow",
		Short: "TUI for managing GitHub release PRs",
		RunE:  run,
	}

	rootCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Simulate operations: no GitHub or Linear calls, no update check, no config writes")
	rootCmd.Flags().BoolVar(&testUpdate, "test-update", false, "Show update prompt for testing")
	rootCmd.Flags().MarkHidden("test-update")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	model := app.New(cfg, dryRun, testUpdate, Version)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("error running program: %w", err)
	}

	return nil
}
