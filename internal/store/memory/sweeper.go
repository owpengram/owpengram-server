package memory

import (
	"context"
	"time"
)

// sweepable is any ttlCache instantiation -- Go lets a generic type's method
// satisfy a plain interface once its type parameters are fixed, so every
// cache below (whatever its key/value types) can register with one shared
// Sweeper instead of running its own goroutine.
type sweepable interface {
	sweep(now time.Time) int
}

// Sweeper periodically evicts expired entries from every in-process store
// that replaced a Redis TTL key with a plain Go map. Redis expired those keys
// on its own; a bare map does not, so without this a login attempt nobody
// ever finished, an inline query nobody ever answered, or a bot-callback
// query id nobody ever polled again would sit in memory for the life of the
// process. One goroutine sweeps all of them -- registration happens once, at
// construction (see NewInProcessStores), not scattered across call sites.
type Sweeper struct {
	targets []sweepable
}

// NewSweeper builds an empty sweeper; use Register to add caches to it.
func NewSweeper() *Sweeper {
	return &Sweeper{}
}

// Register adds a cache to the sweep rotation. Not safe to call once Run has
// started -- registration is a startup-time concern, done once while wiring
// dependencies, same as every other constructor call in this codebase.
func (s *Sweeper) Register(targets ...sweepable) {
	s.targets = append(s.targets, targets...)
}

// Run sweeps every registered cache on interval until ctx is cancelled. A
// few seconds of staleness on an already-loose expiry costs nothing, so one
// shared interval across every cache (rather than one timer per cache) is
// deliberate -- it's one goroutine instead of a dozen doing the same job.
func (s *Sweeper) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			for _, target := range s.targets {
				target.sweep(now)
			}
		}
	}
}
