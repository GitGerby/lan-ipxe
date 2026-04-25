package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// SearchResult represents a candidate driver package found in the catalog.
type SearchResult struct {
	DevicePrefix string
	FamilyName   string
	Queries      []string
	Detail       *UpdateDetail
	Arch         string // Normalized to match ArchToCatalog output
}

// SearchDevice represents a single hardware target to search for.
type SearchDevice struct {
	Prefix            string
	HWID              string
	FamilyName        string
	Queries           []string
	PreferredBranches []string
	SelectionStrategy SelectionStrategy
	ExcludeNDIS       bool
}

// SearchConfig holds configuration for catalog search operations.
type SearchConfig struct {
	// ProviderName is the name of the provider (e.g., "Intel Ethernet")
	ProviderName string
	// AcceptedArchs are the architectures to accept (e.g., "AMD64", "ARM64")
	AcceptedArchs []string
	// DetailThrottle limits concurrent detail page fetches
	DetailThrottle int
	// ExcludeNDIS if true, excludes packages with "NDIS" in the title
	ExcludeNDIS bool
	// Progress channel for reporting search events
	Progress ProgressChan
}

// DefaultSearchConfig returns a SearchConfig with sensible defaults.
func DefaultSearchConfig() *SearchConfig {
	return &SearchConfig{
		DetailThrottle: 8,
		AcceptedArchs:  []string{"AMD64"},
		ExcludeNDIS:    false,
	}
}

// errorLogMu protects the errorLogFiles map.
var errorLogMu sync.Mutex

// errorLogFiles maps device names to their error log file handles.
var errorLogFiles = make(map[string]*os.File)

// openSearchErrorLog opens (or reuses) an error log file for a device.
func openSearchErrorLog(device string) (*os.File, string) {
	errorLogMu.Lock()
	defer errorLogMu.Unlock()

	if f, ok := errorLogFiles[device]; ok {
		return f, ""
	}

	path := filepath.Join(os.TempDir(), fmt.Sprintf("driver-search-errors-%s-%d.log", sanitizeFilename(device), time.Now().UnixNano()))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, ""
	}
	errorLogFiles[device] = f
	return f, path
}

// logSearchError writes a non-fatal error to the device's error log file.
func logSearchError(f *os.File, device, updateID string, err error) {
	if f != nil {
		fmt.Fprintf(f, "%s [%s] [%s]: %v\n", time.Now().Format(time.RFC3339), device, updateID, err)
	}
}

// SearchDeviceWithContext searches the catalog for a single device and returns all matching results.
func SearchDeviceWithContext(ctx context.Context, client *CatalogClient, dev SearchDevice, cfg *SearchConfig) []*SearchResult {
	var queries []string
	if len(dev.Queries) > 0 {
		queries = dev.Queries
	} else {
		queries = []string{dev.HWID + " Windows 11"}
	}

	var allResults []*SearchResult

	for _, query := range queries {
		select {
		case <-ctx.Done():
			return allResults
		default:
		}

		// Report search start - include device name for TUI
		if cfg.Progress != nil {
			cfg.Progress.Send(ProgressEvent{
				Type:     EventDeviceSearchStart,
				Provider: cfg.ProviderName,
				Device:   dev.Prefix,
				Status:   fmt.Sprintf("Searching %s...", dev.Prefix),
			})
		}

		// Search the catalog
		updateIDs, err := client.Search(query)
		if err != nil {
			// Don't fail on single query error, try next query
			if cfg.Progress != nil {
				cfg.Progress.Send(ProgressEvent{
					Type:     EventDeviceSearchStart,
					Provider: cfg.ProviderName,
					Device:   dev.Prefix,
					Status:   "Search failed",
					Message:  err.Error(),
				})
			}
			continue
		}

		if len(updateIDs) == 0 {
			continue
		}

		if cfg.Progress != nil {
			cfg.Progress.Send(ProgressEvent{
				Type:     EventDeviceSearchStart,
				Provider: cfg.ProviderName,
				Device:   dev.Prefix,
				Status:   fmt.Sprintf("Found %d result(s)", len(updateIDs)),
				Progress: 1.0,
			})
		}

		// Fetch details for each update ID in parallel (throttled by errgroup)
		var detailResults []*SearchResult
		var detailMu sync.Mutex
		errFile, _ := openSearchErrorLog(dev.Prefix)
		if errFile != nil {
			defer errFile.Close()
		}

		g, ctx := errgroup.WithContext(ctx)
		g.SetLimit(cfg.DetailThrottle)

		for _, id := range updateIDs {
			id := id
			g.Go(func() error {
				select {
				case <-ctx.Done():
					return nil // non-fatal cancellation
				default:
				}

				detail, err := client.GetDetail(id)
				if err != nil {
					logSearchError(errFile, dev.Prefix, id, fmt.Errorf("get detail: %w", err))
					return nil // non-fatal
				}

				if detail.Version == "" || detail.Date.IsZero() {
					return nil // non-fatal: invalid detail
				}

				// Check architecture
				if !slices.Contains(cfg.AcceptedArchs, detail.Arch) {
					return nil // non-fatal: wrong architecture
				}

				// Check NDIS exclusion
				if cfg.ExcludeNDIS && strings.Contains(strings.ToLower(detail.Title), "ndis") {
					return nil // non-fatal: NDIS excluded
				}

				result := &SearchResult{
					DevicePrefix: dev.Prefix,
					FamilyName:   dev.FamilyName,
					Queries:      dev.Queries,
					Detail:       detail,
					Arch:         detail.Arch,
				}

				detailMu.Lock()
				detailResults = append(detailResults, result)
				detailMu.Unlock()
				return nil
			})
		}

		g.Wait() // always nil since all Go() calls return nil
		allResults = append(allResults, detailResults...)
	}

	return allResults
}

// SearchDevices searches the catalog for multiple devices concurrently.
func SearchDevices(ctx context.Context, client *CatalogClient, devices []SearchDevice, cfg *SearchConfig) map[string][]*SearchResult {
	results := make(map[string][]*SearchResult)
	var mu sync.Mutex
	g, ctx := errgroup.WithContext(ctx)

	for _, dev := range devices {
		dev := dev
		g.Go(func() error {
			devResults := SearchDeviceWithContext(ctx, client, dev, cfg)
			mu.Lock()
			results[dev.Prefix] = devResults
			mu.Unlock()
			return nil
		})
	}

	g.Wait() // always nil
	return results
}
