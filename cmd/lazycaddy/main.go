// Command lazycaddy is the terminal user interface for inspecting Caddy.
// This milestone adds a read-only inspector with an opt-in caddy fmt
// and caddy validate workflow (--caddy-path). No file writes and no
// Caddy daemon interaction are performed: the operator is always in
// control of when format and validate run.
package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/config"
	"github.com/PierpaoloPernici/lazycaddy/internal/ui"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
)

func main() {
	settings := config.DefaultSettings()

	rootCmd := &cobra.Command{
		Use:   "lazycaddy",
		Short: "A keyboard-first terminal UI for inspecting and managing Caddy",
		Long: "lazycaddy inspects a Caddyfile and its imports in the terminal.\n" +
			"The inspector is read-only; format and validate are opt-in via --caddy-path.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			loader := app.NewLoader(settings, os.ReadFile)
			var formatter app.Formatter
			if settings.BinaryPath != "" {
				v, err := validator.New(validator.Options{
					BinaryPath: settings.BinaryPath,
					Timeout:    settings.ValidatorTimeout,
				})
				if err != nil {
					return fmt.Errorf("new validator: %w", err)
				}
				formatter = app.NewFormatter(v)
			}
			model := ui.New(loader, formatter)
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
	rootCmd.Flags().StringVar(&settings.BinaryPath, "caddy-path", "",
		"path to the caddy binary (default: empty; format and validate are disabled)")
	rootCmd.Flags().DurationVar(&settings.ValidatorTimeout, "validator-timeout", 0,
		"per-invocation timeout for caddy fmt and caddy validate (default: 5s, the validator package default)")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
