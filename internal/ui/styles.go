// Package ui contains the Bubble Tea models, views and keybindings of
// lazycaddy. UI models emit intents and depend on application interfaces
// (internal/app.Loader); they never touch the filesystem, command execution
// or the Caddy runtime directly.
package ui

import "github.com/charmbracelet/lipgloss"

// Palette-agnostic styles: important state is never conveyed by color alone,
// so labels like "READ-ONLY" and error text are always printed explicitly.
var (
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Padding(0, 1).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("236"))
	readOnlyBadge = lipgloss.NewStyle().
			Bold(true).
			Padding(0, 1).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("238"))
	writableBadge = lipgloss.NewStyle().
			Bold(true).
			Padding(0, 1).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("28"))
	// loadedBadge marks the state where the running Caddy configuration
	// provably matches the saved file. The dark-blue background keeps it
	// distinct from the neutral read-only grey.
	loadedBadge = lipgloss.NewStyle().
			Bold(true).
			Padding(0, 1).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("24"))
	// staleBadge marks a saved-but-not-reloaded configuration: the file
	// on disk is newer than the running config. The amber background
	// signals "action pending".
	staleBadge = lipgloss.NewStyle().
			Bold(true).
			Padding(0, 1).
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("214"))
	// unreachableBadge marks a reload that failed because the Admin API
	// could not be reached. The dark-red background signals a degraded
	// operational state.
	unreachableBadge = lipgloss.NewStyle().
				Bold(true).
				Padding(0, 1).
				Foreground(lipgloss.Color("15")).
				Background(lipgloss.Color("52"))
	// reloadingBadge marks an in-flight reload. The teal background is
	// distinct from both the loaded and stale states.
	reloadingBadge = lipgloss.NewStyle().
			Bold(true).
			Padding(0, 1).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("30"))
	// unknownBadge signals that nothing has been proven yet. The neutral
	// grey matches the dim status style so it reads as quiet rather than
	// alarming.
	unknownBadge = lipgloss.NewStyle().
			Bold(true).
			Padding(0, 1).
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("244"))
	// runtimeRunningBadge marks a Caddy daemon reachable through the
	// Admin API. The green background matches the writable mode badge.
	runtimeRunningBadge = lipgloss.NewStyle().
				Bold(true).
				Padding(0, 1).
				Foreground(lipgloss.Color("15")).
				Background(lipgloss.Color("28"))
	// runtimeStoppedBadge marks a queryable caddy binary whose Admin API
	// is unreachable (daemon stopped or admin disabled). The dark-red
	// background matches the unreachable badge.
	runtimeStoppedBadge = lipgloss.NewStyle().
				Bold(true).
				Padding(0, 1).
				Foreground(lipgloss.Color("15")).
				Background(lipgloss.Color("52"))
	// runtimeUnreachableBadge marks a runtime probe that could not
	// complete (timeout or cancellation). The amber background matches
	// the stale badge and reads as "state unknown, action pending".
	runtimeUnreachableBadge = lipgloss.NewStyle().
				Bold(true).
				Padding(0, 1).
				Foreground(lipgloss.Color("0")).
				Background(lipgloss.Color("214"))
	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("203")).
			Padding(0, 1)
	paneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1)
	cursorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("212"))
	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245"))
	// selectedGutterNumberStyle emphasizes the line numbers of the
	// currently selected section. It stays quiet (grey, not the cursor
	// accent) so it reads as a subtle guide rather than state.
	selectedGutterNumberStyle = lipgloss.NewStyle().
					Bold(true).
					Foreground(lipgloss.Color("250"))
	// selectedGutterBarStyle renders the thin vertical bar that marks
	// the selected block's lines in the source gutter.
	selectedGutterBarStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("245"))
	statusLineStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Foreground(lipgloss.Color("245"))
	statusSuccessStyle = lipgloss.NewStyle().
				Bold(true).
				Padding(0, 1).
				Foreground(lipgloss.Color("42"))
	statusInfoStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Foreground(lipgloss.Color("244"))

	// diffAddStyle highlights lines added by the working copy (lines
	// that start with '+') in the conventional unified-diff green.
	diffAddStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("42"))
	// diffRemoveStyle highlights lines removed from the original source
	// (lines that start with '-') in the conventional unified-diff red,
	// matching the error-style foreground for visual consistency.
	diffRemoveStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("203"))
	// diffHunkStyle highlights '@@ -l,c +l,c @@' hunk headers in bold
	// cyan so the boundaries between change groups stand out.
	diffHunkStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("81"))
	// diffFileStyle highlights the ---/+++ file header pair in bold
	// bright white so the compared filenames are immediately visible.
	diffFileStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15"))

	// Syntax highlighting styles are presentation-only: they never carry
	// state, so every decision is made from the parsed source alone.
	// syntaxCommentStyle renders "# ..." comments dimmed and italic so they
	// recede behind the directives.
	syntaxCommentStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("245")).
				Italic(true)
	// syntaxStringStyle renders quoted string tokens in the soft green used
	// elsewhere for "success" content.
	syntaxStringStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("114"))
	// syntaxHeredocStyle shares the string color: a heredoc body is the same
	// kind of literal content as a quoted string.
	syntaxHeredocStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("114"))
	// syntaxPlaceholderStyle reuses the cursor accent so {…} sub-tokens read
	// as active/editable regions.
	syntaxPlaceholderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("212"))
	// syntaxBraceStyle renders structural braces in soft yellow so block
	// boundaries are easy to scan.
	syntaxBraceStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("221"))
	// syntaxWordStyle is intentionally empty: barewords keep the default
	// foreground so the source reads like a plain file.
	syntaxWordStyle = lipgloss.NewStyle()

	// Log token styles follow the zap CapitalColorLevelEncoder palette:
	// the token text itself carries the level/status label, so color is a
	// consistent reinforcement rather than the only signal.
	// logKeyStyle renders JSON object keys in bold cyan.
	logKeyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("81"))
	// logStringStyle renders JSON string values in the soft green shared
	// with the Caddyfile string style.
	logStringStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("114"))
	// logNumberStyle renders JSON numbers in soft yellow.
	logNumberStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("221"))
	// logBoolStyle renders true/false literals in the cursor accent.
	logBoolStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("212"))
	// logNullStyle renders null literals dimmed.
	logNullStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245"))
	// logDelimiterStyle renders braces, brackets, colons and commas
	// dimmed so the structural tokens recede.
	logDelimiterStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("245"))
	// logTimestampStyle renders the ts value in blue.
	logTimestampStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("33"))
	// logMsgStyle renders the msg value bold white.
	logMsgStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15"))
	// logLoggerStyle renders the logger value dimmed and italic.
	logLoggerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Italic(true)
	// logLevelDebugStyle renders the debug level in bright magenta.
	logLevelDebugStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("201"))
	// logLevelInfoStyle renders the info level in blue.
	logLevelInfoStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("33"))
	// logLevelWarnStyle renders the warn level in amber.
	logLevelWarnStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("214"))
	// logLevelErrorStyle renders the error level in red.
	logLevelErrorStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("203"))
	// logLevelOtherStyle renders unrecognized levels dimmed.
	logLevelOtherStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("245"))
	// logStatus1xxStyle renders 1xx status values dimmed (rare
	// informational responses).
	logStatus1xxStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("245"))
	// logStatus2xxStyle renders 2xx status values in green.
	logStatus2xxStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("42"))
	// logStatus3xxStyle renders 3xx status values in amber.
	logStatus3xxStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("214"))
	// logStatus4xxStyle renders 4xx status values in amber.
	logStatus4xxStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("214"))
	// logStatus5xxStyle renders 5xx status values in red.
	logStatus5xxStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("203"))
	// logStatusOtherStyle renders unrecognized status values dimmed.
	logStatusOtherStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("245"))
	// logMethodStyle renders the request method in bold cyan.
	logMethodStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("81"))
	// logURIStyle renders the request URI underlined in cyan.
	logURIStyle = lipgloss.NewStyle().
			Underline(true).
			Foreground(lipgloss.Color("81"))
)
