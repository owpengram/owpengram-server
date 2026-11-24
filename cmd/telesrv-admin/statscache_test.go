package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestSWRCacheServesStaleAndRefreshesInBackground(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	cache := newSWRCache("test", time.Millisecond, func(ctx context.Context) (int, error) {
		n := calls.Add(1)
		if n > 1 {
			<-release
		}
		return int(n), nil
	})

	if got, err := cache.Get(context.Background()); err != nil || got != 1 {
		t.Fatalf("first Get = %d, %v; want 1, nil", got, err)
	}
	time.Sleep(5 * time.Millisecond)

	// Stale now: the old value comes back at once while a refresh is blocked.
	done := make(chan int, 1)
	go func() {
		got, _ := cache.Get(context.Background())
		done <- got
	}()
	select {
	case got := <-done:
		if got != 1 {
			t.Fatalf("stale Get = %d, want 1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("stale Get waited for the refresh")
	}

	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got, _ := cache.Get(context.Background()); got == 2 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("background refresh never landed")
}

func TestSWRCacheKeepsValueWhenRefreshFails(t *testing.T) {
	var calls atomic.Int32
	cache := newSWRCache("test", time.Millisecond, func(ctx context.Context) (string, error) {
		if calls.Add(1) > 1 {
			return "", errors.New("database down")
		}
		return "ok", nil
	})
	if got, _ := cache.Get(context.Background()); got != "ok" {
		t.Fatalf("first Get = %q", got)
	}
	time.Sleep(5 * time.Millisecond)
	for i := 0; i < 5; i++ {
		got, err := cache.Get(context.Background())
		if err != nil || got != "ok" {
			t.Fatalf("Get after failed refresh = %q, %v; want the old value", got, err)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// A value older than maxStale (e.g. cached yesterday) must not be shown: the
// read waits for a fresh load.
func TestSWRCacheReloadsPastMaxStale(t *testing.T) {
	var calls atomic.Int32
	cache := newSWRCache("test", time.Millisecond, func(ctx context.Context) (int, error) {
		return int(calls.Add(1)), nil
	}).withMaxStale(5 * time.Millisecond)
	if got, _ := cache.Get(context.Background()); got != 1 {
		t.Fatalf("first Get = %d, want 1", got)
	}
	time.Sleep(20 * time.Millisecond)
	if got, _ := cache.Get(context.Background()); got != 2 {
		t.Fatalf("Get past maxStale = %d, want the fresh value 2", got)
	}
}

func TestSWRCacheServesOldValueWhenStaleReloadFails(t *testing.T) {
	var calls atomic.Int32
	cache := newSWRCache("test", time.Millisecond, func(ctx context.Context) (string, error) {
		if calls.Add(1) > 1 {
			return "", errors.New("database down")
		}
		return "ok", nil
	}).withMaxStale(5 * time.Millisecond)
	cache.Get(context.Background())
	time.Sleep(20 * time.Millisecond)
	if got, err := cache.Get(context.Background()); err != nil || got != "ok" {
		t.Fatalf("Get = %q, %v; want the old value", got, err)
	}
}

// Invalidate refreshes in the background; it must not make the next read wait.
func TestSWRCacheInvalidateDoesNotBlock(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	defer close(release)
	cache := newSWRCache("test", time.Minute, func(ctx context.Context) (int, error) {
		if calls.Add(1) > 1 {
			<-release
		}
		return 1, nil
	}).withMaxStale(10 * time.Minute)
	cache.Get(context.Background())
	cache.Invalidate()
	done := make(chan struct{})
	go func() { cache.Get(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Get after Invalidate waited for the reload")
	}
}

func TestSWRCacheFirstLoadErrorIsReturned(t *testing.T) {
	cache := newSWRCache("test", time.Minute, func(ctx context.Context) (int, error) {
		return 0, errors.New("boom")
	})
	if _, err := cache.Get(context.Background()); err == nil {
		t.Fatal("want the first load's error")
	}
}
