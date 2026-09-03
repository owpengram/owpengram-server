// Package hoststats samples host-level CPU/RAM/disk usage for the admin
// panel's dashboard. It intentionally reports the machine's own resources,
// not the Go process's (runtime.MemStats already covers that elsewhere) --
// on a single self-hosted box the two are the same box, but the metric an
// operator wants here is "is this server about to fall over."
package hoststats

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Snapshot is the last successfully sampled host-resource reading. Ready is
// false until the first sample completes, so callers can distinguish "0% CPU"
// from "no data yet" instead of rendering a misleading zero on startup.
// DiskReady is a separate flag: disk stats are sampled from a configured
// path (see NewPoller) that can fail independently of CPU/memory sampling
// (wrong working directory, path not created yet, etc) -- without it, a
// failed disk read looked identical to "this server's disk is completely
// full" (0 free bytes) instead of "we don't have a reading right now".
type Snapshot struct {
	CPUPercent     float64
	MemUsedBytes   int64
	MemTotalBytes  int64
	DiskFreeBytes  int64
	DiskTotalBytes int64
	DiskReady      bool
	Ready          bool
}

// Poller periodically samples host stats and caches the last snapshot for
// lock-cheap reads from HTTP handlers -- the same "background worker
// refreshes, handler reads a cached value" split this codebase already uses
// for the local blob-storage free-space guard.
type Poller struct {
	diskPath string
	// diskFreeBytesFn defaults to the platform diskFreeBytes function;
	// overridable in tests to simulate a failing/succeeding disk read
	// without touching the real filesystem/OS call.
	diskFreeBytesFn func(path string) (free, total int64, err error)

	mu   sync.RWMutex
	snap Snapshot

	cpu cpuSampler
}

// NewPoller creates a poller that reports free/total disk space for the
// filesystem containing diskPath (pass the server's data/blob directory, or
// "." if it doesn't matter which volume). diskPath is resolved to an
// absolute path up front (a relative path depends on the process's current
// directory, which callers shouldn't have to reason about here) and, if it
// doesn't exist yet -- e.g. an S3-backend deployment whose local blob
// staging directory is only created on first upload -- walked up to the
// nearest existing ancestor, since GetDiskFreeSpaceEx/statfs need a real
// path and every ancestor is on the same volume anyway.
func NewPoller(diskPath string) *Poller {
	if diskPath == "" {
		diskPath = "."
	}
	if abs, err := filepath.Abs(diskPath); err == nil {
		diskPath = abs
	}
	for {
		if _, err := os.Stat(diskPath); err == nil {
			break
		}
		parent := filepath.Dir(diskPath)
		if parent == diskPath {
			break
		}
		diskPath = parent
	}
	return &Poller{diskPath: diskPath, diskFreeBytesFn: diskFreeBytes}
}

// Snapshot returns the last sample. Safe to call concurrently with Run.
func (p *Poller) Snapshot() Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.snap
}

// Run samples immediately, then on every tick of interval, until ctx is
// canceled. CPU usage is a delta between successive samples, so the first
// sample after startup reports 0% -- expected, not a bug, and Ready still
// flips true for the memory/disk figures that don't need a delta.
func (p *Poller) Run(ctx context.Context, interval time.Duration) {
	p.sampleOnce()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.sampleOnce()
		}
	}
}

func (p *Poller) sampleOnce() {
	p.mu.RLock()
	prevFree, prevTotal := p.snap.DiskFreeBytes, p.snap.DiskTotalBytes
	p.mu.RUnlock()

	var snap Snapshot
	snap.CPUPercent = p.cpu.sample()
	if used, total, err := memStats(); err == nil {
		snap.MemUsedBytes, snap.MemTotalBytes = used, total
	}
	if free, total, err := p.diskFreeBytesFn(p.diskPath); err == nil {
		snap.DiskFreeBytes, snap.DiskTotalBytes = free, total
		snap.DiskReady = true
	} else {
		// Keep the last known-good byte values stored (harmless, and a
		// reasonable fallback for any future caller that wants "last known"
		// over nothing) but DiskReady reflects THIS sample, not a stale one
		// -- a failure must show as "no current reading", not silently keep
		// claiming Ready while quietly reusing old numbers forever if the
		// underlying path became permanently unreadable.
		snap.DiskFreeBytes, snap.DiskTotalBytes = prevFree, prevTotal
		snap.DiskReady = false
	}
	snap.Ready = true

	p.mu.Lock()
	p.snap = snap
	p.mu.Unlock()
}
