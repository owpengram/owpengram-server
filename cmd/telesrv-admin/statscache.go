package main

import (
	"context"
	"log"
	"sync"
	"time"
)

// The overview and Storage pages open on aggregate queries that scan every
// stored file (StorageStats, the per-account usage list). On a real server
// those take seconds, and the pages used to sit on skeletons for all of it.
//
// swrCache answers from the last result and refreshes it in the background
// once it is older than `fresh` (stale-while-revalidate). Only the very first
// call, before anything was ever loaded, waits for the query -- and main warms
// the caches at start-up, so normally not even that one does.
type swrCache[T any] struct {
	name  string
	fresh time.Duration
	load  func(context.Context) (T, error)

	mu         sync.Mutex
	value      T
	loaded     bool
	at         time.Time
	refreshing bool
}

func newSWRCache[T any](name string, fresh time.Duration, load func(context.Context) (T, error)) *swrCache[T] {
	return &swrCache[T]{name: name, fresh: fresh, load: load}
}

// refreshTimeout bounds a background refresh; it runs detached from the
// request that triggered it, so it needs a deadline of its own.
const refreshTimeout = 2 * time.Minute

func (c *swrCache[T]) Get(ctx context.Context) (T, error) {
	c.mu.Lock()
	if c.loaded {
		value := c.value
		if time.Since(c.at) > c.fresh && !c.refreshing {
			c.refreshing = true
			go c.refresh()
		}
		c.mu.Unlock()
		return value, nil
	}
	c.mu.Unlock()

	// Nothing cached yet: this caller waits, and its result seeds the cache.
	value, err := c.load(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	c.store(value)
	return value, nil
}

func (c *swrCache[T]) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
	defer cancel()
	value, err := c.load(ctx)
	c.mu.Lock()
	c.refreshing = false
	c.mu.Unlock()
	if err != nil {
		// The previous value keeps being served; the next read retries.
		log.Printf("admin: refreshing %s failed: %v", c.name, err)
		return
	}
	c.store(value)
}

func (c *swrCache[T]) store(value T) {
	c.mu.Lock()
	c.value = value
	c.loaded = true
	c.at = time.Now()
	c.mu.Unlock()
}

// Invalidate makes the next read refresh in the background (still answering
// from the old value), e.g. right after an action that changed the numbers.
func (c *swrCache[T]) Invalidate() {
	c.mu.Lock()
	c.at = time.Time{}
	c.mu.Unlock()
}

// warmAttempts/warmBackoff: the panel can start before the database it reads
// (owpengram-ctl brings the embedded PostgreSQL up alongside it), so warming
// retries, waiting a little longer each time, for about two minutes in all.
const (
	warmAttempts = 12
	warmBackoff  = 2 * time.Second
)

// Warm loads the value ahead of the first page view, retrying while the
// database is not up yet. A page view that comes first simply loads it itself.
func (c *swrCache[T]) Warm(ctx context.Context) {
	var err error
	for attempt := 1; attempt <= warmAttempts; attempt++ {
		var value T
		if value, err = c.load(ctx); err == nil {
			c.store(value)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(attempt) * warmBackoff):
		}
	}
	log.Printf("admin: warming %s failed: %v", c.name, err)
}

// storageUsagePage is one cached page of the per-account storage list.
type storageUsagePage struct {
	rows    []AccountStorageRow
	hasMore bool
}

// statsCaches holds the cached aggregates. Only the unfiltered first page of
// the per-account list is cached, per sort order: that is what the Storage
// page opens on; searches and later pages always query live.
type statsCaches struct {
	read      *readStore
	storage   *swrCache[StorageStatsRow]
	dashboard *swrCache[DashboardCounts]
	activity  *swrCache[DashboardActivity]

	mu        sync.Mutex
	usageList map[string]*swrCache[storageUsagePage]
}

const (
	storageStatsFresh   = 60 * time.Second
	dashboardCountFresh = 15 * time.Second
	storageListFresh    = 60 * time.Second
	storageListPageSize = 50
	// The activity chart is per day; a few minutes behind is invisible on it.
	activityFresh = 5 * time.Minute
)

func newStatsCaches(read *readStore) *statsCaches {
	if read == nil {
		return nil
	}
	return &statsCaches{
		read:      read,
		storage:   newSWRCache("storage stats", storageStatsFresh, read.StorageStats),
		dashboard: newSWRCache("dashboard counts", dashboardCountFresh, read.DashboardCounts),
		activity:  newSWRCache("dashboard activity", activityFresh, read.DashboardActivity),
		usageList: map[string]*swrCache[storageUsagePage]{},
	}
}

func (c *statsCaches) storageList(sortBy string, sortDesc bool) *swrCache[storageUsagePage] {
	key := sortBy
	if sortDesc {
		key += ":desc"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if cache, ok := c.usageList[key]; ok {
		return cache
	}
	cache := newSWRCache("storage list "+key, storageListFresh, func(ctx context.Context) (storageUsagePage, error) {
		rows, hasMore, err := c.read.ListAccountStorageUsage(ctx, "", sortBy, sortDesc, 0, storageListPageSize)
		return storageUsagePage{rows: rows, hasMore: hasMore}, err
	})
	c.usageList[key] = cache
	return cache
}

// invalidateStorage is called after anything that changes stored bytes.
func (c *statsCaches) invalidateStorage() {
	if c == nil {
		return
	}
	c.storage.Invalidate()
	c.mu.Lock()
	for _, cache := range c.usageList {
		cache.Invalidate()
	}
	c.mu.Unlock()
}

// warm fills the caches the overview and Storage pages open on.
func (c *statsCaches) warm(ctx context.Context) {
	if c == nil {
		return
	}
	var wg sync.WaitGroup
	for _, fn := range []func(context.Context){
		c.dashboard.Warm,
		c.activity.Warm,
		c.storage.Warm,
		c.storageList("bytes", true).Warm,
	} {
		wg.Add(1)
		go func(warm func(context.Context)) {
			defer wg.Done()
			warm(ctx)
		}(fn)
	}
	wg.Wait()
}
