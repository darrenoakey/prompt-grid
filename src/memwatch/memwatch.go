package memwatch

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"time"
)

const (
	checkInterval    = 10 * time.Second
	logInterval      = 5 * time.Minute
	defaultThreshold = 2 * 1024 * 1024 * 1024 // 2GB
	exitCodeOOM      = 2
	rateSamples      = 10
)

// IntrospectFunc is called when dumping memory state.
// It should return a map of session names to scrollback line counts.
type IntrospectFunc func() map[string]int

// Watchdog monitors memory usage and crashes if it exceeds the threshold.
type Watchdog struct {
	threshold      uint64
	introspect     IntrospectFunc
	dumpDir        string
	allocRates     []uint64 // Last N TotalAlloc deltas (bytes/interval)
	lastTotalAlloc uint64
	lastLogTime    time.Time
	stopCh         chan struct{}
}

// Start launches the memory watchdog with the default 2GB threshold.
func Start(introspect IntrospectFunc) *Watchdog {
	return StartWithThreshold(defaultThreshold, introspect)
}

// StartWithThreshold launches the memory watchdog with a custom threshold.
func StartWithThreshold(threshold uint64, introspect IntrospectFunc) *Watchdog {
	homeDir, _ := os.UserHomeDir()
	dumpDir := filepath.Join(homeDir, ".config", "claude-term")

	w := &Watchdog{
		threshold:  threshold,
		introspect: introspect,
		dumpDir:    dumpDir,
		allocRates: make([]uint64, 0, rateSamples),
		stopCh:     make(chan struct{}),
	}

	go w.run()
	return w
}

// Stop stops the watchdog goroutine.
func (w *Watchdog) Stop() {
	close(w.stopCh)
}

func (w *Watchdog) run() {
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	w.lastLogTime = time.Now()

	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.check()
		}
	}
}

func (w *Watchdog) check() {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)

	// Track allocation rate
	if w.lastTotalAlloc > 0 {
		delta := stats.TotalAlloc - w.lastTotalAlloc
		w.allocRates = append(w.allocRates, delta)
		if len(w.allocRates) > rateSamples {
			w.allocRates = w.allocRates[len(w.allocRates)-rateSamples:]
		}
	}
	w.lastTotalAlloc = stats.TotalAlloc

	// Periodic stats logging
	if time.Since(w.lastLogTime) >= logInterval {
		goroutines := runtime.NumGoroutine()
		fmt.Fprintf(os.Stderr, "memwatch: HeapAlloc=%dMB HeapSys=%dMB Goroutines=%d TotalAlloc=%dMB\n",
			stats.HeapAlloc/(1024*1024),
			stats.HeapSys/(1024*1024),
			goroutines,
			stats.TotalAlloc/(1024*1024),
		)
		w.lastLogTime = time.Now()
	}

	// Threshold check
	if stats.HeapAlloc > w.threshold {
		w.dumpAndCrash(&stats)
	}
}

func (w *Watchdog) dumpAndCrash(stats *runtime.MemStats) {
	dumpPath := w.writeDump(stats)
	fmt.Fprintf(os.Stderr, "memwatch: FATAL HeapAlloc=%dMB exceeds threshold=%dMB, dump written to %s\n",
		stats.HeapAlloc/(1024*1024),
		w.threshold/(1024*1024),
		dumpPath,
	)
	os.Exit(exitCodeOOM)
}

// WriteDump writes a memory dump to the configured directory and returns the file path.
// Exported for testing.
func (w *Watchdog) WriteDump(stats *runtime.MemStats) string {
	return w.writeDump(stats)
}

func (w *Watchdog) writeDump(stats *runtime.MemStats) string {
	os.MkdirAll(w.dumpDir, 0755)
	timestamp := time.Now().Format("20060102-150405")
	dumpPath := filepath.Join(w.dumpDir, fmt.Sprintf("memdump-%s.log", timestamp))

	f, err := os.Create(dumpPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "memwatch: failed to create dump file: %v\n", err)
		return dumpPath
	}
	defer f.Close()

	// Memory stats
	fmt.Fprintf(f, "=== Memory Watchdog Dump ===\n")
	fmt.Fprintf(f, "Time: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(f, "Threshold: %d bytes (%d MB)\n\n", w.threshold, w.threshold/(1024*1024))

	fmt.Fprintf(f, "=== Runtime MemStats ===\n")
	fmt.Fprintf(f, "HeapAlloc:    %d bytes (%d MB)\n", stats.HeapAlloc, stats.HeapAlloc/(1024*1024))
	fmt.Fprintf(f, "HeapSys:      %d bytes (%d MB)\n", stats.HeapSys, stats.HeapSys/(1024*1024))
	fmt.Fprintf(f, "HeapIdle:     %d bytes (%d MB)\n", stats.HeapIdle, stats.HeapIdle/(1024*1024))
	fmt.Fprintf(f, "HeapInuse:    %d bytes (%d MB)\n", stats.HeapInuse, stats.HeapInuse/(1024*1024))
	fmt.Fprintf(f, "HeapReleased: %d bytes (%d MB)\n", stats.HeapReleased, stats.HeapReleased/(1024*1024))
	fmt.Fprintf(f, "HeapObjects:  %d\n", stats.HeapObjects)
	fmt.Fprintf(f, "TotalAlloc:   %d bytes (%d MB)\n", stats.TotalAlloc, stats.TotalAlloc/(1024*1024))
	fmt.Fprintf(f, "Sys:          %d bytes (%d MB)\n", stats.Sys, stats.Sys/(1024*1024))
	fmt.Fprintf(f, "NumGC:        %d\n", stats.NumGC)
	fmt.Fprintf(f, "GCCPUFraction: %.4f\n\n", stats.GCCPUFraction)

	// Goroutine count
	goroutines := runtime.NumGoroutine()
	fmt.Fprintf(f, "=== Goroutines: %d ===\n\n", goroutines)

	// Allocation rate history
	fmt.Fprintf(f, "=== Allocation Rate History (last %d samples, bytes/%s) ===\n", len(w.allocRates), checkInterval)
	for i, rate := range w.allocRates {
		fmt.Fprintf(f, "  [%d] %d bytes (%d MB)\n", i, rate, rate/(1024*1024))
	}
	fmt.Fprintln(f)

	// Session introspection
	if w.introspect != nil {
		sessions := w.introspect()
		fmt.Fprintf(f, "=== Session Scrollback Lines ===\n")
		for name, lines := range sessions {
			fmt.Fprintf(f, "  %s: %d lines\n", name, lines)
		}
		fmt.Fprintln(f)
	}

	// Full goroutine stack dump
	fmt.Fprintf(f, "=== Goroutine Stack Dump ===\n")
	buf := make([]byte, 64*1024*1024) // 64MB buffer for goroutine stacks
	n := runtime.Stack(buf, true)
	f.Write(buf[:n])
	fmt.Fprintln(f)

	// Heap profile
	fmt.Fprintf(f, "\n=== Heap Profile ===\n")
	pprof.Lookup("heap").WriteTo(f, 1)

	return dumpPath
}
