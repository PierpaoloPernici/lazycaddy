// Package ui contains the Bubble Tea models, views and keybindings of
// lazycaddy. UI models emit intents and depend on application interfaces
// (internal/app.Loader); they never touch the filesystem, command execution
// or the Caddy runtime directly.
package ui

import "github.com/charmbracelet/lipgloss"

// Theme-aware styles: important state is never conveyed by color alone,
// so labels like "RO" and error text are always printed explicitly.
// Colors come from theme.go and use lipgloss.AdaptiveColor so they degrade
// gracefully to ANSI-256, ANSI-16 or Ascii/NoColor profiles.
var (
	// brandStyle renders the application name and version in the header.
	brandStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(accentColor)
	readOnlyBadge = lipgloss.NewStyle().
			Bold(true).
			Padding(0, 1).
			Foreground(badgeMutedForeground).
			Background(mutedColor)
	writableBadge = lipgloss.NewStyle().
			Bold(true).
			Padding(0, 1).
			Foreground(badgeSuccessForeground).
			Background(successColor)
	// loadedBadge marks the state where the running Caddy configuration
	// provably matches the saved file.
	loadedBadge = lipgloss.NewStyle().
			Bold(true).
			Padding(0, 1).
			Foreground(badgeSuccessForeground).
			Background(successColor)
	// staleBadge marks a saved-but-not-reloaded configuration: the file
	// on disk is newer than the running config. The amber background
	// signals "action pending".
	staleBadge = lipgloss.NewStyle().
			Bold(true).
			Padding(0, 1).
			Foreground(badgeWarningForeground).
			Background(warningColor)
	// unreachableBadge marks a reload that failed because the Admin API
	// could not be reached. The dark-red background signals a degraded
	// operational state.
	unreachableBadge = lipgloss.NewStyle().
				Bold(true).
				Padding(0, 1).
				Foreground(badgeErrorForeground).
				Background(errorColor)
	// reloadingBadge marks an in-flight reload. The teal background is
	// distinct from the loaded and stale states.
	reloadingBadge = lipgloss.NewStyle().
			Bold(true).
			Padding(0, 1).
			Foreground(badgeReloadForeground).
			Background(lipgloss.Color("30"))
	// unknownBadge signals that nothing has been proven yet. The neutral
	// grey matches the dim status style so it reads as quiet rather than
	// alarming.
	unknownBadge = lipgloss.NewStyle().
			Bold(true).
			Padding(0, 1).
			Foreground(badgeMutedForeground).
			Background(mutedColor)
	// unsavedBadge marks in-memory edits that are not yet on disk. It
	// carries an explicit UNSAVED label (never color alone) and uses the
	// amber warning palette so it reads as "action pending", mirroring
	// the stale badge.
	unsavedBadge = lipgloss.NewStyle().
			Bold(true).
			Padding(0, 1).
			Foreground(badgeWarningForeground).
			Background(warningColor)
	// runtimeRunningBadge marks a Caddy daemon reachable through the
	// Admin API.
	runtimeRunningBadge = lipgloss.NewStyle().
				Bold(true).
				Padding(0, 1).
				Foreground(badgeSuccessForeground).
				Background(successColor)
	// runtimeStoppedBadge marks a queryable caddy binary whose Admin API
	// is unreachable (daemon stopped or admin disabled).
	runtimeStoppedBadge = lipgloss.NewStyle().
				Bold(true).
				Padding(0, 1).
				Foreground(badgeErrorForeground).
				Background(errorColor)
	// runtimeUnreachableBadge marks a runtime probe that could not
	// complete (timeout or cancellation). The amber background matches
	// the stale badge and reads as "state unknown, action pending".
	runtimeUnreachableBadge = lipgloss.NewStyle().
				Bold(true).
				Padding(0, 1).
				Foreground(badgeWarningForeground).
				Background(warningColor)
	errorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(errorColor).
			Padding(0, 1)
	// paneStyle is the default unfocused pane border.
	paneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(mutedColor).
			Padding(0, 1)
	// focusedPaneStyle is the focused/active pane border. It uses the
	// single accent color so the current input target is obvious.
	focusedPaneStyle = paneStyle.Copy().
				BorderForeground(accentColor)
	// selectedTreeRowStyle uses the same blue accent as the command palette
	// selector so the active structural row is visible without a full-width
	// background block.
	selectedTreeRowStyle = lipgloss.NewStyle().
				Foreground(accentColor)
	// commandPaletteStyle is an opaque, centered modal. The opaque background
	// makes the palette readable over the still-visible application chrome.
	commandPaletteStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(accentColor).
				Background(paletteBackground).
				Padding(0, 1)
	commandPaletteSurfaceStyle = lipgloss.NewStyle().
					Background(paletteBackground)
	commandPaletteSelectedStyle = lipgloss.NewStyle().
					Bold(true)
	commandPaletteDisabledStyle = lipgloss.NewStyle().
					Foreground(mutedColor)
	commandPaletteGroupStyle = lipgloss.NewStyle().
					Bold(true).
					Foreground(mutedColor)
	cursorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(accentColor)
	dimStyle = lipgloss.NewStyle().
			Foreground(mutedColor)
	// activeTitleStyle renders the title of the focused section or modal.
	activeTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(accentColor)
	// keyHintStyle renders key names in the footer hint line.
	keyHintStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(accentColor)
	// selectedGutterNumberStyle emphasizes the line numbers of the
	// currently selected section. It uses the same accent as the tree
	// selection while leaving source syntax colors untouched.
	selectedGutterNumberStyle = lipgloss.NewStyle().
					Bold(true).
					Foreground(accentColor)
	// selectedGutterBarStyle renders the thin vertical bar that marks
	// the selected block's lines in the source gutter.
	selectedGutterBarStyle = lipgloss.NewStyle().
				Foreground(accentColor)
	// footerStyle renders the navigation key hint line at the bottom.
	// It only provides padding; key names and descriptions carry their own
	// colors so the accent is not lost. Its surface is applied after nested
	// key styles have rendered so the background cannot be interrupted.
	footerStyle = lipgloss.NewStyle().
			Padding(0, 1)
	statusSuccessStyle = lipgloss.NewStyle().
				Bold(true).
				Padding(0, 1).
				Foreground(successColor)
	// statusWarningStyle renders transient warning messages. Warnings
	// keep the ✗ glyph contract (tests assert the exact prefix) but are
	// styled distinctly from errors.
	statusWarningStyle = lipgloss.NewStyle().
				Bold(true).
				Padding(0, 1).
				Foreground(warningColor)
	statusInfoStyle = lipgloss.NewStyle().
			Bold(true).
			Padding(0, 1).
			Foreground(infoColor)
	statusErrorStyle = lipgloss.NewStyle().
				Bold(true).
				Padding(0, 1).
				Foreground(errorColor)

	// diffAddStyle highlights lines added by the working copy (lines
	// that start with '+') in the conventional unified-diff green.
	diffAddStyle = lipgloss.NewStyle().
			Foreground(successColor)
	// diffRemoveStyle highlights lines removed from the original source
	// (lines that start with '-') in the conventional unified-diff red,
	// matching the error-style foreground for visual consistency.
	diffRemoveStyle = lipgloss.NewStyle().
			Foreground(errorColor)
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
				Foreground(mutedColor).
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
				Foreground(accentColor)
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
			Foreground(accentColor)
	// logNullStyle renders null literals dimmed.
	logNullStyle = lipgloss.NewStyle().
			Foreground(mutedColor)
	// logDelimiterStyle renders braces, brackets, colons and commas
	// dimmed so the structural tokens recede.
	logDelimiterStyle = lipgloss.NewStyle().
				Foreground(mutedColor)
	// logTimestampStyle renders the ts value in blue.
	logTimestampStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("33"))
	// logMsgStyle renders the msg value bold white.
	logMsgStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15"))
	// logLoggerStyle renders the logger value dimmed and italic.
	logLoggerStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			Italic(true)
	// logLevelDebugStyle renders the debug level in bright magenta.
	logLevelDebugStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("201"))
	// logLevelInfoStyle renders the info level in blue.
	logLevelInfoStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("33"))
	// logLevelWarnStyle renders the warn level in amber.
	logLevelWarnStyle = lipgloss.NewStyle().
				Foreground(warningColor)
	// logLevelErrorStyle renders the error level in red.
	logLevelErrorStyle = lipgloss.NewStyle().
				Foreground(errorColor)
	// logLevelOtherStyle renders unrecognized levels dimmed.
	logLevelOtherStyle = lipgloss.NewStyle().
				Foreground(mutedColor)
	// logStatus1xxStyle renders 1xx status values dimmed (rare
	// informational responses).
	logStatus1xxStyle = lipgloss.NewStyle().
				Foreground(mutedColor)
	// logStatus2xxStyle renders 2xx status values in green.
	logStatus2xxStyle = lipgloss.NewStyle().
				Foreground(successColor)
	// logStatus3xxStyle renders 3xx status values in amber.
	logStatus3xxStyle = lipgloss.NewStyle().
				Foreground(warningColor)
	// logStatus4xxStyle renders 4xx status values in amber.
	logStatus4xxStyle = lipgloss.NewStyle().
				Foreground(warningColor)
	// logStatus5xxStyle renders 5xx status values in red.
	logStatus5xxStyle = lipgloss.NewStyle().
				Foreground(errorColor)
	// logStatusOtherStyle renders unrecognized status values dimmed.
	logStatusOtherStyle = lipgloss.NewStyle().
				Foreground(mutedColor)
	// logMethodStyle renders the request method in bold cyan.
	logMethodStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("81"))
	// logURIStyle renders the request URI underlined in cyan.
	logURIStyle = lipgloss.NewStyle().
			Underline(true).
			Foreground(lipgloss.Color("81"))
)
