package ui

import "github.com/charmbracelet/lipgloss"

// Palette. Every colour is adaptive so the UI stays legible on light and dark
// terminals, and every value is a 256-colour-safe hex that lipgloss degrades
// gracefully on poorer terminals.
var (
	colorAccent  = lipgloss.AdaptiveColor{Light: "#0969da", Dark: "#58a6ff"}
	colorText    = lipgloss.AdaptiveColor{Light: "#1f2328", Dark: "#e6edf3"}
	colorMuted   = lipgloss.AdaptiveColor{Light: "#59636e", Dark: "#8b949e"}
	colorFaint   = lipgloss.AdaptiveColor{Light: "#8c959f", Dark: "#6e7681"}
	colorBorder  = lipgloss.AdaptiveColor{Light: "#d1d9e0", Dark: "#30363d"}
	colorSuccess = lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"}
	colorWarning = lipgloss.AdaptiveColor{Light: "#9a6700", Dark: "#d29922"}
	colorDanger  = lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#f85149"}
	colorPurple  = lipgloss.AdaptiveColor{Light: "#8250df", Dark: "#bc8cff"}
)

var (
	// Chrome.
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#ffffff")).
			Background(colorAccent).
			Padding(0, 1)

	hostStyle = lipgloss.NewStyle().Foreground(colorFaint)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1)

	panelTitleStyle = lipgloss.NewStyle().Foreground(colorMuted).Bold(true)

	// Filters.
	labelStyle        = lipgloss.NewStyle().Foreground(colorMuted)
	focusedLabelStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)

	// Summary.
	headlineStyle = lipgloss.NewStyle().Bold(true).Foreground(colorText)
	totalStyle    = lipgloss.NewStyle().Bold(true).Foreground(colorSuccess)
	mutedStyle    = lipgloss.NewStyle().Foreground(colorMuted)
	faintStyle    = lipgloss.NewStyle().Foreground(colorFaint)

	// Suggestions.
	suggestionStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorAccent).
			Padding(0, 1)

	suggestionSelectedStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)

	// Feedback.
	successStyle = lipgloss.NewStyle().Foreground(colorSuccess)
	warningStyle = lipgloss.NewStyle().Foreground(colorWarning)
	dangerStyle  = lipgloss.NewStyle().Foreground(colorDanger)

	// List.
	selectedTitleStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	cursorStyle        = lipgloss.NewStyle().Foreground(colorAccent)
)

// kindStyles colours the badge on each contribution row. Reviews borrow
// GitHub's purple, merged pull requests included.
var kindStyles = map[string]lipgloss.Style{
	"commit": lipgloss.NewStyle().Foreground(colorMuted),
	"issue":  lipgloss.NewStyle().Foreground(colorWarning),
	"pr":     lipgloss.NewStyle().Foreground(colorSuccess),
	"merged": lipgloss.NewStyle().Foreground(colorPurple),
	"review": lipgloss.NewStyle().Foreground(colorAccent),
}

// statusLevel picks the style for a status line.
type statusLevel int

const (
	statusInfo statusLevel = iota
	statusOK
	statusWarn
	statusError
)

func (l statusLevel) style() lipgloss.Style {
	switch l {
	case statusOK:
		return successStyle
	case statusWarn:
		return warningStyle
	case statusError:
		return dangerStyle
	default:
		return mutedStyle
	}
}
