package memwatch

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMemStatsReading(t *testing.T) {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)

	if stats.HeapAlloc == 0 {
		t.Error("HeapAlloc should be non-zero")
	}
	if stats.TotalAlloc == 0 {
		t.Error("TotalAlloc should be non-zero")
	}
}

func TestWriteDump(t *testing.T) {
	tmpDir := t.TempDir()

	w := &Watchdog{
		threshold:  1024,
		introspect: func() map[string]int {
			return map[string]int{"TestSession": 42}
		},
		dumpDir:    tmpDir,
		allocRates: []uint64{100, 200, 300},
	}

	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)

	dumpPath := w.WriteDump(&stats)

	// Verify file was created
	if _, err := os.Stat(dumpPath); os.IsNotExist(err) {
		t.Fatalf("dump file not created at %s", dumpPath)
	}

	// Verify contents
	content, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatalf("failed to read dump: %v", err)
	}

	text := string(content)

	checks := []string{
		"Memory Watchdog Dump",
		"Runtime MemStats",
		"HeapAlloc:",
		"Goroutine Stack Dump",
		"Allocation Rate History",
		"Session Scrollback Lines",
		"TestSession: 42",
		"Heap Profile",
	}
	for _, check := range checks {
		if !strings.Contains(text, check) {
			t.Errorf("dump missing expected content: %q", check)
		}
	}
}

func TestWriteDumpInSubdir(t *testing.T) {
	tmpDir := filepath.Join(t.TempDir(), "nested", "dir")

	w := &Watchdog{
		threshold:  1024,
		dumpDir:    tmpDir,
		allocRates: []uint64{},
	}

	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)

	dumpPath := w.WriteDump(&stats)

	if _, err := os.Stat(dumpPath); os.IsNotExist(err) {
		t.Fatalf("dump file not created at %s (nested dir should be auto-created)", dumpPath)
	}
}

func TestAllocationRateTracking(t *testing.T) {
	w := &Watchdog{
		threshold:      ^uint64(0), // Max uint64 - never trigger
		dumpDir:        t.TempDir(),
		allocRates:     make([]uint64, 0, rateSamples),
		lastTotalAlloc: 0,
		lastLogTime:    time.Now(),
		stopCh:         make(chan struct{}),
	}

	// Simulate first check (establishes baseline)
	w.check()

	if w.lastTotalAlloc == 0 {
		t.Error("lastTotalAlloc should be set after first check")
	}

	// Allocate some memory to create a rate
	_ = make([]byte, 1024*1024)

	// Second check should record a rate
	w.check()

	if len(w.allocRates) == 0 {
		t.Error("allocRates should have at least one entry after second check")
	}

	// Verify rate cap at rateSamples
	for i := 0; i < rateSamples+5; i++ {
		w.check()
	}

	if len(w.allocRates) > rateSamples {
		t.Errorf("allocRates should be capped at %d, got %d", rateSamples, len(w.allocRates))
	}
}

func TestStartStop(t *testing.T) {
	w := StartWithThreshold(^uint64(0), nil)
	w.Stop()
	// Should not hang or panic
}
