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
)
