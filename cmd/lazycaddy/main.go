// Command lazycaddy is a keyboard-first terminal UI for inspecting and
// managing Caddy. It loads a Caddyfile, resolves imports and renders a
// document/site tree with a raw source view. An opt-in format and
// validate workflow (--caddy-path) runs caddy fmt and caddy validate
// against a temporary working copy and surfaces structured diagnostics.
// The tool is read-only by default: --write enables writable mode, in
// which saving creates a backup in --backup-dir and atomically replaces
// the file. With a caddy binary and a running local Admin API
// (--caddy-path; http://localhost:2019 by default), the r keybinding
// reloads the configuration through the Admin API — never implicitly,
// always after a validated, saved configuration and a confirmation
// prompt. The operator is always in control of when format, validate,
// save and reload run.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/backup"
	"github.com/PierpaoloPernici/lazycaddy/internal/config"
	"github.com/PierpaoloPernici/lazycaddy/internal/runtime"
	"github.com/PierpaoloPernici/lazycaddy/internal/ui"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
)

func main() {
	settings := config.DefaultSettings()
	var write bool

	rootCmd := &cobra.Command{
		Use:   "lazycaddy",
		Short: "A keyboard-first terminal UI for inspecting and managing Caddy",
		Long: "lazycaddy inspects a Caddyfile and its imports in the terminal.\n" +
			"The inspector is read-only by default; --write enables backups and atomic saves, and format/validate are opt-in via --caddy-path.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if write {
				settings.ReadOnly = false
			}
			if settings.BackupDir == "" {
				settings.BackupDir = filepath.Join(filepath.Dir(settings.ConfigPath), ".lazycaddy", "backups")
			}

			loader := app.NewLoader(settings, os.ReadFile)
			var formatter app.Formatter
			var reloader app.Reloader
			if settings.BinaryPath != "" {
				v, err := validator.New(validator.Options{
					BinaryPath: settings.BinaryPath,
					Timeout:    settings.ValidatorTimeout,
				})
				if err != nil {
					return fmt.Errorf("new validator: %w", err)
				}
				formatter = app.NewFormatter(v)
				// The reloader adapts the configuration locally with the
				// same caddy binary (so relative imports resolve from the
				// real config path) and posts the JSON to the Admin API.
				// Without a binary it stays nil and the r keybinding is
				// disabled.
				client := runtime.NewAdminClient(settings.AdminEndpoint, settings.AdminTimeout)
				reloader = app.NewReloader(settings.AdminEndpoint, client, v, os.ReadFile)
			}
			var saver app.Saver
			if !settings.ReadOnly {
				creator, err := backup.New(backup.Options{Dir: settings.BackupDir})
				if err != nil {
					return fmt.Errorf("new backup creator: %w", err)
				}
				saver = app.NewSaver(creator, os.ReadFile)
			}
			model := ui.New(loader, formatter, saver, reloader)
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
	rootCmd.Flags().BoolVar(&write, "write", false,
		"enable writable mode (save creates backups and writes the file); default is read-only")
	rootCmd.Flags().StringVar(&settings.BackupDir, "backup-dir", "",
		"directory for pre-save backups (default: <config-dir>/.lazycaddy/backups)")
	rootCmd.Flags().StringVar(&settings.AdminEndpoint, "admin-endpoint", settings.AdminEndpoint,
		"base URL of the local Caddy Admin API used for reloads (default: http://localhost:2019)")
	rootCmd.Flags().DurationVar(&settings.AdminTimeout, "admin-timeout", settings.AdminTimeout,
		"per-request timeout for Admin API calls such as reload (default: 30s)")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
