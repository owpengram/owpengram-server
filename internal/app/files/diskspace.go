package files

import (
	"io"
	"sync/atomic"

	"telesrv/internal/domain"
)

// SpaceGuard bounds how much more may be written to the permanent blob
// backend. LocalDiskSpaceGuard checks real OS free disk bytes;
// S3BudgetSpaceGuard compares a cached tracked-bytes total against a
// configured budget (S3 has no OS-level "free space" concept). Both are
// refreshed periodically by DiskUsageWorker rather than recomputed on every
// upload chunk.
type SpaceGuard interface {
	// Allow reports whether writing `additional` more bytes is currently
	// permitted. false should surface to the client as domain.ErrStorageFull.
	Allow(additional int64) (bool, error)
	// Usage returns the last-refreshed (used, total) byte snapshot for the
	// admin panel. ok is false if no successful refresh has happened yet.
	Usage() (used, total int64, ok bool)
}

// NoopSpaceGuard always allows writes; the default when the low-space guard
// is disabled by configuration.
type NoopSpaceGuard struct{}

func (NoopSpaceGuard) Allow(int64) (bool, error)  { return true, nil }
func (NoopSpaceGuard) Usage() (int64, int64, bool) { return 0, 0, false }

// requireSpace maps a SpaceGuard rejection to domain.ErrStorageFull. A nil
// guard always allows the write.
func requireSpace(guard SpaceGuard, additional int64) error {
	if guard == nil {
		return nil
	}
	ok, err := guard.Allow(additional)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrStorageFull
	}
	return nil
}

// capacityReader stops a streaming permanent write before the backend can
// publish an object larger than the current capacity snapshot permits.
type capacityReader struct {
	src   io.Reader
	guard SpaceGuard
	total int64
}

func (r *capacityReader) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	if n > 0 {
		if guardErr := requireSpace(r.guard, r.total+int64(n)); guardErr != nil {
			return 0, guardErr
		}
		r.total += int64(n)
	}
	return n, err
}

// LocalDiskSpaceGuard rejects writes once cached free disk bytes fall below
// minFreeBytes (<=0 disables the check). The free-bytes figure is
// refreshed by DiskUsageWorker, not recomputed per call, to avoid a statfs
// syscall on every upload chunk -- reads are lock-free.
type LocalDiskSpaceGuard struct {
	minFreeBytes int64
	free         atomic.Int64
	total        atomic.Int64
	ready        atomic.Bool
}

func NewLocalDiskSpaceGuard(minFreeBytes int64) *LocalDiskSpaceGuard {
	return &LocalDiskSpaceGuard{minFreeBytes: minFreeBytes}
}

func (g *LocalDiskSpaceGuard) Allow(additional int64) (bool, error) {
	// Before the first refresh completes, allow rather than reject: a
	// startup race shouldn't turn into spurious upload failures.
	if g.minFreeBytes <= 0 || !g.ready.Load() {
		return true, nil
	}
	return g.free.Load()-additional >= g.minFreeBytes, nil
}

func (g *LocalDiskSpaceGuard) Usage() (used, total int64, ok bool) {
	if !g.ready.Load() {
		return 0, 0, false
	}
	total = g.total.Load()
	used = total - g.free.Load()
	if used < 0 {
		used = 0
	}
	return used, total, true
}

func (g *LocalDiskSpaceGuard) setFree(free, total int64) {
	g.free.Store(free)
	g.total.Store(total)
	g.ready.Store(true)
}

// S3BudgetSpaceGuard rejects writes once a cached tracked-bytes total
// (refreshed periodically from file_blobs) would exceed maxTotalBytes
// (<=0 disables the check).
type S3BudgetSpaceGuard struct {
	maxTotalBytes int64
	used          atomic.Int64
	ready         atomic.Bool
}

func NewS3BudgetSpaceGuard(maxTotalBytes int64) *S3BudgetSpaceGuard {
	return &S3BudgetSpaceGuard{maxTotalBytes: maxTotalBytes}
}

func (g *S3BudgetSpaceGuard) Allow(additional int64) (bool, error) {
	if g.maxTotalBytes <= 0 || !g.ready.Load() {
		return true, nil
	}
	return g.used.Load()+additional <= g.maxTotalBytes, nil
}

func (g *S3BudgetSpaceGuard) Usage() (used, total int64, ok bool) {
	if !g.ready.Load() {
		return 0, 0, false
	}
	return g.used.Load(), g.maxTotalBytes, true
}

func (g *S3BudgetSpaceGuard) setUsed(used int64) {
	g.used.Store(used)
	g.ready.Store(true)
}
