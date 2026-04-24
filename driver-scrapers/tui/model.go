package tui

import (
	"fmt"
	"strings"

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
	status  string
	message string
}

type deviceState struct {
	prefix   string
	arch     string
	version  string
	status   string
	progress float64
	message  string
	done     bool
	failed   bool
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
// Bubble Tea automatically sends a tea.WindowSizeMsg after starting,
// which will update m.width and m.height via the Update handler.
func (m *Model) Init() tea.Cmd {
	return m.spinner.Tick
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

	return m, nil
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

	// Provider sections
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

	// Render each provider in insertion order
	for _, name := range m.providerOrder {
		ps := m.providerStates[name]
		if ps == nil {
			continue
		}

		// Provider header
		header := fmt.Sprintf(" %s (%d/%d devices)",
			providerStyle.Render(ps.name), ps.done, ps.total)
		b.WriteString(header)
		b.WriteString("\n")

		// Provider progress bar
		if ps.total > 0 {
			fraction := float64(ps.done) / float64(ps.total)
			bar := progressBarWithColor(fraction, 20)
			b.WriteString(fmt.Sprintf("     %s\n", progressBarStyle.Render(bar)))
		}

		// Device lines
		for _, ds := range ps.devices {
			line := m.renderDeviceLine(ds)
			b.WriteString(line)
			b.WriteString("\n")
		}

		b.WriteString("\n")
	}

	return b.String()
}

func (m *Model) renderDeviceLine(ds *deviceState) string {
	// Progress bar
	var bar string
	if ds.done {
		bar = doneStyle.Render("████████████████")
	} else if ds.failed {
		bar = failStyle.Render("████░░░░░░░░░░░░")
	} else {
		bar = progressBarWithColor(ds.progress, 16)
	}

	// Status symbol
	var status string
	if ds.done {
		status = doneStyle.Render(" ✓ ")
	} else if ds.failed {
		status = failStyle.Render(" ✗ ")
	} else if ds.progress > 0 {
		status = statusStyle.Render(" → ")
	} else {
		status = statusStyle.Render(" · ")
	}

	// Version
	version := ""
	if ds.version != "" {
		version = versionStyle.Render(" v" + ds.version)
	}

	return deviceStyle.Render(fmt.Sprintf("  %s[%s]%s%s%s%s",
		ds.prefix, ds.arch, bar, version, status, ds.status))
}

func (m *Model) renderOverall() string {
	text := fmt.Sprintf("Overall: %d/%d devices complete", m.doneDevices, m.totalDevices)
	if m.downloading > 0 {
		text += fmt.Sprintf("    Downloading: %d", m.downloading)
	}
	if m.extracting > 0 {
		text += fmt.Sprintf("    Extracting: %d", m.extracting)
	}

	return "\n" + overallStyle.Render(text)
}

func (m *Model) handleProgress(ev core.ProgressEvent) {
	// Track provider insertion order
	if _, ok := m.providerStates[ev.Provider]; !ok {
		m.providerOrder = append(m.providerOrder, ev.Provider)
		m.providerStates[ev.Provider] = &providerState{
			name:    ev.Provider,
			devices: make(map[string]*deviceState),
		}
	}

	ps := m.providerStates[ev.Provider]

	switch ev.Type {
	case core.EventProviderStart:
		ps.status = "Starting..."

	case core.EventDeviceSearchStart:
		key := deviceKey(ev.Device, ev.Arch)
		ds, ok := ps.devices[key]
		if !ok {
			ds = &deviceState{prefix: ev.Device, arch: ev.Arch}
			ps.devices[key] = ds
			ps.total++
			m.totalDevices++
		}
		ds.status = ev.Status
		ds.message = ev.Message

	case core.EventDeviceSearchDone:
		key := deviceKey(ev.Device, ev.Arch)
		if ds, ok := ps.devices[key]; ok {
			ds.status = "Search complete"
		}

	case core.EventPackageSelected:
		key := deviceKey(ev.Device, ev.Arch)
		if ds, ok := ps.devices[key]; ok {
			ds.version = ev.Version
			ds.status = ev.Status
			ds.message = ev.Message
		}

	case core.EventDownloadStart:
		key := deviceKey(ev.Device, ev.Arch)
		if ds, ok := ps.devices[key]; ok {
			ds.status = "Downloading..."
			ds.progress = 0
			m.downloading++
		}

	case core.EventDownloadProgress:
		key := deviceKey(ev.Device, ev.Arch)
		if ds, ok := ps.devices[key]; ok {
			ds.progress = ev.Progress
			ds.status = ev.Status
			ds.message = ev.Message
		}

	case core.EventDownloadDone:
		key := deviceKey(ev.Device, ev.Arch)
		if ds, ok := ps.devices[key]; ok {
			ds.progress = 1.0
			ds.status = "Download complete"
			ds.message = ev.Message
			m.downloading--
		}

	case core.EventExtractStart:
		key := deviceKey(ev.Device, ev.Arch)
		if ds, ok := ps.devices[key]; ok {
			ds.status = "Extracting..."
			m.extracting++
		}

	case core.EventExtractDone:
		key := deviceKey(ev.Device, ev.Arch)
		if ds, ok := ps.devices[key]; ok {
			ds.status = "Extract complete"
			ds.message = ev.Message
			m.extracting--
		}

	case core.EventProviderDone:
		// Distinguish per-device vs per-provider events
		if ev.Device != "" && ev.Arch != "" {
			// Per-device completion
			key := deviceKey(ev.Device, ev.Arch)
			if ds, ok := ps.devices[key]; ok {
				ds.done = true
				ds.status = ev.Status
				m.doneDevices++
				ps.done++
			}
		}
		ps.status = "Complete"
		ps.message = ev.Message

	case core.EventProviderFailed:
		ps.failed++
		ps.status = "Failed"
		ps.message = ev.Message
	}
}

func (m *Model) estimateHeight() int {
	lines := 4 // header + separator + overall + footer
	for _, ps := range m.providerStates {
		lines += 1 // provider header
		lines += len(ps.devices)
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
