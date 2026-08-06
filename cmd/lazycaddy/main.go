// Command lazycaddy is the terminal user interface for inspecting Caddy.
// This milestone is strictly read-only: it loads a Caddyfile, resolves its
// imports and renders a document/site tree with a raw source view. No write
// operations and no Caddy daemon interaction exist yet.
package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/config"
	"github.com/PierpaoloPernici/lazycaddy/internal/ui"
)

func main() {
	settings := config.DefaultSettings()

	rootCmd := &cobra.Command{
		Use:   "lazycaddy",
		Short: "A keyboard-first terminal UI for inspecting and managing Caddy",
		Long: "lazycaddy inspects a Caddyfile and its imports in the terminal.\n" +
			"This milestone is read-only: the raw Caddyfile is never modified.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			model := ui.New(app.NewLoader(settings, os.ReadFile))
			// Load before starting the program. Parse errors stay inside the
			// state, so the TUI still shows the raw source; only a missing
			// or unreadable config file is surfaced as the top-level error.
			model.Load()
			program := tea.NewProgram(model, tea.WithAltScreen())
			if _, err := program.Run(); err != nil {
				return fmt.Errorf("run TUI: %w", err)
			}
			return nil
		},
	}

	rootCmd.Flags().StringVar(&settings.ConfigPath, "config", config.DefaultConfigPath(),
		"path to the Caddyfile to inspect (default: ./Caddyfile)")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
