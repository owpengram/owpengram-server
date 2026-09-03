package hoststats

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSampleOncePreservesLastGoodDiskReadingOnFailure guards the bug
// reported live: a failed disk-space sample used to silently reset
// DiskFreeBytes/DiskTotalBytes to 0 while still marking the overall
// snapshot Ready -- rendering as "0 bytes free" (indistinguishable from an
// actually-full disk) instead of "no reading right now". A failed sample
// must keep the last known-good reading and report DiskReady=false.
func TestSampleOncePreservesLastGoodDiskReadingOnFailure(t *testing.T) {
	p := &Poller{diskFreeBytesFn: func(string) (int64, int64, error) {
		return 1234, 5678, nil
	}}
	p.sampleOnce()
	first := p.Snapshot()
	if !first.DiskReady || first.DiskFreeBytes != 1234 || first.DiskTotalBytes != 5678 {
		t.Fatalf("first snapshot = %+v, want a successful disk reading", first)
	}

	p.diskFreeBytesFn = func(string) (int64, int64, error) {
		return 0, 0, errors.New("disk stat failed")
	}
	p.sampleOnce()
	second := p.Snapshot()
	if second.DiskReady {
		t.Fatal("DiskReady = true after a failed sample, want false")
	}
	if second.DiskFreeBytes != 1234 || second.DiskTotalBytes != 5678 {
		t.Fatalf("disk fields after failed sample = (%d, %d), want the preserved (1234, 5678)", second.DiskFreeBytes, second.DiskTotalBytes)
	}
	// CPU/memory sampling must still complete and mark Ready, independent
	// of the disk failure.
	if !second.Ready {
		t.Fatal("Ready = false after a disk-only failure, want true (CPU/mem still sampled)")
	}
}

// TestSampleOnceNeverReadyWithoutAnyPriorSuccess confirms a disk read that
// has NEVER succeeded (not just failed after a prior success) still reports
// DiskReady=false with zero-value fields, not a fabricated 0-bytes-free
// reading.
func TestSampleOnceNeverReadyWithoutAnyPriorSuccess(t *testing.T) {
	p := &Poller{diskFreeBytesFn: func(string) (int64, int64, error) {
		return 0, 0, errors.New("never worked")
	}}
	p.sampleOnce()
	snap := p.Snapshot()
	if snap.DiskReady {
		t.Fatal("DiskReady = true with no successful sample ever, want false")
	}
	if !snap.Ready {
		t.Fatal("Ready = false, want true (CPU/mem sampling is independent of disk)")
	}
}

// TestNewPollerWalksUpToNearestExistingAncestor guards the other half of the
// fix: a configured disk path that doesn't exist yet (e.g. an S3-backend
// deployment's local blob staging directory, only created on first upload)
// must not permanently break disk stats -- NewPoller walks up to the
// nearest existing ancestor instead of handing GetDiskFreeSpaceEx/statfs a
// path they will always fail to stat.
func TestNewPollerWalksUpToNearestExistingAncestor(t *testing.T) {
	tmp := t.TempDir()
	missing := filepath.Join(tmp, "not-created-yet", "nested", "deeper")

	p := NewPoller(missing)

	if p.diskPath != tmp {
		t.Fatalf("resolved disk path = %q, want the nearest existing ancestor %q", p.diskPath, tmp)
	}
	if _, err := os.Stat(p.diskPath); err != nil {
		t.Fatalf("resolved disk path %q does not exist: %v", p.diskPath, err)
	}
}

// TestNewPollerResolvesRelativePathToAbsolute confirms a relative diskPath
// (as configured today, e.g. "data/blobs") no longer silently depends on
// whatever the process's current directory happens to be at NewPoller time.
func TestNewPollerResolvesRelativePathToAbsolute(t *testing.T) {
	p := NewPoller(".")
	if !filepath.IsAbs(p.diskPath) {
		t.Fatalf("resolved disk path %q is not absolute", p.diskPath)
	}
}

// TestRunSamplesImmediatelyOnStart confirms Run's documented behavior
// (sampleOnce before entering the ticker loop) without relying on any
// ticker firing or a busy-wait: cancel the context right away and check the
// synchronous first sample already landed by the time Run returns.
func TestRunSamplesImmediatelyOnStart(t *testing.T) {
	p := &Poller{diskFreeBytesFn: func(string) (int64, int64, error) { return 1, 1, nil }}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.Run(ctx, time.Hour)
	if !p.Snapshot().Ready {
		t.Fatal("Run did not produce a ready snapshot from its initial sample")
	}
}
