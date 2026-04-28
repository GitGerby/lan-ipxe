package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Pre-computed style constants for efficient rendering.
var (
	// Colors
	colorHeader   = lipgloss.Color("252")
	colorProvider = lipgloss.Color("14")
	colorDevice   = lipgloss.Color("252")
	colorStatus   = lipgloss.Color("241")
	colorDone     = lipgloss.Color("42")
	colorFail     = lipgloss.Color("196")
	colorSpinner  = lipgloss.Color("135")
	colorVersion  = lipgloss.Color("39")
	colorSep      = lipgloss.Color("237")
	colorOverall  = lipgloss.Color("205")

	// Progress bar colors by fill level
	colorBarLow   = lipgloss.Color("220") // <50% : yellow
	colorBarMid   = lipgloss.Color("34")  // 50-99% : green
	colorBarFull  = colorHeader           // 100% : bright white
	colorBarEmpty = lipgloss.Color("235") // unfilled : dark gray

	// Characters
	charBarFill  = "█"
	charBarEmpty = "░"
	charDone     = "✓"
	charFail     = "✗"
	charActive   = "•"
	charIdle     = "·"
	charDash     = "─"

	// Styles
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorHeader).
			MarginTop(1)

	providerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorProvider)

	deviceStyle = lipgloss.NewStyle().
			Foreground(colorDevice)

	statusStyle = lipgloss.NewStyle().
			Foreground(colorStatus)

	doneStyle = lipgloss.NewStyle().
			Foreground(colorDone)

	failStyle = lipgloss.NewStyle().
			Foreground(colorFail)

	progressBarStyle = lipgloss.NewStyle().
				Bold(true)

	overallStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorOverall).
			MarginTop(1)

	versionStyle = lipgloss.NewStyle().
			Foreground(colorVersion)

	separatorStyle = lipgloss.NewStyle().
			Foreground(colorSep).
			Padding(0, 1)
)

// Pre-computed single-character styled strings for progress bars.
// These are computed once and reused on every frame to avoid lipgloss
// allocation overhead in the hot render path.
var (
	barFillLow  string
	barFillMid  string
	barFillFull string
	barFillDone string
	barFillFail string
	barEmpty    string
	dashStyled  string
)

func init() {
	barFillLow = lipgloss.NewStyle().Foreground(colorBarLow).SetString(charBarFill).String()
	barFillMid = lipgloss.NewStyle().Foreground(colorBarMid).SetString(charBarFill).String()
	barFillFull = lipgloss.NewStyle().Foreground(colorBarFull).SetString(charBarFill).String()
	barFillDone = lipgloss.NewStyle().Foreground(colorDone).SetString(charBarFill).String()
	barFillFail = lipgloss.NewStyle().Foreground(colorFail).SetString(charBarFill).String()
	barEmpty = lipgloss.NewStyle().Foreground(colorBarEmpty).SetString(charBarEmpty).String()
	dashStyled = lipgloss.NewStyle().Foreground(colorStatus).SetString(charDash).String()
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

	// Pick fill character based on completion level
	var fill string
	if fraction >= 1.0 {
		fill = barFillFull
	} else if fraction >= 0.5 {
		fill = barFillMid
	} else {
		fill = barFillLow
	}

	var sb strings.Builder
	sb.Grow(width * len(fill)) // rough capacity hint
	for i := 0; i < filled; i++ {
		sb.WriteString(fill)
	}
	for i := 0; i < empty; i++ {
		sb.WriteString(barEmpty)
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

// idleBar returns a dashed bar for devices with no progress yet.
func idleBar(width int) string {
	return strings.Repeat(dashStyled, width)
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
