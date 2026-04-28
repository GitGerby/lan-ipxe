package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/gitgerby/lan-ipxe/driver-scrapers/core"
)

// Model is the Bubble Tea TUI model for displaying driver scrape progress.
type Model struct {
	width  int
	height int
	// Provider insertion order (preserves first-seen order)
	providerOrder []string
	// Per-provider state
	providerStates map[string]*providerState
	// Overall state
	totalDevices int
	doneDevices  int
	downloading  int
	extracting   int
	// Spinner
	spinner  spinner.Model
	quitting bool
}

type providerState struct {
	name    string
	devices map[string]*deviceState
	total   int
	done    int
	failed  int
	// Phase tracking
	searching int // devices still searching
	ready     int // devices ready for download (found packages)
	status    string
	// subStatus shows the currently active device operation (e.g., "Searching I225-V...")
	subStatus string
	// Currently active device key for this provider (shows spinner)
	activeDeviceKey string
}

type deviceState struct {
	prefix   string
	arch     string
	version  string
	progress float64
	done     bool
	failed   bool
	phase    string // "searching", "selected", "downloading", "extracting", "complete", "failed"
}

// Layout constants - computed dynamically based on terminal width.
// Minimum terminal dimensions the TUI can function in.
const (
	minWidth  = 40
	minHeight = 10
)

// NewModel creates a new TUI model.
func NewModel() *Model {
	s := spinner.New()
	s.Spinner = spinner.Line
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("135"))

	return &Model{
		width:          minWidth,
		height:         minHeight,
		providerOrder:  make([]string, 0),
		providerStates: make(map[string]*providerState),
		spinner:        s,
	}
}

// statusPriority returns a numeric priority for provider status strings.
// Higher values mean a more "advanced" state that should not be downgraded.
func statusPriority(s string) int {
	switch s {
	case "Complete":
		return 5
	case "Extracting...":
		return 4
	case "Downloading...":
		return 3
	case "Searching...":
		return 2
	case "Starting", "Waiting...":
		return 1
	default:
		return 0
	}
}

// advanceStatus returns newStatus if its priority is >= current status priority,
// otherwise returns the current status (preventing downgrade).
func advanceStatus(current, newStatus string) string {
	if statusPriority(newStatus) >= statusPriority(current) {
		return newStatus
	}
	return current
}

// Update handles TUI messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = clamp(msg.Width, minWidth, msg.Width)
		m.height = clamp(msg.Height, minHeight, msg.Height)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case core.ProgressEvent:
		m.handleProgress(msg)
		return m, nil

	case timeMsg:
		// Periodic tick — trigger re-render for smooth spinner animation
		return m, nil
	}

	// Trigger re-render on any unhandled message
	return m, nil
}

// tickCmd is a lightweight periodic command that triggers re-renders
// so the spinner animates smoothly even between progress events.
func tickCmd(t time.Time) tea.Msg {
	return timeMsg(t)
}

type timeMsg time.Time

// Init returns the initial TUI commands: spinner tick + periodic re-render timer.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		tea.Every(100*time.Millisecond, tickCmd),
	)
}

// View renders the TUI.
func (m *Model) View() string {
	if m.quitting {
		return ""
	}

	// Calculate dynamic widths based on terminal size.
	// Reserve space for:
	//   - header (1 line)
	//   - separator (1 line)
	//   - blank line (1 line)
	//   - overall progress (2 lines)
	//   - blank line (1 line)
	//   - footer (1 line)
	// = 7 fixed lines
	// Remaining lines are for provider blocks.
	availableWidth := m.width
	if availableWidth < minWidth {
		availableWidth = minWidth
	}
	availableHeight := m.height
	if availableHeight < minHeight {
		availableHeight = minHeight
	}

	var b strings.Builder
	// Pre-allocate a reasonable capacity
	b.Grow(m.width * m.height)

	// Header
	b.WriteString(headerStyle.Render("=== LAN-iPXE Driver Scraper ==="))
	b.WriteString("\n")

	// Separator
	sepWidth := availableWidth - 2 // account for separator padding
	if sepWidth < 20 {
		sepWidth = 20
	}
	b.WriteString(separatorStyle.Render(strings.Repeat("─", sepWidth)))
	b.WriteString("\n")

	// Provider list - render into a separate buffer first so we can
	// measure and truncate if needed.
	providerContent := m.renderProviders(availableWidth)

	// Count lines in provider content to decide if we need to truncate.
	// Fixed lines: header(1) + sep(1) + overall(2) + footer(2) = 6
	fixedLines := 6
	maxProviderLines := availableHeight - fixedLines
	if maxProviderLines < 1 {
		maxProviderLines = 1
	}

	// Truncate provider content if it exceeds available height.
	if strings.Count(providerContent, "\n") > maxProviderLines {
		providerContent = truncateToLines(providerContent, maxProviderLines)
	}

	b.WriteString(providerContent)

	// Overall progress
	b.WriteString(m.renderOverall(availableWidth))

	// Fill remaining space to keep the layout stable.
	contentHeight := 1 + 1 + 1 + // header + sep + newline
		strings.Count(providerContent, "\n") + 1 +
		2 + // overall
		1 // newline before footer
	paddingLines := availableHeight - contentHeight
	if paddingLines < 0 {
		paddingLines = 0
	}
	if paddingLines > 3 {
		paddingLines = 3
	}

	// Footer with spinner
	for i := 0; i < paddingLines; i++ {
		b.WriteString("\n")
	}
	b.WriteString(fmt.Sprintf(" %s Press q to quit", m.spinner.View()))

	return b.String()
}

// truncateToLines truncates s to at most n lines, cutting at newline boundaries.
// Returns the first n lines of s (including their trailing newlines).
func truncateToLines(s string, n int) string {
	cut := 0
	for i := 0; i < n; i++ {
		idx := strings.IndexRune(s[cut:], '\n')
		if idx == -1 {
			// Fewer than n lines remain — return everything.
			return s
		}
		cut += idx + 1
	}
	// Return everything up to the nth newline (inclusive).
	return s[:cut]
}

func (m *Model) renderProviders(availableWidth int) string {
	var b strings.Builder

	// Device bar width: reserve space for prefix + arch + status + padding
	// Format: "  [PREFIX][ARCH]BBBBBBBBBBBBBB v1.2.3 ✓"
	// Prefix ~8, arch ~5, brackets 4, version ~10, status ~3, spaces ~6 = ~36
	// Provider bar width: reserve space for prefix + padding
	// Format: "     BBBBBBBBBBBBBBBBBBBB"
	// prefix ~6, so bar gets the rest
	deviceBarWidth := clamp(availableWidth-42, 10, 30)
	providerBarWidth := clamp(availableWidth-12, 15, 40)

	for _, name := range m.providerOrder {
		ps := m.providerStates[name]
		if ps == nil {
			continue
		}

		// Provider header with phase status
		b.WriteString(m.renderProviderHeader(ps))
		b.WriteString("\n")

		// Sub-status line (shows active device operation for this provider)
		if ps.subStatus != "" {
			b.WriteString(fmt.Sprintf("     %s\n", statusStyle.Render(ps.subStatus)))
		}

		// Per-provider progress bar + counts
		if ps.total > 0 {
			fraction := m.providerFraction(ps)
			bar := progressBar(fraction, providerBarWidth)
			b.WriteString(fmt.Sprintf("     %s\n", progressBarStyle.Render(bar)))

			// Count line - only show when there's meaningful data
			if ps.ready > 0 || ps.done > 0 || ps.failed > 0 {
				b.WriteString(fmt.Sprintf("     %d found, %d done, %d failed\n",
					ps.ready, ps.done, ps.failed))
			} else if ps.searching > 0 {
				b.WriteString(fmt.Sprintf("     Searching %d device(s)...\n", ps.searching))
			}
		}

		// Device lines - show all devices with results or active status
		for _, ds := range ps.devices {
			// Show devices that are done, failed, have results, or are active
			if ds.done || ds.failed || ds.version != "" || ds.phase == "downloading" || ds.phase == "extracting" || ds.phase == "selected" || ds.phase == "found" {
				isActive := ps.activeDeviceKey == ds.prefix
				line := m.renderDeviceLine(ds, isActive, deviceBarWidth)
				b.WriteString(line)
				b.WriteString("\n")
			}
		}

		b.WriteString("\n")
	}

	return b.String()
}

func (m *Model) renderProviderHeader(ps *providerState) string {
	// Phase status
	phaseStatus := ps.status
	if phaseStatus == "" {
		phaseStatus = "Waiting..."
	}

	// If there's a sub-status, show it with the spinner
	if ps.subStatus != "" {
		return fmt.Sprintf(" %s  %s %s",
			providerStyle.Render(ps.name),
			m.spinner.View(),
			statusStyle.Render(ps.subStatus))
	}

	// Otherwise just show the phase status
	return fmt.Sprintf(" %s (%s)",
		providerStyle.Render(ps.name), statusStyle.Render(phaseStatus))
}

func (m *Model) providerFraction(ps *providerState) float64 {
	if ps.total == 0 {
		return 0
	}
	// Fraction is based on completed devices
	return float64(ps.done) / float64(ps.total)
}

func (m *Model) renderDeviceLine(ds *deviceState, active bool, barWidth int) string {
	// Progress bar
	var bar string
	if ds.done {
		bar = doneBar(barWidth)
	} else if ds.failed {
		bar = failBar(barWidth)
	} else if ds.phase == "downloading" || ds.phase == "extracting" {
		bar = progressBar(ds.progress, barWidth)
	} else {
		bar = idleBar(barWidth)
	}

	// Status symbol
	var status string
	if ds.done {
		status = doneStyle.Render(charDone)
	} else if ds.failed {
		status = failStyle.Render(charFail)
	} else if active && (ds.phase == "downloading" || ds.phase == "extracting") {
		status = m.spinner.View()
	} else if ds.phase == "selected" || ds.phase == "complete" {
		status = statusStyle.Render(charActive)
	} else {
		status = statusStyle.Render(charIdle)
	}

	// Version
	version := ""
	if ds.version != "" {
		version = versionStyle.Render(" v" + ds.version)
	}

	// Phase label (only for active devices being processed)
	var phaseLabel string
	if active && ds.phase != "" {
		phaseLabel = " " + ds.phase
	}

	return deviceStyle.Render(fmt.Sprintf("  %s[%s]%s%s %s%s",
		ds.prefix, ds.arch, bar, version, status, phaseLabel))
}

func (m *Model) renderOverall(availableWidth int) string {
	// Overall progress bar
	overallFraction := 0.0
	if m.totalDevices > 0 {
		overallFraction = float64(m.doneDevices) / float64(m.totalDevices)
	}

	barWidth := clamp(availableWidth-12, 15, 50)
	bar := progressBar(overallFraction, barWidth)

	var b strings.Builder
	b.WriteString(fmt.Sprintf(" %s\n", progressBarStyle.Render(bar)))

	// Text summary
	text := fmt.Sprintf("Overall: %d/%d devices complete", m.doneDevices, m.totalDevices)
	if m.downloading > 0 {
		text += fmt.Sprintf("    Downloading: %d", m.downloading)
	}
	if m.extracting > 0 {
		text += fmt.Sprintf("    Extracting: %d", m.extracting)
	}

	b.WriteString("\n" + overallStyle.Render(text))

	return b.String()
}

func (m *Model) handleProgress(ev core.ProgressEvent) {
	ps, exists := m.providerStates[ev.Provider]
	if !exists {
		m.providerOrder = append(m.providerOrder, ev.Provider)
		m.providerStates[ev.Provider] = &providerState{
			name:    ev.Provider,
			devices: make(map[string]*deviceState),
		}
		ps = m.providerStates[ev.Provider]
	}

	switch ev.Type {
	case core.EventProviderStart:
		ps.status = "Searching..."
		ps.subStatus = ""
		ps.searching = 0

	case core.EventDeviceSearchStart:
		// Search events have no Arch — use device name only as key.
		key := ev.Device
		ds, ok := ps.devices[key]
		if !ok {
			ds = &deviceState{prefix: ev.Device, arch: "x64", phase: "searching"}
			ps.devices[key] = ds
			ps.total++
			ps.searching++
			m.totalDevices++
		}
		// Update sub-status to show which device is being searched
		if strings.HasPrefix(ev.Status, "Searching") {
			ps.subStatus = ev.Status
			ps.activeDeviceKey = key
		}

	case core.EventDeviceSearchDone:
		// Search done events also have no Arch — look up by device name only.
		key := ev.Device
		if ds, ok := ps.devices[key]; ok {
			ps.searching--
			if strings.HasPrefix(ev.Status, "Found") {
				ds.phase = "found"
				ps.status = fmt.Sprintf("Found packages for %s", ev.Device)
			} else if ds.phase == "searching" {
				ds.phase = "failed"
				ds.failed = true
				ps.failed++
			}
			if ps.activeDeviceKey == key {
				ps.activeDeviceKey = ""
			}
		}

	case core.EventPackageSelected:
		// Selection events have Arch. Look up by device name (the key used during search),
		// then update the arch field now that we know it.
		key := ev.Device
		if ds, ok := ps.devices[key]; ok {
			ds.arch = ev.Arch
			ds.version = ev.Version
			ds.phase = "selected"
			ps.ready++
			ps.status = fmt.Sprintf("Selected v%s for %s", ev.Version, ev.Device)
		}

	case core.EventDownloadStart:
		key := ev.Device
		if ds, ok := ps.devices[key]; ok {
			ds.arch = ev.Arch
			ds.phase = "downloading"
			ds.progress = 0
			m.downloading++
			ps.subStatus = fmt.Sprintf("Downloading %s...", ev.Device)
			ps.status = advanceStatus(ps.status, "Downloading...")
			ps.activeDeviceKey = key
		}

	case core.EventDownloadProgress:
		key := ev.Device
		if ds, ok := ps.devices[key]; ok {
			ds.progress = ev.Progress
		}

	case core.EventDownloadDone:
		key := ev.Device
		if ds, ok := ps.devices[key]; ok {
			ds.arch = ev.Arch
			ds.progress = 1.0
			ds.phase = "downloaded"
			m.downloading--
			ps.status = advanceStatus(ps.status, fmt.Sprintf("Downloaded %s", ev.Device))
			if ps.activeDeviceKey == key {
				ps.activeDeviceKey = ""
			}
		}

	case core.EventExtractStart:
		key := ev.Device
		if ds, ok := ps.devices[key]; ok {
			ds.arch = ev.Arch
			ds.phase = "extracting"
			m.extracting++
			ps.subStatus = fmt.Sprintf("Extracting %s...", ev.Device)
			ps.status = advanceStatus(ps.status, "Extracting...")
			ps.activeDeviceKey = key
		}

	case core.EventExtractDone:
		key := ev.Device
		if ds, ok := ps.devices[key]; ok {
			ds.arch = ev.Arch
			ds.phase = "complete"
			m.extracting--
			ps.status = advanceStatus(ps.status, fmt.Sprintf("Extracted %s", ev.Device))
			if ps.activeDeviceKey == key {
				ps.activeDeviceKey = ""
			}
		}

	case core.EventDeviceComplete:
		key := ev.Device
		if ds, ok := ps.devices[key]; ok {
			ds.done = true
			ds.phase = "complete"
			m.doneDevices++
			ps.done++
		}

	case core.EventProviderDone:
		ps.status = "Complete"
		ps.subStatus = ""
		ps.activeDeviceKey = ""

	case core.EventProviderFailed:
		ps.failed++
		ps.status = "Failed"
		ps.subStatus = ev.Message
		ps.activeDeviceKey = ""
	}
}
