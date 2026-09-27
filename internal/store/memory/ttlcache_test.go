package memory

import (
	"testing"
	"time"
)

func TestTTLCacheGetPutDelete(t *testing.T) {
	c := newTTLCache[string, int](time.Minute)
	if _, ok := c.get("a"); ok {
		t.Fatal("get on an empty cache found a value")
	}
	c.put("a", 1, 0)
	v, ok := c.get("a")
	if !ok || v != 1 {
		t.Fatalf("get(a) = %d, %v, want 1, true", v, ok)
	}
	c.delete("a")
	if _, ok := c.get("a"); ok {
		t.Fatal("get after delete still found a value")
	}
}

func TestTTLCacheExpiresEntries(t *testing.T) {
	c := newTTLCache[string, int](time.Minute)
	c.put("a", 1, 5*time.Millisecond)
	time.Sleep(15 * time.Millisecond)
	if _, ok := c.get("a"); ok {
		t.Fatal("get returned a value past its ttl")
	}
}

func TestTTLCacheGetWithTTLFloorsAtOneSecond(t *testing.T) {
	c := newTTLCache[string, int](time.Minute)
	c.put("a", 1, 100*time.Millisecond)
	_, ok, ttl := c.getWithTTL("a")
	if !ok {
		t.Fatal("getWithTTL did not find the value")
	}
	if ttl < time.Second {
		t.Fatalf("getWithTTL remaining = %v, want floored to at least 1s", ttl)
	}
}

func TestTTLCacheSweepRemovesOnlyExpired(t *testing.T) {
	c := newTTLCache[string, int](time.Minute)
	c.put("fresh", 1, time.Hour)
	c.put("stale", 2, time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	removed := c.sweep(time.Now())
	if removed != 1 {
		t.Fatalf("sweep removed %d entries, want 1", removed)
	}
	if _, ok := c.get("fresh"); !ok {
		t.Fatal("sweep removed the fresh entry too")
	}
	if _, ok := c.get("stale"); ok {
		t.Fatal("stale entry survived sweep")
	}
}

func TestTTLCacheDefaultUsedWhenTTLNonPositive(t *testing.T) {
	c := newTTLCache[string, int](5 * time.Millisecond)
	c.put("a", 1, 0)
	time.Sleep(15 * time.Millisecond)
	if _, ok := c.get("a"); ok {
		t.Fatal("entry put with ttl<=0 did not use the cache's default ttl")
	}
}

func TestTTLCacheRemainingTTLPreservesUnexpiredEntry(t *testing.T) {
	c := newTTLCache[string, int](time.Minute)
	c.put("a", 1, time.Hour)
	remaining, ok := c.remainingTTL("a")
	if !ok {
		t.Fatal("remainingTTL did not find the value")
	}
	if remaining <= 50*time.Minute {
		t.Fatalf("remainingTTL = %v, want close to 1h", remaining)
	}
}

func TestSweeperRunsRegisteredCaches(t *testing.T) {
	c1 := newTTLCache[string, int](time.Minute)
	c2 := newTTLCache[string, int](time.Minute)
	c1.put("a", 1, time.Millisecond)
	c2.put("b", 2, time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	sweeper := NewSweeper()
	sweeper.Register(c1, c2)
	// Run directly rather than via the ticker loop -- exercise the same
	// sweep-all pass Run's ticker branch performs, without needing to wait
	// on a timer in the test.
	now := time.Now()
	for _, target := range sweeper.targets {
		target.sweep(now)
	}
	if _, ok := c1.get("a"); ok {
		t.Fatal("c1 entry survived a sweep pass through the shared Sweeper")
	}
	if _, ok := c2.get("b"); ok {
		t.Fatal("c2 entry survived a sweep pass through the shared Sweeper")
	}
}
