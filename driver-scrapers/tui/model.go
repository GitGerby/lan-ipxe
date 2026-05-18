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

// Column widths for the fixed table layout.
// Total: 18 + 8 + 12 + 14 + 30 = 82ch (fits standard 80ch terminal with padding)
const (
	colDevice   = 18
	colArch     = 8
	colVersion  = 12
	colStatus   = 14
	colProgress = 30
)

// Model is the Bubble Tea TUI model for displaying driver scrape progress.
type Model struct {
	width  int
	height int
	// Providers in order, each with pre-populated devices
	providers []*providerState
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
	devices []*deviceState // pre-populated, stable order
	done    int
	failed  int
}

type deviceState struct {
	prefix   string
	arch     string
	version  string
	progress float64
	done     bool
	failed   bool
	phase    string // "waiting", "searching", "selected", "downloading", "extracting", "complete", "failed"
}

// Minimum terminal dimensions.
const (
	minWidth  = 80
	minHeight = 10
)

// NewModel creates a new TUI model with pre-populated providers.
func NewModel(providerInfos []core.ProviderInfo) *Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(colorAmber))

	m := &Model{
		width:     minWidth,
		height:    minHeight,
		providers: make([]*providerState, 0),
		spinner:   s,
	}

	// Pre-populate all providers and devices immediately
	m.initializeProviders(providerInfos)

	return m
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
		return m, nil
	}

	return m, nil
}

// tickCmd is a lightweight periodic command that triggers re-renders.
func tickCmd(t time.Time) tea.Msg {
	return timeMsg(t)
}

type timeMsg time.Time

// Init returns the initial TUI commands.
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

	availWidth := m.width
	if availWidth < minWidth {
		availWidth = minWidth
	}
	availHeight := m.height
	if availHeight < minHeight {
		availHeight = minHeight
	}

	var b strings.Builder
	b.Grow(availWidth * availHeight)

	// Header line
	b.WriteString(m.renderHeader(availWidth))
	b.WriteString("\n")

	// Separator
	b.WriteString(m.renderSeparator(availWidth))
	b.WriteString("\n")

	// Provider sections rendered into buffer for truncation
	providerContent := m.renderProviders(availWidth)

	// Calculate available lines for provider content.
	// Fixed: header(1) + sep(1) + overallSep(1) + overall(1) + footer(1) = 5
	fixedLines := 5
	maxProviderLines := availHeight - fixedLines
	if maxProviderLines < 1 {
		maxProviderLines = 1
	}

	// Truncate if needed
	lines := strings.Count(providerContent, "\n")
	if lines > maxProviderLines {
		providerContent = truncateToLines(providerContent, maxProviderLines)
	}

	b.WriteString(providerContent)

	// Bottom separator + overall
	b.WriteString(m.renderSeparator(availWidth))
	b.WriteString("\n")
	b.WriteString(m.renderOverall(availWidth))

	// Footer padding
	contentLines := 1 + 1 + strings.Count(providerContent, "\n") + 1 + 1
	padding := availHeight - contentLines
	if padding < 1 {
		padding = 1
	}
	for i := 0; i < padding; i++ {
		b.WriteString("\n")
	}
	b.WriteString(fmt.Sprintf(" %s Press q to quit", m.spinner.View()))

	return b.String()
}

func (m *Model) renderHeader(width int) string {
	title := "LAN-iPXE DRIVER SCRAPER"
	now := time.Now()
	timestamp := fmt.Sprintf("%02d/%02d %02d:%02d", now.Month(), now.Day(), now.Hour(), now.Minute())

	rightPad := max(width-len(title)-len(timestamp)-2, 1)

	return headerStyle.Render(title + strings.Repeat(" ", rightPad) + timestamp)
}

func (m *Model) renderSeparator(width int) string {
	runes := max(width-1, 20)
	return separatorStyle.Render(strings.Repeat("─", runes))
}

func (m *Model) renderProviders(width int) string {
	var b strings.Builder

	for i, ps := range m.providers {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(m.renderProviderSection(ps))
	}

	return b.String()
}

func (m *Model) renderProviderSection(ps *providerState) string {
	var b strings.Builder

	// Provider header line
	b.WriteString(m.renderProviderHeader(ps))
	b.WriteString("\n")

	// Device rows (all devices, always visible)
	for _, ds := range ps.devices {
		b.WriteString(m.renderDeviceRow(ds))
		b.WriteString("\n")
	}

	return b.String()
}

func (m *Model) renderProviderHeader(ps *providerState) string {
	// Count active phases
	downloading := 0
	extracting := 0
	for _, ds := range ps.devices {
		switch ds.phase {
		case "downloading":
			downloading++
		case "extracting":
			extracting++
		}
	}

	// Build summary
	var summaryParts []string
	summaryParts = append(summaryParts, fmt.Sprintf("%d/%d DONE", ps.done, len(ps.devices)))
	if downloading > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d DOWNLOADING", downloading))
	}
	if extracting > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d EXTRACTING", extracting))
	}

	summary := strings.Join(summaryParts, "  ")
	header := strings.ToUpper(ps.name) + "  " + summary

	return providerStyle.Render(header)
}

func (m *Model) renderDeviceRow(ds *deviceState) string {
	// Device prefix (left-aligned, truncated)
	deviceStr := padRight(ds.prefix, colDevice)

	// Arch tag (centered)
	archStr := m.renderArch(ds.arch)

	// Version (left-aligned)
	var versionStr string
	if ds.version != "" {
		versionStr = padRight("v"+ds.version, colVersion)
	} else {
		versionStr = strings.Repeat(" ", colVersion)
	}

	// Status column: symbol + phase text
	statusStr := m.renderStatus(ds)

	// Progress bar (fixed width)
	barStr := m.renderProgressBar(ds)

	return "  " + deviceStr + " " + archStr + " " + versionStr + " " + statusStr + " " + barStr
}

func (m *Model) renderArch(arch string) string {
	if arch == "" {
		return lipgloss.NewStyle().Width(colArch).Align(lipgloss.Center).
			Foreground(lipgloss.Color(colorDimGray)).Render("—")
	}
	tag := "[" + arch + "]"
	return archStyle.Render(padCenter(tag, colArch))
}

func (m *Model) renderStatus(ds *deviceState) string {
	var symbol string
	var phaseText string

	switch {
	case ds.done:
		symbol = doneStyle.Render(charDone)
		phaseText = ""
	case ds.phase == "skipped":
		symbol = failStyle.Render(charFail)
		phaseText = statusTextStyle.Render("skipped")
	case ds.failed:
		symbol = failStyle.Render(charFail)
		phaseText = ""
	case ds.phase == "searching":
		symbol = activeStyle.Render(m.spinner.View())
		phaseText = statusTextStyle.Render("searching")
	case ds.phase == "selected":
		symbol = readyStyle.Render(charActive)
		phaseText = ""
	case ds.phase == "found":
		symbol = readyStyle.Render(charActive)
		phaseText = statusTextStyle.Render("found")
	case ds.phase == "downloaded":
		symbol = readyStyle.Render(charActive)
		phaseText = statusTextStyle.Render("downloaded")
	case ds.phase == "downloading":
		symbol = activeStyle.Render(m.spinner.View())
		phaseText = statusTextStyle.Render("downloading")
	case ds.phase == "extracting":
		symbol = activeStyle.Render(m.spinner.View())
		phaseText = statusTextStyle.Render("extracting")
	default:
		symbol = waitStyle.Render(charIdle)
		phaseText = ""
	}

	var statusStr string
	if phaseText != "" {
		statusStr = symbol + " " + phaseText
	} else {
		statusStr = symbol
	}

	return lipgloss.NewStyle().Width(colStatus).Render(statusStr)
}

func (m *Model) renderProgressBar(ds *deviceState) string {
	switch {
	case ds.done:
		return doneBar(colProgress)
	case ds.failed:
		return failBar(colProgress)
	case ds.phase == "downloading" || ds.phase == "extracting":
		return progressBar(ds.progress, colProgress)
	default:
		return emptyBar(colProgress)
	}
}

func (m *Model) renderOverall(width int) string {
	fraction := 0.0
	if m.totalDevices > 0 {
		fraction = float64(m.doneDevices) / float64(m.totalDevices)
	}

	bar := progressBar(fraction, colProgress)

	text := fmt.Sprintf(" %d/%d COMPLETE", m.doneDevices, m.totalDevices)
	if m.downloading > 0 {
		text += fmt.Sprintf("  %d DOWNLOADING", m.downloading)
	}
	if m.extracting > 0 {
		text += fmt.Sprintf("  %d EXTRACTING", m.extracting)
	}

	return overallStyle.Render(text + "  " + bar)
}

// handleProgress processes a progress event.
func (m *Model) handleProgress(ev core.ProgressEvent) {
	// Find provider
	ps := m.findProvider(ev.Provider)
	if ps == nil {
		return
	}

	// Find device within provider
	ds := m.findDevice(ps, ev.Device)

	switch ev.Type {
	case core.EventProviderStart:
		// Set all devices to searching phase (first device) or keep waiting
		for _, d := range ps.devices {
			if d.phase == "waiting" {
				// Will be set to searching when EventDeviceSearchStart fires
				break
			}
		}

	case core.EventDeviceSearchStart:
		if ds != nil {
			ds.phase = "searching"
		}

	case core.EventDeviceSearchDone:
		if ds != nil {
			if strings.HasPrefix(ev.Status, "Found") {
				ds.phase = "found"
			} else if ds.phase == "searching" {
				ds.phase = "failed"
				ds.failed = true
				ps.failed++
			}
		}

	case core.EventDeviceSkipped:
		if ds != nil {
			ds.phase = "skipped"
			ds.failed = true
			ps.failed++
		}

	case core.EventPackageSelected:
		if ds != nil {
			ds.arch = ev.Arch
			ds.version = ev.Version
			ds.phase = "selected"
		}

	case core.EventDownloadStart:
		if ds != nil {
			ds.arch = ev.Arch
			ds.phase = "downloading"
			ds.progress = 0
			m.downloading++
		}

	case core.EventDownloadProgress:
		if ds != nil {
			ds.progress = ev.Progress
		}

	case core.EventDownloadDone:
		if ds != nil {
			ds.arch = ev.Arch
			ds.progress = 1.0
			ds.phase = "downloaded"
			m.downloading--
		}

	case core.EventExtractStart:
		if ds != nil {
			ds.arch = ev.Arch
			ds.phase = "extracting"
			m.extracting++
		}

	case core.EventExtractDone:
		if ds != nil {
			ds.arch = ev.Arch
			ds.phase = "complete"
			m.extracting--
		}

	case core.EventDeviceComplete:
		if ds != nil {
			ds.done = true
			ds.phase = "complete"
			ds.version = ev.Version
			ds.arch = ev.Arch
			m.doneDevices++
			ps.done++
		}

	case core.EventProviderDone:
		// Nothing to update at provider level

	case core.EventProviderFailed:
		if ds != nil {
			ds.failed = true
			ds.phase = "failed"
		}
		ps.failed++
	}
}

// initializeProviders pre-populates all providers and devices from config.
func (m *Model) initializeProviders(providers []core.ProviderInfo) {
	m.providers = make([]*providerState, 0, len(providers))
	m.totalDevices = 0

	for _, p := range providers {
		ps := &providerState{
			name:    p.Name,
			devices: make([]*deviceState, 0, len(p.Devices)),
		}

		for _, device := range p.Devices {
			ds := &deviceState{
				prefix: device,
				phase:  "waiting",
			}
			ps.devices = append(ps.devices, ds)
			m.totalDevices++
		}

		m.providers = append(m.providers, ps)
	}
}

// findProvider returns the provider state by name, or nil.
func (m *Model) findProvider(name string) *providerState {
	for _, ps := range m.providers {
		if ps.name == name {
			return ps
		}
	}
	return nil
}

// findDevice returns the device state by prefix, or nil.
func (m *Model) findDevice(ps *providerState, prefix string) *deviceState {
	for _, ds := range ps.devices {
		if ds.prefix == prefix {
			return ds
		}
	}
	return nil
}

// padRight pads s to width with spaces on the right, truncating if needed.
func padRight(s string, width int) string {
	if len(s) >= width {
		return s[:width]
	}
	return s + strings.Repeat(" ", width-len(s))
}

// padCenter centers s within width, padding with spaces on both sides.
func padCenter(s string, width int) string {
	if len(s) >= width {
		return s[:width]
	}
	left := (width - len(s)) / 2
	right := width - len(s) - left
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
}

// truncateToLines truncates s to at most n lines.
func truncateToLines(s string, n int) string {
	cut := 0
	for i := 0; i < n; i++ {
		idx := strings.IndexRune(s[cut:], '\n')
		if idx == -1 {
			return s
		}
		cut += idx + 1
	}
	return s[:cut]
}
