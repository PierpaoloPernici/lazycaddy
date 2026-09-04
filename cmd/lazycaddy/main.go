// Command lazycaddy is a keyboard-first terminal UI for inspecting and
// managing Caddy. It loads a Caddyfile, resolves imports and renders a
// document/site tree with a raw source view. Without an explicit --config
// it discovers ./Caddyfile (falling back to /etc/caddy/Caddyfile), and
// without --caddy-path it discovers caddy through PATH while keeping
// format, validate and reload disabled when the binary is unavailable. An
// opt-in format and validate workflow (--caddy-path) runs caddy fmt and
// caddy validate
// against a temporary working copy and surfaces structured diagnostics.
// The tool is read-only by default: --write enables writable mode, in
// which saving creates a backup in --backup-dir and atomically replaces
// the file. With a caddy binary and a running local Admin API
// (--caddy-path; http://localhost:2019 by default), the r keybinding
// reloads the configuration through the Admin API — never implicitly,
// always after a validated, saved configuration and a confirmation
// prompt. At startup a runtime probe queries the configured caddy binary
// for its version and checks the Admin API, so the header can report the
// detected capabilities; every probe failure degrades to an explicit
// unknown/stopped state and the TUI remains fully browsable read-only.
// With --log-file or --log-journal-unit (mutually exclusive), the l
// keybinding opens a read-only log view that follows the Caddy log file
// (polling, rotation-aware) or a systemd journal unit, and highlights
// each line's JSON structure; without either the log view is disabled.
// The / and Ctrl-F keybindings open a read-only, case-insensitive
// substring search across site/node labels, document paths and content
// (imported files included) and the loaded log history; Enter jumps to
// the hit and Esc closes without side effects.
// The operator is always in control of when format, validate, save and
// reload run.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/backup"
	webbrowser "github.com/PierpaoloPernici/lazycaddy/internal/browser"
	"github.com/PierpaoloPernici/lazycaddy/internal/clipboard"
	"github.com/PierpaoloPernici/lazycaddy/internal/config"
	"github.com/PierpaoloPernici/lazycaddy/internal/discover"
	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
	"github.com/PierpaoloPernici/lazycaddy/internal/runtime"
	"github.com/PierpaoloPernici/lazycaddy/internal/tls"
	"github.com/PierpaoloPernici/lazycaddy/internal/ui"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
	"github.com/PierpaoloPernici/lazycaddy/internal/watch"
)

func main() {
	settings := config.DefaultSettings()
	var write bool
	rootCmd := newRootCommand(&settings, &write)
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// newRootCommand builds the lazycaddy cobra command. The flags are bound to
// the provided settings and write pointers so tests can drive flag parsing
// without executing the application.
func newRootCommand(settings *config.Settings, write *bool) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:     "lazycaddy",
		Short:   "A keyboard-first terminal UI for inspecting and managing Caddy",
		Version: version,
		Long: "lazycaddy inspects a Caddyfile and its imports in the terminal.\n" +
			"The inspector is read-only by default; --write enables backups and atomic saves. " +
			"Without --caddy-path, caddy is discovered through PATH and format/validate are disabled when it is unavailable.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if *write {
				settings.ReadOnly = false
			}
			// Resolve v0.2 path discovery and sensible defaults before
			// anything is wired: the effective Caddyfile, the caddy
			// binary and the backup directory are derived from the
			// environment when the corresponding flags are absent.
			// Explicit --config, --caddy-path and --backup-dir values
			// always take precedence.
			if err := resolvePaths(cmd.Flags(), settings, discover.DefaultDeps()); err != nil {
				return err
			}

			loader := app.NewLoader(*settings, os.ReadFile)
			// The Admin API client is shared by the reloader and the
			// startup runtime probe; it is created unconditionally so the
			// probe can report on the API even when no caddy binary is
			// configured.
			client := runtime.NewAdminClient(settings.AdminEndpoint, settings.AdminTimeout)
			// The retention policy is shared by the saver and the
			// rollbacker. It is disabled by default (Keep zero) and is
			// applied only after a successful save or rollback.
			retention := backup.Retention{Dir: settings.BackupDir, Keep: settings.BackupRetention}
			var formatter app.Formatter
			var reloader app.Reloader
			var validatorInstance *validator.Validator
			if settings.BinaryPath != "" {
				v, err := validator.New(validator.Options{
					BinaryPath: settings.BinaryPath,
					Timeout:    settings.ValidatorTimeout,
				})
				if err != nil {
					return fmt.Errorf("new validator: %w", err)
				}
				validatorInstance = v
				formatter = app.NewFormatter(v)
				// The reloader adapts the configuration locally with the
				// same caddy binary (so relative imports resolve from the
				// real config path) and posts the JSON to the Admin API.
				// Without a binary it stays nil and the r keybinding is
				// disabled.
				reloader = app.NewReloader(settings.AdminEndpoint, client, v, os.ReadFile)
			}
			var saver app.Saver
			var creator backup.Creator
			if !settings.ReadOnly {
				var err error
				creator, err = backup.New(backup.Options{Dir: settings.BackupDir})
				if err != nil {
					return fmt.Errorf("new backup creator: %w", err)
				}
				saver = app.NewSaverWithRetention(creator, os.ReadFile, retention)
			}
			// Backup listing and comparison are available whenever a
			// backup directory is configured; rollback additionally needs
			// writable mode (a creator) and a caddy binary (validation).
			// The rollbacker is built unconditionally so the B keybinding
			// works read-only; its guards report the missing capability.
			rollbacker, err := app.NewRollbacker(app.RollbackerOptions{
				Dir:       settings.BackupDir,
				Creator:   creator,
				ReadFile:  os.ReadFile,
				Validator: validatorInstance,
				Retention: retention,
			})
			if err != nil {
				return fmt.Errorf("new rollbacker: %w", err)
			}
			// The editor runs the operator's $EDITOR on the selected node
			// range and recomposes the document from the result. It needs
			// the caddy binary for the validation gate; without it the e
			// keybinding stays disabled, because an unvalidated edit must
			// never become savable. The editor command itself comes from
			// $VISUAL / $EDITOR at run time — there is no CLI flag.
			var editor app.Editor
			if formatter != nil {
				editor = app.NewEditor(app.EditorOptions{
					LookupEnv:   os.LookupEnv,
					Formatter:   formatter,
					ReadFile:    os.ReadFile,
					SnapshotDir: filepath.Join(filepath.Dir(settings.ConfigPath), ".lazycaddy", "snapshots"),
				})
			}
			// The startup runtime probe queries the configured caddy
			// binary for its version and checks the Admin API, feeding
			// the header's capability badges. Failures degrade to
			// explicit unknown/stopped states; the TUI never blocks on
			// the probe (each step carries its own timeout).
			detector := runtime.NewDetector(runtime.Options{
				Binary:         settings.BinaryPath,
				Runner:         validator.ExecRunner{},
				Admin:          client,
				Writable:       !settings.ReadOnly,
				VersionTimeout: settings.ValidatorTimeout,
				AdminTimeout:   5 * time.Second,
			})
			runtimeStatus := app.RuntimeStatusFunc(detector.Probe)
			// The log view is opt-in: without --log-file or
			// --log-journal-unit the source stays nil and the l keybinding
			// is disabled, so the TUI never ticks or touches the
			// filesystem or the journal. The two options are mutually
			// exclusive; buildLogSource validates that before constructing
			// anything.
			logSource, err := buildLogSource(*settings)
			if err != nil {
				return err
			}
			// Search is always available and read-only: it matches node
			// labels, document paths and content lines (imports included)
			// and the loaded log history with / or Ctrl-F.
			searcher := app.NewSearcher()
			clip := clipboard.New(clipboard.Options{Output: os.Stdout})
			webHelp := webbrowser.New(webbrowser.Options{})
			// The external-change monitor watches the parent directories of
			// the resolved documents, coalesces event bursts and reports
			// only byte differences against the in-memory sources, feeding
			// the reload/compare/keep conflict modal. It is a notification
			// layer: the saver, editor and reloader conflict checks remain
			// the final guards. A watcher failure degrades to the
			// notification being disabled, never to browsing being blocked.
			watcher, err := watch.NewWatcher()
			if err != nil {
				return fmt.Errorf("new file watcher: %w", err)
			}
			monitor := watch.NewMonitor(watch.Options{
				Watcher:  watcher,
				ReadFile: os.ReadFile,
			})
			monitor.Start()
			// Dashboard fetchers: each panel fetches independently and is
			// cancellable, so a failure in one never blocks the others.
			configFetcher := runtime.NewAdminConfigFetcher(client)
			upstreamFetcher := runtime.NewAdminUpstreamFetcher(client)
			tlsSource := tls.NewFileSource(settings.TLSStorageDir, os.ReadFile, os.ReadDir, os.Stat)
			tlsFetcher := app.TLSFetcherFunc(func(ctx context.Context) ([]tls.Certificate, error) {
				return tlsSource.ListCertificates(ctx)
			})
			model := ui.NewWithBrowser(loader, formatter, saver, reloader, runtimeStatus, logSource, editor, searcher, version, monitor, rollbacker, os.ReadFile, webHelp, clip)
			model.WithConfigFetcher(configFetcher).WithUpstreamFetcher(upstreamFetcher).WithTLSFetcher(tlsFetcher)
			// Load before starting the program. Parse errors stay inside the
			// state, so the TUI still shows the raw source; only a missing
			// or unreadable config file is surfaced as the top-level error.
			model.Load()
			program := teaProgram(model)
			if err := runTUI(func() (tea.Model, error) { return program.Run() }, logSource, monitor); err != nil {
				return err
			}
			return nil
		},
	}
	rootCmd.SetVersionTemplate(versionOutput())

	rootCmd.Flags().StringVar(&settings.ConfigPath, "config", config.DefaultConfigPath(),
		"path to the Caddyfile to inspect (default: ./Caddyfile when present, else /etc/caddy/Caddyfile)")
	rootCmd.Flags().StringVar(&settings.BinaryPath, "caddy-path", "",
		"path to the caddy binary (default: discover caddy through PATH; format and validate are disabled when it is unavailable)")
	rootCmd.Flags().DurationVar(&settings.ValidatorTimeout, "validator-timeout", 0,
		"per-invocation timeout for caddy fmt and caddy validate (default: 5s, the validator package default)")
	rootCmd.Flags().BoolVar(write, "write", false,
		"enable writable mode (save creates backups and writes the file); default is read-only")
	rootCmd.Flags().StringVar(&settings.BackupDir, "backup-dir", "",
		"directory for pre-save backups (default: ~/.local/state/lazycaddy/backups, honoring $XDG_STATE_HOME)")
	rootCmd.Flags().IntVar(&settings.BackupRetention, "backup-retention", 0,
		"maximum number of backups kept per source file after a successful save or rollback (default: 0, retention disabled)")
	rootCmd.Flags().StringVar(&settings.AdminEndpoint, "admin-endpoint", settings.AdminEndpoint,
		"base URL of the local Caddy Admin API used for reloads (default: http://localhost:2019)")
	rootCmd.Flags().DurationVar(&settings.AdminTimeout, "admin-timeout", settings.AdminTimeout,
		"per-request timeout for Admin API calls such as reload (default: 30s)")
	rootCmd.Flags().StringVar(&settings.LogPath, "log-file", "",
		"path of a Caddy log file to follow in the log view (default: empty; the log view is disabled)")
	rootCmd.Flags().StringVar(&settings.JournalUnit, "log-journal-unit", "",
		"systemd journal unit to follow in the log view (e.g. caddy.service); default: empty; the log view then reads the journal")
	rootCmd.Flags().StringVar(&settings.TLSStorageDir, "tls-storage-dir", "",
		"path of the TLS storage directory (CertMagic) for the TLS dashboard (default: empty; the TLS panel is unavailable)")

	return rootCmd
}

// teaProgram constructs the Bubble Tea program for the TUI. It is a
// package-level variable so tests can run the full command wiring
// headlessly (no terminal) by replacing it with a program whose input and
// output are in-memory; the production value uses the alt screen on the
// real terminal and enables cell-motion mouse tracking so the source, log
// and diff panes can create text selections.
var teaProgram = func(model tea.Model) *tea.Program {
	return tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
}

// resolvePaths applies v0.2 path discovery and sensible defaults to settings
// before the application is wired. Explicit --config, --caddy-path and
// --backup-dir values always take precedence; discovery fills the gaps:
//
//   - config: ./Caddyfile when present, else /etc/caddy/Caddyfile, with a
//     clear missing-file error when neither exists;
//   - binary: caddy through PATH, leaving the binary empty (format,
//     validate and reload disabled) when it is unavailable;
//   - backup: a user-writable XDG state directory, so system Caddyfiles
//     never force backups into a root-owned config directory.
//
// All external lookups go through deps (discover.DefaultDeps in
// production), keeping the rules deterministic under test.
func resolvePaths(flags *pflag.FlagSet, settings *config.Settings, deps discover.Deps) error {
	resolver := discover.Resolver{Deps: deps}
	configPath, err := resolver.ConfigPath(flags.Changed("config"), settings.ConfigPath)
	if err != nil {
		return err
	}
	settings.ConfigPath = configPath
	settings.BinaryPath = resolver.BinaryPath(flags.Changed("caddy-path"), settings.BinaryPath)
	backupDir, err := resolver.BackupDir(flags.Changed("backup-dir"), settings.BackupDir)
	if err != nil {
		return err
	}
	settings.BackupDir = backupDir
	return nil
}

// buildLogSource validates and constructs the configured read-only log
// source. --log-file and --log-journal-unit are mutually exclusive; when
// neither is set the source is nil and the log view stays disabled.
func buildLogSource(settings config.Settings) (app.LogSource, error) {
	if settings.LogPath != "" && settings.JournalUnit != "" {
		return nil, fmt.Errorf("--log-file and --log-journal-unit are mutually exclusive: choose one log source")
	}
	if settings.JournalUnit != "" {
		return app.NewJournalLogSource(logs.NewJournalSource(journalOptions(settings))), nil
	}
	if settings.LogPath != "" {
		return app.NewLogSource(logs.NewTailer(logs.Options{Path: settings.LogPath})), nil
	}
	return nil, nil
}

// journalOptions maps the resolved settings onto the journal source options.
func journalOptions(settings config.Settings) logs.JournalOptions {
	return logs.JournalOptions{Unit: settings.JournalUnit}
}

// runTUI runs the Bubble Tea program and then closes the configured log
// source and the external-change monitor. The log source owns its process
// lifetime — the journal source spawns a supervisor goroutine around
// journalctl that must be released on exit — and the monitor owns the
// fsnotify watcher, so every path out of the program releases them
// exactly once. Closing is best-effort: a Close error must never mask a
// Run error nor turn a successful run into a failure.
func runTUI(run func() (tea.Model, error), logSource app.LogSource, monitor app.ChangeMonitor) error {
	if _, err := run(); err != nil {
		closeLogSource(logSource)
		closeChangeMonitor(monitor)
		return fmt.Errorf("run TUI: %w", err)
	}
	closeLogSource(logSource)
	closeChangeMonitor(monitor)
	return nil
}

// closeChangeMonitor closes monitor when non-nil, ignoring the error:
// shutdown is best-effort and Close is idempotent.
func closeChangeMonitor(monitor app.ChangeMonitor) {
	if monitor != nil {
		_ = monitor.Close()
	}
}

// closeLogSource closes logSource when non-nil, ignoring the error:
// shutdown is best-effort and every LogSource implementation is idempotent.
func closeLogSource(logSource app.LogSource) {
	if logSource != nil {
		_ = logSource.Close()
	}
}
