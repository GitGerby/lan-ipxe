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

// NewModel creates a new TUI model.
func NewModel() *Model {
	s := spinner.New()
	s.Spinner = spinner.Line
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("135"))

	return &Model{
		width:          80,
		height:         24,
		providerOrder:  make([]string, 0),
		providerStates: make(map[string]*providerState),
		spinner:        s,
	}
}

// Init returns the initial TUI message.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, tea.Every(100*time.Millisecond, func(time.Time) tea.Msg {
		return spinner.TickMsg{}
	}))
}

// Update handles TUI messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
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
	}

	// On any message, re-render to update spinner/progress
	return m, m.spinner.Tick
}

// View renders the TUI.
func (m *Model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder

	// Header
	b.WriteString(headerStyle.Render("=== LAN-iPXE Driver Scraper ==="))
	b.WriteString("\n")

	// Separator
	sepWidth := m.width
	if sepWidth < 20 {
		sepWidth = 20
	}
	b.WriteString(separatorStyle.Render(strings.Repeat("─", sepWidth)))
	b.WriteString("\n\n")

	// Provider list
	b.WriteString(m.renderProviders())

	// Overall progress
	b.WriteString(m.renderOverall())

	// Fill remaining space
	remaining := m.height - m.estimateHeight()
	if remaining < 0 {
		remaining = 0
	}
	if remaining > 3 {
		remaining = 3
	}
	for i := 0; i < remaining; i++ {
		b.WriteString("\n")
	}

	// Footer
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf(" %s Press q to quit\n", m.spinner.View()))

	return b.String()
}

func (m *Model) renderProviders() string {
	var b strings.Builder

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
			bar := progressBarWithColor(fraction, 20)
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
			if ds.done || ds.failed || ds.version != "" || ds.phase == "downloading" || ds.phase == "extracting" {
				isActive := ps.activeDeviceKey == deviceKey(ds.prefix, ds.arch)
				line := m.renderDeviceLine(ds, isActive)
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

func (m *Model) renderDeviceLine(ds *deviceState, active bool) string {
	// Progress bar
	var bar string
	if ds.done {
		bar = doneStyle.Render("████████████████")
	} else if ds.failed {
		bar = failStyle.Render("████░░░░░░░░░░░░")
	} else if ds.phase == "downloading" || ds.phase == "extracting" {
		bar = progressBarWithColor(ds.progress, 14)
	} else {
		bar = statusStyle.Render("──────────────")
	}

	// Status symbol
	var status string
	if ds.done {
		status = doneStyle.Render("✓")
	} else if ds.failed {
		status = failStyle.Render("✗")
	} else if active && (ds.phase == "downloading" || ds.phase == "extracting") {
		status = m.spinner.View()
	} else if ds.phase == "selected" || ds.phase == "complete" {
		status = statusStyle.Render("•")
	} else {
		status = statusStyle.Render("·")
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

func (m *Model) renderOverall() string {
	// Overall progress bar
	overallFraction := 0.0
	if m.totalDevices > 0 {
		overallFraction = float64(m.doneDevices) / float64(m.totalDevices)
	}

	var b strings.Builder

	// Progress bar
	bar := progressBarWithColor(overallFraction, 20)
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
		key := deviceKey(ev.Device, ev.Arch)
		ds, ok := ps.devices[key]
		if !ok {
			ds = &deviceState{prefix: ev.Device, arch: ev.Arch, phase: "searching"}
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
		key := deviceKey(ev.Device, ev.Arch)
		if ds, ok := ps.devices[key]; ok {
			ps.searching--
			if strings.HasPrefix(ev.Status, "Found") {
				ds.phase = "selected"
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
		key := deviceKey(ev.Device, ev.Arch)
		if ds, ok := ps.devices[key]; ok {
			ds.version = ev.Version
			ds.phase = "selected"
			ps.ready++
			ps.status = fmt.Sprintf("Selected v%s for %s", ev.Version, ev.Device)
		}

	case core.EventDownloadStart:
		key := deviceKey(ev.Device, ev.Arch)
		if ds, ok := ps.devices[key]; ok {
			ds.phase = "downloading"
			ds.progress = 0
			m.downloading++
			ps.subStatus = fmt.Sprintf("Downloading %s...", ev.Device)
			ps.status = "Downloading..."
			ps.activeDeviceKey = key
		}

	case core.EventDownloadProgress:
		key := deviceKey(ev.Device, ev.Arch)
		if ds, ok := ps.devices[key]; ok {
			ds.progress = ev.Progress
		}

	case core.EventDownloadDone:
		key := deviceKey(ev.Device, ev.Arch)
		if ds, ok := ps.devices[key]; ok {
			ds.progress = 1.0
			ds.phase = "selected" // downloaded but not extracted yet
			m.downloading--
			ps.status = fmt.Sprintf("Downloaded %s", ev.Device)
			if ps.activeDeviceKey == key {
				ps.activeDeviceKey = ""
			}
		}

	case core.EventExtractStart:
		key := deviceKey(ev.Device, ev.Arch)
		if ds, ok := ps.devices[key]; ok {
			ds.phase = "extracting"
			m.extracting++
			ps.subStatus = fmt.Sprintf("Extracting %s...", ev.Device)
			ps.status = "Extracting..."
			ps.activeDeviceKey = key
		}

	case core.EventExtractDone:
		key := deviceKey(ev.Device, ev.Arch)
		if ds, ok := ps.devices[key]; ok {
			ds.phase = "complete"
			m.extracting--
			ps.status = fmt.Sprintf("Extracted %s", ev.Device)
			if ps.activeDeviceKey == key {
				ps.activeDeviceKey = ""
			}
		}

	case core.EventProviderDone:
		if ev.Device != "" && ev.Arch != "" {
			key := deviceKey(ev.Device, ev.Arch)
			if ds, ok := ps.devices[key]; ok {
				ds.done = true
				ds.phase = "complete"
				m.doneDevices++
				ps.done++
			}
		}
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

func (m *Model) estimateHeight() int {
	lines := 4 // header + separator + overall + footer
	for _, ps := range m.providerStates {
		lines += 3 // provider header + progress bar + count line
		// Count visible devices
		visible := 0
		for _, ds := range ps.devices {
			if ds.done || ds.failed || ds.version != "" || ds.phase == "downloading" || ds.phase == "extracting" {
				visible++
			}
		}
		lines += visible
		lines += 1 // blank line between providers
	}
	return lines
}

func deviceKey(device, arch string) string {
	if arch != "" {
		return fmt.Sprintf("%s-%s", device, arch)
	}
	return device
}
