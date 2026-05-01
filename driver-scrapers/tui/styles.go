package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Bloomberg Terminal color palette.
const (
	colorBg       = "#0A0A1E" // Dark navy background
	colorWhite    = "#FFFFFF" // Headers, titles
	colorAmber    = "#FFB815" // Primary data, provider headers, active
	colorCyan     = "#00BFFF" // Secondary labels, device prefixes, status text
	colorDimCyan  = "#5BA3C0" // Arch tags
	colorGreen    = "#00E676" // Success, complete
	colorRed      = "#FF1744" // Failure, errors
	colorDimGray  = "#607D8B" // Waiting, inactive
	colorBarEmpty = "#2A2A3E" // Unfilled progress bar
	colorSep      = "#3A3A5C" // Separators
)

// Pre-computed style constants for efficient rendering.
var (
	// Header
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(colorWhite)).
			Background(lipgloss.Color(colorBg)).
			MarginTop(1)

	// Provider section header
	providerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(colorAmber)).
			Background(lipgloss.Color(colorBg))

	// Device prefix
	deviceStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorCyan))

	// Arch tag
	archStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorDimCyan))

	// Version
	versionStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorAmber))

	// Status text (phase label like "searching", "downloading")
	statusTextStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorCyan))

	// Done symbol
	doneStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorGreen))

	// Failed symbol
	failStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorRed))

	// Active symbol (spinner for active device)
	activeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorAmber))

	// Waiting/idle symbol
	waitStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorDimGray))

	// Selected/ready symbol
	readyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorCyan))

	// Separator
	separatorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorSep)).
			Background(lipgloss.Color(colorBg))

	// Overall/footer
	overallStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(colorWhite)).
			Background(lipgloss.Color(colorBg))

	// Progress bar style (bold for crisp rendering)
	progressBarStyle = lipgloss.NewStyle().
				Bold(true).
				Background(lipgloss.Color(colorBg))
)

// Characters
const (
	charBarFill  = "█"
	charBarEmpty = "░"
	charDone     = "✓"
	charFail     = "✗"
	charActive   = "•"
	charIdle     = "·"
)

// Pre-computed single-character styled strings for progress bars.
var (
	barFillDone   string
	barFillFail   string
	barFillActive string
	barFillEmpty  string
)

func init() {
	barFillDone = lipgloss.NewStyle().Foreground(lipgloss.Color(colorGreen)).SetString(charBarFill).String()
	barFillFail = lipgloss.NewStyle().Foreground(lipgloss.Color(colorRed)).SetString(charBarFill).String()
	barFillActive = lipgloss.NewStyle().Foreground(lipgloss.Color(colorAmber)).SetString(charBarFill).String()
	barFillEmpty = lipgloss.NewStyle().Foreground(lipgloss.Color(colorBarEmpty)).SetString(charBarEmpty).String()
}

// progressBar renders a progress bar of the given width and fill fraction.
// Uses pre-computed styled characters for efficient rendering.
func progressBar(fraction float64, width int) string {
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}

	filled := int(math.Round(fraction * float64(width)))
	if filled > width {
		filled = width
	}
	empty := width - filled

	var fill string
	if fraction >= 1.0 {
		fill = barFillDone
	} else {
		fill = barFillActive
	}

	var sb strings.Builder
	sb.Grow(width * 6) // rough capacity for wide chars
	for i := 0; i < filled; i++ {
		sb.WriteString(fill)
	}
	for i := 0; i < empty; i++ {
		sb.WriteString(barFillEmpty)
	}
	return sb.String()
}

// doneBar returns a fully-filled green bar.
func doneBar(width int) string {
	return strings.Repeat(barFillDone, width)
}

// failBar returns a fully-filled red bar.
func failBar(width int) string {
	return strings.Repeat(barFillFail, width)
}

// emptyBar returns a fully empty bar.
func emptyBar(width int) string {
	return strings.Repeat(barFillEmpty, width)
}

// clamp returns v clamped to [lo, hi].
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
