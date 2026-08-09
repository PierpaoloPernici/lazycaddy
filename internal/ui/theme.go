package ui

import "github.com/charmbracelet/lipgloss"

// Theme colors for lazycaddy. AdaptiveColor pairs resolve to the Light
// value on light backgrounds and the Dark value on dark backgrounds.
// lipgloss auto-degrades each pair to the detected terminal profile
// (TrueColor → ANSI-256 → ANSI-16 → Ascii/NoColor), so the fallback is
// free. At Ascii/NoColor only Bold, Reverse and Underline survive; every
// color-coded affordance also uses Bold or Reverse so state never depends
// on color alone.
var (
	// accentColor is the single primary accent used consistently for
	// focused panes, selected rows, active section titles, the brand
	// label and key hints.
	accentColor = lipgloss.AdaptiveColor{Light: "#0969da", Dark: "#58a6ff"}

	// Semantic colors are intentionally distinct from the accent.
	successColor = lipgloss.AdaptiveColor{Light: "#22863a", Dark: "#85e89d"}
	warningColor = lipgloss.AdaptiveColor{Light: "#b08800", Dark: "#ffea7f"}
	errorColor   = lipgloss.AdaptiveColor{Light: "#cb2431", Dark: "#f97583"}

	// infoColor and mutedColor are neutral greys used for status text,
	// dim labels and secondary metadata.
	infoColor  = lipgloss.AdaptiveColor{Light: "#57606a", Dark: "#8b949e"}
	mutedColor = lipgloss.AdaptiveColor{Light: "#6e7781", Dark: "#7d8590"}

	// Badge foregrounds adapt to the same light/dark terminal context as
	// their semantic backgrounds. This avoids low-contrast light-grey text
	// on green or amber badges in ANSI terminals.
	badgeSuccessForeground = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#0d1117"}
	badgeWarningForeground = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#0d1117"}
	badgeErrorForeground   = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#0d1117"}
	badgeMutedForeground   = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#ffffff"}
	badgeReloadForeground  = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#0d1117"}
)
