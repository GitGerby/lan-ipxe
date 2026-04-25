package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gitgerby/lan-ipxe/driver-scrapers/core"
	"github.com/gitgerby/lan-ipxe/driver-scrapers/providers"
	"github.com/gitgerby/lan-ipxe/driver-scrapers/tui"
	"golang.org/x/sync/errgroup"
)

var version = "dev"

func main() {
	// Parse flags
	arch := flag.String("arch", "x64", "Target architecture (x64, arm64, all)")
	output := flag.String("output", "./drivers", "Download output directory")
	providersFlag := flag.String("providers", "all", "Comma-separated list of providers to run (intel-eth, intel-wifi, marvell, realtek, qualcomm, mediatek, broadcom)")
	noDownload := flag.Bool("no-download", false, "Search only, skip download and extraction")
	noExtract := flag.Bool("no-extract", false, "Download but skip CAB extraction")
	noTUI := flag.Bool("no-tui", false, "Plain text output instead of TUI")
	workers := flag.Int("workers", runtime.NumCPU(), "Max concurrent downloads")
	detailThrottle := flag.Int("detail-throttle", runtime.NumCPU()*4, "Max concurrent detail page fetches")
	timeout := flag.Duration("timeout", 5*time.Minute, "Request timeout per operation")
	verbose := flag.Bool("verbose", false, "Enable debug logging")
	showVersion := flag.Bool("version", false, "Show version")
	flag.Parse()

	if *showVersion {
		fmt.Println("driver-scrape", version)
		os.Exit(0)
	}

	// Validate architecture
	archVal, err := core.ValidateArch(*arch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Build accepted architectures list
	var acceptedArchs []string
	switch archVal {
	case core.ArchX64:
		acceptedArchs = []string{"AMD64"}
	case core.ArchARM64:
		acceptedArchs = []string{"ARM64"}
	case core.ArchAll:
		acceptedArchs = []string{"AMD64", "ARM64"}
	}

	// Parse providers list
	providerKeys := parseProviders(*providersFlag)

	// Convert output to absolute path so all derived paths work correctly
	if absOutput, err := filepath.Abs(*output); err == nil {
		*output = absOutput
	}

	// Create output directory
	if err := core.EnsureDir(*output); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating output directory: %v\n", err)
		os.Exit(1)
	}

	// Build provider map
	allProviders := buildProviderMap()
	selectedProviders := make([]core.DriverProvider, 0)
	for _, key := range providerKeys {
		p, ok := allProviders[key]
		if !ok {
			fmt.Fprintf(os.Stderr, "Error: unknown provider '%s'\n", key)
			fmt.Fprintf(os.Stderr, "Available providers: %s\n", strings.Join(providerKeysList(allProviders), ", "))
			os.Exit(1)
		}
		selectedProviders = append(selectedProviders, p)
	}

	// Create progress channel with large buffer to absorb download progress bursts
	progressChan := make(core.ProgressChan, 4096)

	// Create orchestrator config
	cfg := &core.OrchestratorConfig{
		AcceptedArchs:  acceptedArchs,
		OutputDir:      *output,
		DetailThrottle: *detailThrottle,
		MaxDownloads:   *workers,
		NoDownload:     *noDownload,
		NoExtract:      *noExtract,
		Timeout:        int(timeout.Seconds()),
		Verbose:        *verbose,
	}

	// Run providers in a background goroutine using errgroup
	results := make([]*core.ProviderResult, len(selectedProviders))
	g, _ := errgroup.WithContext(context.Background())

	for i, p := range selectedProviders {
		prov := p
		idx := i
		g.Go(func() error {
			orch := core.NewOrchestrator(prov, cfg, progressChan)
			results[idx] = orch.Run()
			return nil
		})
	}

	if *noTUI {
		// Plain output mode: run providers and print results
		go func() {
			g.Wait()
			close(progressChan)
		}()
		runPlainOutput(progressChan, selectedProviders, results)
	} else {
		// TUI mode: providers run in background, TUI runs on main goroutine
		// Bubble Tea MUST run on the main goroutine because it takes over stdin/stdout
		model := tui.NewModel()
		p := tea.NewProgram(model)

		// Start providers in a background goroutine
		go func() {
			g.Wait()
			close(progressChan)
		}()

		// Batch drain progress events and forward to the TUI.
		// Events are accumulated in a batch and flushed every 16ms (~60fps target)
		// to reduce per-event overhead and keep the TUI responsive.
		go func() {
			ticker := time.NewTicker(16 * time.Millisecond)
			defer ticker.Stop()

			var batch []core.ProgressEvent

			for {
				select {
				case ev, ok := <-progressChan:
					if !ok {
						// Channel closed — flush remaining events then quit
						for _, ev := range batch {
							p.Send(ev)
						}
						p.Quit()
						return
					}
					batch = append(batch, ev)

				case <-ticker.C:
					// Flush batch to Bubble Tea
					for _, ev := range batch {
						p.Send(ev)
					}
					batch = batch[:0] // reuse slice capacity
				}
			}
		}()

		// Run the TUI — this blocks until the model returns tea.Quit
		p.Run()
	}

	// Print summary
	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("Driver Scraping Complete")
	fmt.Println("========================================")

	totalSuccess, totalFailed, totalSkipped := 0, 0, 0
	for _, r := range results {
		if r == nil {
			continue
		}
		totalSuccess += r.Success
		totalFailed += r.Failed
		totalSkipped += r.Skipped
		fmt.Printf("%s: %d success, %d failed, %d skipped\n",
			r.ProviderName, r.Success, r.Failed, r.Skipped)
		if len(r.Errors) > 0 {
			for _, err := range r.Errors {
				fmt.Printf("  ERROR: %v\n", err)
			}
		}
	}

	fmt.Println()
	fmt.Printf("Total: %d success, %d failed, %d skipped\n",
		totalSuccess, totalFailed, totalSkipped)

	if totalFailed > 0 {
		os.Exit(1)
	}
}

func parseProviders(s string) []string {
	if s == "all" {
		return []string{"intel-eth", "intel-wifi", "marvell", "realtek", "qualcomm", "mediatek", "broadcom"}
	}

	var result []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func buildProviderMap() map[string]core.DriverProvider {
	m := make(map[string]core.DriverProvider)

	intelEth := providers.NewIntelEthernet()
	m[intelEth.ProviderKey()] = intelEth

	intelWifi := providers.NewIntelWiFi()
	m[intelWifi.ProviderKey()] = intelWifi

	marvell := providers.NewMarvell()
	m[marvell.ProviderKey()] = marvell

	realtek := providers.NewRealtek()
	m[realtek.ProviderKey()] = realtek

	qualcomm := providers.NewQualcomm()
	m[qualcomm.ProviderKey()] = qualcomm

	mediatek := providers.NewMediaTek()
	m[mediatek.ProviderKey()] = mediatek

	broadcom := providers.NewBCM()
	m[broadcom.ProviderKey()] = broadcom

	return m
}

func providerKeysList(m map[string]core.DriverProvider) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// ====================================================================
// Plain output
// ====================================================================

func runPlainOutput(progressChan core.ProgressChan, providers []core.DriverProvider, results []*core.ProviderResult) {
	type deviceKey struct {
		provider string
		device   string
		arch     string
	}

	deviceStatus := make(map[deviceKey]*plainDeviceState)
	providerStatus := make(map[string]*plainProviderState)
	var mu sync.Mutex

	for ev := range progressChan {
		mu.Lock()

		ps, ok := providerStatus[ev.Provider]
		if !ok {
			ps = &plainProviderState{name: ev.Provider}
			providerStatus[ev.Provider] = ps
		}

		key := deviceKey{
			provider: ev.Provider,
			device:   ev.Device,
			arch:     ev.Arch,
		}

		ds, ok := deviceStatus[key]
		if !ok && ev.Device != "" {
			ds = &plainDeviceState{prefix: ev.Device, arch: ev.Arch}
			deviceStatus[key] = ds
		}

		switch ev.Type {
		case core.EventProviderStart:
			fmt.Printf("[%s] => %s\n", time.Now().Format("15:04:05"), ev.Provider)

		case core.EventDeviceSearchStart:
			if ds != nil {
				fmt.Printf("[%s]   [SEARCH] %s %s\n", time.Now().Format("15:04:05"), ds.prefix, ev.Status)
			}

		case core.EventPackageSelected:
			if ds != nil {
				fmt.Printf("[%s]   [SELECT] %s [%s] v%s\n", time.Now().Format("15:04:05"), ds.prefix, ds.arch, ev.Version)
			}

		case core.EventDownloadStart:
			if ds != nil {
				fmt.Printf("[%s]   [DOWNLOAD] %s [%s] %s\n", time.Now().Format("15:04:05"), ds.prefix, ds.arch, ev.Message)
			}

		case core.EventDownloadProgress:
			if ds != nil {
				fmt.Printf("[%s]   [DOWNLOAD] %s [%s] %.1f%% %s\n", time.Now().Format("15:04:05"), ds.prefix, ds.arch, ev.Progress*100, ev.Message)
			}

		case core.EventDownloadDone:
			if ds != nil {
				fmt.Printf("[%s]   [DOWNLOAD] %s [%s] Complete %s\n", time.Now().Format("15:04:05"), ds.prefix, ds.arch, ev.Message)
			}

		case core.EventExtractStart:
			if ds != nil {
				fmt.Printf("[%s]   [EXTRACT] %s [%s] %s\n", time.Now().Format("15:04:05"), ds.prefix, ds.arch, ev.Message)
			}

		case core.EventExtractDone:
			if ds != nil {
				fmt.Printf("[%s]   [EXTRACT] %s [%s] Complete %s\n", time.Now().Format("15:04:05"), ds.prefix, ds.arch, ev.Message)
			}

		case core.EventProviderDone:
			ps.done = true
			fmt.Printf("[%s] => %s complete: %s\n", time.Now().Format("15:04:05"), ev.Provider, ev.Message)

		case core.EventProviderFailed:
			ps.failed = true
			fmt.Printf("[%s] => %s FAILED: %s\n", time.Now().Format("15:04:05"), ev.Provider, ev.Message)
		}

		mu.Unlock()
	}
}

type plainDeviceState struct {
	prefix string
	arch   string
}

type plainProviderState struct {
	name   string
	done   bool
	failed bool
}
