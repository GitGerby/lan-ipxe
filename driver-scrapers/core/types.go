package core

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
)

// Event type constants
type EventType int

const (
	EventProviderStart EventType = iota
	EventDeviceSearchStart
	EventDeviceSearchDone
	EventPackageSelected
	EventDownloadStart
	EventDownloadProgress
	EventDownloadDone
	EventExtractStart
	EventExtractDone
	EventDeviceComplete
	EventProviderDone
	EventProviderFailed
	EventDone
)

// String returns a human-readable string for the event type.
func (e EventType) String() string {
	switch e {
	case EventProviderStart:
		return "PROVIDER_START"
	case EventDeviceSearchStart:
		return "DEVICE_SEARCH_START"
	case EventDeviceSearchDone:
		return "DEVICE_SEARCH_DONE"
	case EventPackageSelected:
		return "PACKAGE_SELECTED"
	case EventDownloadStart:
		return "DOWNLOAD_START"
	case EventDownloadProgress:
		return "DOWNLOAD_PROGRESS"
	case EventDownloadDone:
		return "DOWNLOAD_DONE"
	case EventExtractStart:
		return "EXTRACT_START"
	case EventExtractDone:
		return "EXTRACT_DONE"
	case EventDeviceComplete:
		return "DEVICE_COMPLETE"
	case EventProviderDone:
		return "PROVIDER_DONE"
	case EventProviderFailed:
		return "PROVIDER_FAILED"
	case EventDone:
		return "DONE"
	default:
		return "UNKNOWN"
	}
}

// ProviderInfo describes a provider and its devices for TUI initialization.
type ProviderInfo struct {
	Name    string   // display name
	Devices []string // device prefixes
}

// ProgressEvent is sent via the progress channel to report status updates.
type ProgressEvent struct {
	Type     EventType
	Provider string  // provider name (e.g., "Intel Ethernet")
	Device   string  // device prefix or model name
	Arch     string  // "AMD64" or "ARM64"
	Version  string  // selected package version
	Progress float64 // 0.0 - 1.0
	Status   string  // human-readable status
	Message  string  // additional details (filename, error, etc.)
	Err      error   // non-nil if EventProviderFailed
}

// ProgressChan is a channel for ProgressEvent values.
type ProgressChan chan ProgressEvent

// Send sends a progress event, non-blocking if channel is full.
func (pc ProgressChan) Send(ev ProgressEvent) {
	select {
	case pc <- ev:
	default:
		// Drop event if channel is full to avoid blocking the producer
	}
}

// SendBlocking sends a progress event, blocking with a timeout if channel is full.
func (pc ProgressChan) SendBlocking(ev ProgressEvent) {
	select {
	case pc <- ev:
	case <-time.After(5 * time.Second):
		// Log warning: channel full, event dropped after timeout
	}
}

// DriverPackage represents a selected driver package ready for download.
type DriverPackage struct {
	ProviderName string
	DevicePrefix string
	FamilyName   string
	Arch         string
	Version      string
	UpdateID     string
	CabURL       string
	CabPath      string // local path after download
	ExtractPath  string // local path after extraction
}

// SelectionResult holds the result of package selection.
type SelectionResult struct {
	Package *DriverPackage
	Reason  string // why this package was selected
}

// SelectionStrategy determines how to pick the best package.
type SelectionStrategy int

const (
	// NewestByDate selects the package with the newest date.
	NewestByDate SelectionStrategy = iota
	// SemanticVersion selects the package with the highest semantic version.
	SemanticVersion
	// SemanticVersionWithBranch selects by preferred version branch first, then highest.
	SemanticVersionWithBranch
)

// DeviceTarget represents a hardware target to search for.
// SelectionStrategy and ExcludeNDIS are per-device to allow different
// selection behavior for each device within the same provider.
type DeviceTarget struct {
	Prefix            string
	HWID              string
	FamilyName        string
	Queries           []string
	PreferredBranches []string
	SelectionStrategy SelectionStrategy
	ExcludeNDIS       bool
}

// DriverProvider is the interface that all driver providers must implement.
type DriverProvider interface {
	// Name returns the display name for this provider.
	Name() string
	// ProviderKey returns the CLI key for this provider (e.g., "intel-eth").
	ProviderKey() string
	// Devices returns the list of hardware targets to search.
	// Each DeviceTarget carries its own SelectionStrategy and ExcludeNDIS.
	Devices() []DeviceTarget
}

// ProviderResult holds the results for a single provider run.
type ProviderResult struct {
	ProviderName string
	Success      int
	Failed       int
	Skipped      int
	Errors       []error
}

// Orchestrator manages the driver scraping workflow.
type Orchestrator struct {
	provider DriverProvider
	client   *CatalogClient
	cfg      *OrchestratorConfig
	progress ProgressChan
	ctx      context.Context
	cancel   context.CancelFunc
	results  sync.Map // map[string]*DriverPackage
}

// OrchestratorConfig holds configuration for the orchestrator.
type OrchestratorConfig struct {
	AcceptedArchs  []string
	OutputDir      string
	DetailThrottle int
	MaxDownloads   int
	NoDownload     bool
	NoExtract      bool
	Timeout        int // seconds
	Verbose        bool
}

// DefaultOrchestratorConfig returns config with sensible defaults.
func DefaultOrchestratorConfig() *OrchestratorConfig {
	return &OrchestratorConfig{
		AcceptedArchs:  []string{"AMD64"},
		OutputDir:      "./drivers",
		DetailThrottle: 8,
		MaxDownloads:   4,
		Timeout:        300,
	}
}

// NewOrchestrator creates a new orchestrator for the given provider.
func NewOrchestrator(provider DriverProvider, cfg *OrchestratorConfig, progress ProgressChan) *Orchestrator {
	ctx, cancel := context.WithCancel(context.Background())
	return &Orchestrator{
		provider: provider,
		client:   NewCatalogClient(0), // 0 means use default timeout from config
		cfg:      cfg,
		progress: progress,
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Run executes the full driver scraping workflow for the provider.
func (o *Orchestrator) Run() *ProviderResult {
	result := &ProviderResult{
		ProviderName: o.provider.Name(),
	}

	// Send provider start event
	o.progress.Send(ProgressEvent{
		Type:     EventProviderStart,
		Provider: o.provider.Name(),
		Status:   "Starting",
	})

	// Build search devices (with per-device strategy)
	devices := make([]SearchDevice, 0, len(o.provider.Devices()))
	for _, dt := range o.provider.Devices() {
		devices = append(devices, SearchDevice{
			Prefix:            dt.Prefix,
			HWID:              dt.HWID,
			FamilyName:        dt.FamilyName,
			Queries:           dt.Queries,
			PreferredBranches: dt.PreferredBranches,
			SelectionStrategy: dt.SelectionStrategy,
			ExcludeNDIS:       dt.ExcludeNDIS,
		})
	}

	// Search - use first device's ExcludeNDIS as default, overridden per-device
	searchCfg := &SearchConfig{
		ProviderName:   o.provider.Name(),
		AcceptedArchs:  o.cfg.AcceptedArchs,
		DetailThrottle: o.cfg.DetailThrottle,
		Progress:       o.progress,
	}

	// Streaming pipeline: search all devices concurrently, then select + download
	// each device as soon as its search completes. This avoids head-of-line blocking
	// where a slow search for one device delays downloads for all other devices.
	//
	// Architecture:
	//  1. Per-device goroutines search → select, then send result to packageChan
	//  2. A consumer goroutine reads packageChan and schedules download via errgroup
	//  3. Downloads are throttled by MaxDownloads
	//  4. Consumer signals done when channel is closed, then g.Wait() ensures
	//     all downloads complete before returning

	// Download and extract each package in parallel (throttled by MaxDownloads)
	g, ctx := errgroup.WithContext(o.ctx)
	g.SetLimit(o.cfg.MaxDownloads)

	var successCount, failedCount, skippedCount int32

	// Channel to collect selected packages (after search + select per device)
	type deviceResult struct {
		pkg     *DriverPackage
		skipped bool
	}

	packageChan := make(chan deviceResult, len(devices))

	// Track when all search+select producers have sent their results.
	var producerWg sync.WaitGroup
	producerWg.Add(len(devices))

	// Launch per-device search+select goroutines (all run concurrently).
	for i := range devices {
		dev := devices[i]
		go func() {
			defer producerWg.Done()

			// Search
			searchResults := SearchDeviceWithContext(o.ctx, o.client, dev, searchCfg)

			// Select best package for this device
			var pkg *DriverPackage
			if len(searchResults) > 0 {
				pkg = o.selectSingle(dev.Prefix, searchResults, dev.SelectionStrategy)
			}

			if pkg == nil {
				packageChan <- deviceResult{skipped: true}
				return
			}

			// Send selection event immediately so TUI shows progress
			o.progress.Send(ProgressEvent{
				Type:     EventPackageSelected,
				Provider: o.provider.Name(),
				Device:   pkg.DevicePrefix,
				Arch:     pkg.Arch,
				Version:  pkg.Version,
				Status:   fmt.Sprintf("Selected v%s for %s", pkg.Version, pkg.DevicePrefix),
			})

			packageChan <- deviceResult{pkg: pkg}
		}()
	}

	// Close the channel once all producers are done.
	go func() {
		producerWg.Wait()
		close(packageChan)
	}()

	// Consumer goroutine: stream results and schedule downloads immediately.
	// This avoids head-of-line blocking - a device that finishes search first
	// starts downloading right away without waiting for slower devices.
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		for res := range packageChan {
			if res.skipped {
				atomic.AddInt32(&skippedCount, 1)
				continue
			}

			pkg := res.pkg
			g.Go(func() error {
				select {
				case <-ctx.Done():
					atomic.AddInt32(&skippedCount, 1)
					return nil // non-fatal cancellation
				default:
				}

				// Download
				if !o.cfg.NoDownload {
					if err := o.downloadPackage(pkg); err != nil {
						atomic.AddInt32(&failedCount, 1)
						o.progress.Send(ProgressEvent{
							Type:     EventProviderFailed,
							Provider: o.provider.Name(),
							Device:   pkg.DevicePrefix,
							Arch:     pkg.Arch,
							Status:   "Failed",
							Message:  err.Error(),
						})
						return err // fail this package, continue others
					}
				}

				// Extract
				if !o.cfg.NoExtract {
					if err := o.extractPackage(pkg); err != nil {
						atomic.AddInt32(&failedCount, 1)
						o.progress.Send(ProgressEvent{
							Type:     EventProviderFailed,
							Provider: o.provider.Name(),
							Device:   pkg.DevicePrefix,
							Arch:     pkg.Arch,
							Status:   "Extract failed",
							Message:  err.Error(),
						})
						return err // fail this package, continue others
					}
				}

				atomic.AddInt32(&successCount, 1)
				// Send per-device completion event (not provider-level)
				o.progress.Send(ProgressEvent{
					Type:     EventDeviceComplete,
					Provider: o.provider.Name(),
					Device:   pkg.DevicePrefix,
					Arch:     pkg.Arch,
					Version:  pkg.Version,
					Status:   "Complete",
					Progress: 1.0,
				})
				return nil
			})
		}
	}()

	// Wait for consumer to finish scheduling all downloads, then wait for downloads.
	// Order matters: we must not call g.Wait() until all g.Go() calls are made,
	// otherwise g.Wait() could return before some downloads are scheduled.
	<-consumerDone
	g.Wait()

	result.Success = int(atomic.LoadInt32(&successCount))
	result.Failed = int(atomic.LoadInt32(&failedCount))
	result.Skipped = int(atomic.LoadInt32(&skippedCount))

	o.progress.Send(ProgressEvent{
		Type:     EventProviderDone,
		Provider: o.provider.Name(),
		Status:   "Complete",
		Message:  formatResultSummary(result),
	})

	return result
}

// Cancel cancels the current operation.
func (o *Orchestrator) Cancel() {
	o.cancel()
}

func formatResultSummary(r *ProviderResult) string {
	return "Success: " + strconv.Itoa(r.Success) + ", Failed: " + strconv.Itoa(r.Failed) + ", Skipped: " + strconv.Itoa(r.Skipped)
}
