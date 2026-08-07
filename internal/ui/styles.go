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
)
