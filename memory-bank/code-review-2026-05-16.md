# Code Review Report - driver-scrapers (2026-05-16)

## Executive Summary

The driver-scrapers project has architectural merit but contains several critical bugs, correctness issues, and design flaws that need attention. The code was written by a less capable model and shows patterns of incomplete implementation, incorrect internal state management, and missed opportunities for better concurrency design.

---

## 1. CRITICAL BUGS

### 1.1 ExcludeNDIS is completely broken (types.go line 251-256)

**Severity: CRITICAL** - Qualcomm and MediaTek devices will receive wrong packages

The `SearchConfig` struct has an `ExcludeNDIS` field, but in `Orchestrator.Run()`, the search config is created without setting it:
```go
searchCfg := &SearchConfig{
    ProviderName:   o.provider.Name(),
    AcceptedArchs:  o.cfg.AcceptedArchs,
    DetailThrottle: o.cfg.DetailThrottle,
    Progress:       o.progress,
    // ExcludeNDIS is NOT set - defaults to false
}
```

Furthermore, `SearchDeviceWithContext` in `search.go` line 184 checks `cfg.ExcludeNDIS` (the provider-level config), not `dev.ExcludeNDIS` (the per-device setting). The per-device `ExcludeNDIS` field on `SearchDevice` is **never read**.

**Impact:** All Qualcomm WiFi and MediaTek WiFi devices have `exclude_ndis: true` in the YAML config, but NDIS packages are NOT excluded during search. This means the wrong driver packages can be selected.

**Fix:** Pass `dev.ExcludeNDIS` into the search function, or restructure so `SearchDeviceWithContext` receives the per-device value.

### 1.2 TUI phase rendering is incomplete (tui/model.go)

**Severity: HIGH** - TUI shows incorrect status for common states

The `handleProgress` method sets device phase to values like "found", "downloaded", "downloading", "extracting". However, the `renderStatus` function doesn't have cases for "found" and "downloaded" phases - they fall through to the default case which shows "·" (waiting symbol). This means:
- After search completes and a package is selected, the status shows "·" instead of the selected version
- After download completes but before extraction starts, status shows "·"

**Fix:** Add proper rendering cases for all phase values.

### 1.3 TUI counters can underflow (tui/model.go)

**Severity: MEDIUM** - Negative counters displayed in TUI

The `downloading` and `extracting` counters in `providerState` are plain `int` values. If `EventDownloadStart` is dropped (due to channel being full, since `Send()` uses non-blocking select with default drop) but `EventDownloadDone` arrives, the counter decrements below zero. Same for extracting.

**Fix:** Use `max(0, m.providers[p].downloading--)` pattern, or track per-device state instead of global counters.

---

## 2. DESIGN FLAWS

### 2.1 Head-of-line blocking in download phase (types.go Run())

**Severity: MEDIUM** - Unnecessary delay in overall completion time

The current flow in `Orchestrator.Run()`:
1. Launch all device search+select goroutines
2. **Wait for ALL to complete** (blocking on packageChan)
3. Then start downloads

This means if Device A finishes search in 5 seconds but Device Z takes 45 seconds, Device A's download doesn't start until all 45 seconds elapse. The comment at line 258-264 acknowledges the concern about HOL blocking for search, but then reintroduces it for the download phase.

**Fix:** Use a streaming pipeline where each device's search+select goroutine sends the result to a consumer that immediately starts downloading, without waiting for other devices.

### 2.2 EventInit is dead code

**Severity: LOW** - Dead code confusion

The `EventInit` event type exists (line 18 in types.go), and the TUI's `handleInit` method handles it. However, `EventInit` is **never sent** anywhere in the codebase. The TUI is pre-populated with `providerInfos` in `NewModel()` in main.go. The `handleInit` function and the `Providers` field in `ProgressEvent` are unreachable code.

**Fix:** Either remove `EventInit` and the `handleInit` method, or send an `EventInit` event from main.go after building provider info.

### 2.3 TUI device state keyed only by prefix, not prefix+arch (tui/model.go)

**Severity: MEDIUM** - Incorrect state tracking for --arch all

The `findDevice` method in the TUI model searches by provider name and device prefix only. When `--arch all` is used, both AMD64 and ARM64 results for the same device prefix update the same `deviceState`. The last event to arrive wins, meaning one architecture's state overwrites the other's.

Looking at the model more carefully, `deviceState` has an `arch` field, but `findDevice` doesn't match on it. Two progress events for the same device prefix but different architectures will collide on the same state object.

**Fix:** Key device states by a composite key of prefix+arch, or maintain separate device states per architecture.

### 2.4 Global mutable state in search.go (errorLogFiles map)

**Severity: LOW** - Resource leak

The `errorLogFiles` map at package level in search.go holds open file handles that are never closed. The `openSearchErrorLog` function opens files with `O_APPEND` but the corresponding close operation in `SearchDeviceWithContext` (line 153: `defer errFile.Close()`) only closes the file for the current call. If the same device is searched multiple times (e.g., re-running with different configs), the map entry persists with a now-closed file handle.

**Fix:** Close files when done, or use a different logging approach (e.g., write to a single log file, or use structured logging).

---

## 3. PERFORMANCE ISSUES

### 3.1 parseVersion regex compiled on every call (selection.go)

**Severity: MEDIUM** - Unnecessary CPU overhead

The `parseVersion` function uses `regexp.MustCompile` on every call. This function is called during package selection, which happens for every device. `MustCompile` does work on every call even though "Must" suggests it's a one-time operation.

**Fix:** Pre-compile the regex as a package-level `var`.

### 3.2 Progress channel size vs event rate

**Severity: LOW** - Event loss under load

The progress channel has a buffer of 4096 (main.go line 97). During active download with multiple concurrent downloads, each sending progress events at 16ms intervals via the batched sender, plus search events, this buffer can fill up. When full, `Send()` drops events (non-blocking select with default). While this prevents blocking, it means the TUI can miss state transitions.

**Current mitigation:** The batched sender (16ms ticker) helps reduce event rate. The 4096 buffer is reasonably large. This is acceptable but worth monitoring.

---

## 4. CORRECTNESS ISSUES

### 4.1 CatalogClient uses default timeout, ignoring config (types.go line 215)

**Severity: LOW** - Config ignored

`NewOrchestrator` calls `NewCatalogClient(0)`, and the comment says "0 means use default timeout from config". However, `NewCatalogClient` sets a hardcoded 5-minute timeout when timeout is 0. The `OrchestratorConfig.Timeout` field is never passed to the catalog client.

**Fix:** Pass `cfg.Timeout` to `NewCatalogClient`.

### 4.2 detectArch is overly simplistic (catalog.go line 243-253)

**Severity: MEDIUM** - Can misidentify architecture

The `detectArch` function checks for "ARM64" first, then "AMD64"/"x64", defaulting to "x86". This string-based detection on HTML content is fragile. A page could mention "ARM64" in a footnote or compatibility note while the actual package is for AMD64.

**Fix:** Parse architecture from more specific HTML elements or URLs.

### 4.3 extractCab lists files but doesn't verify meaningful content (extractor.go)

**Severity: MEDIUM** - Silent data loss (partially mitigated)

The `extractCAB` function at line 77 does call `listFiles(extractDir)` after extraction, and the file count is reported in `EventExtractDone`. However, it doesn't check if the file list is empty (would fail on empty dir since Walk returns no files but no error), and it doesn't verify that driver files (.inf/.sys/.cat) exist. A CAB containing only readme files or metadata would pass validation.

**Fix:** Add check `if len(files) == 0 { return nil, fmt.Errorf("no files extracted") }` and optionally verify .inf file exists.

### 4.4 downloadPackage accesses unexported fields (downloader.go line 47, 50)

**Severity: LOW** - Tight coupling

The `downloadPackage` function accesses `o.client.userAgent` and `o.client.client` - both unexported fields of `CatalogClient`. This creates tight coupling between what should be independent components.

**Fix:** Add methods to `CatalogClient` for download operations, or export the fields.

---

## 5. TUI-SPECIFIC ISSUES

### 5.1 TUI doesn't handle concurrent provider events correctly

**Severity: MEDIUM** - Race condition in display

Multiple providers run concurrently, all sending events to the same channel. The TUI's `handleProgress` method updates provider state without considering that events from different providers can interleave. While the model itself is accessed from a single goroutine (Bubble Tea's model.Update is single-threaded), the logical state can be inconsistent - e.g., a provider's "done" event arriving before all its device events.

**Mitigation:** This is partially mitigated by Bubble Tea's single-threaded event processing. However, the TUI could show a provider as "done" while still processing device events from that provider.

### 5.2 Spinner is thread-safe but unnecessary

**Severity: LOW** - Overengineering

The `spinner` in `spinner.go` uses `sync.Mutex` for thread safety, but it's only accessed from Bubble Tea's single-threaded update loop. The mutex is unnecessary overhead.

---

## 6. MISSING FUNCTIONALITY

### 6.1 No retry logic

Failed downloads or searches have no retry mechanism. A transient network error permanently fails a device.

### 6.2 No caching

Catalog search results are not cached. Re-running the tool repeats all API calls.

### 6.3 No dry-run mode

There's no way to preview what would be downloaded without actually downloading.

---

## 7. POSITIVE OBSERVATIONS

1. **Good separation of concerns:** Core logic, providers, and TUI are well-separated
2. **YAML config for providers:** Clean and extensible approach
3. **Progress channel pattern:** Good choice for decoupling workers from display
4. **Batched TUI updates:** The 16ms batching reduces event overhead
5. **Error group usage:** Proper use of errgroup for throttled concurrency
6. **Context support:** Cancellation is properly propagated

---

## 8. RECOMMENDED FIXES (PRIORITY ORDER)

1. **FIX IMMEDIATELY:** ExcludeNDIS bug (#1.1) - causes wrong driver selection
2. **FIX IMMEDIATELY:** TUI phase rendering (#1.2) - incorrect user feedback
3. **FIX SOON:** TUI counter underflow (#1.3) - can show negative numbers
4. **FIX SOON:** parseVersion regex pre-compilation (#3.1) - performance
5. **CONSIDER:** Streaming download pipeline (#2.1) - reduces total time
6. **CLEANUP:** Remove EventInit dead code (#2.2)
7. **CLEANUP:** Fix CatalogClient timeout config (#4.1)
8. **ENHANCE:** Add retry logic (#6.1)
9. **ENHANCE:** Add extraction verification (#4.3)