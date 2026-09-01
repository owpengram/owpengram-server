package postgres

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"telesrv/internal/domain"
)

func TestChannelDifferenceBaseCacheSingleflightAndCloneIsolation(t *testing.T) {
	cache := NewChannelDifferenceBaseCache(16, 1<<20, time.Minute)
	key := channelDifferenceBaseKey{channelID: 7, requestPts: 10, capturedPts: 11, capturedTopID: 3, limit: 100}
	var loads atomic.Int32
	load := func() (channelDifferenceBase, error) {
		loads.Add(1)
		time.Sleep(10 * time.Millisecond)
		return channelDifferenceBase{
			lastPts:             11,
			candidatesKnown:     true,
			mentionCandidateIDs: map[int]struct{}{3: {}},
			events: []domain.ChannelUpdateEvent{{
				ChannelID:  7,
				Pts:        11,
				PtsCount:   1,
				Type:       domain.ChannelUpdateNewMessage,
				MessageIDs: []int{3},
				Message: domain.ChannelMessage{
					ChannelID: 7,
					ID:        3,
					Body:      "immutable",
					Entities:  []domain.MessageEntity{{Offset: 1}},
				},
			}},
		}, nil
	}

	const callers = 64
	values := make([]channelDifferenceBase, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := range callers {
		go func(i int) {
			defer wg.Done()
			values[i], errs[i] = cache.getOrLoad(context.Background(), key, load)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	if loads.Load() != 1 {
		t.Fatalf("loads = %d, want 1", loads.Load())
	}
	values[0].events[0].MessageIDs[0] = 99
	values[0].events[0].Message.Entities[0].Offset = 99
	delete(values[0].mentionCandidateIDs, 3)
	got, err := cache.getOrLoad(context.Background(), key, load)
	if err != nil {
		t.Fatal(err)
	}
	if got.events[0].MessageIDs[0] != 3 || got.events[0].Message.Entities[0].Offset != 1 {
		t.Fatalf("cached value was aliased: %+v", got.events[0])
	}
	if _, ok := got.mentionCandidateIDs[3]; !ok {
		t.Fatalf("cached mention candidates were aliased: %+v", got.mentionCandidateIDs)
	}
	snapshot := cache.Snapshot()
	if snapshot.Entries != 1 || snapshot.Loads != 1 || snapshot.Hits == 0 || snapshot.Weight <= 0 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestChannelDifferenceBaseCacheSeparatesCutsAndInvalidatesChannel(t *testing.T) {
	cache := NewChannelDifferenceBaseCache(16, 1<<20, time.Minute)
	var loads atomic.Int32
	load := func() (channelDifferenceBase, error) {
		loads.Add(1)
		return channelDifferenceBase{lastPts: 2}, nil
	}
	keys := []channelDifferenceBaseKey{
		{channelID: 9, requestPts: 1, capturedPts: 2, capturedTopID: 1, limit: 100},
		{channelID: 9, requestPts: 1, capturedPts: 3, capturedTopID: 2, limit: 100},
		{channelID: 10, requestPts: 1, capturedPts: 2, capturedTopID: 1, limit: 100},
	}
	for _, key := range keys {
		if _, err := cache.getOrLoad(context.Background(), key, load); err != nil {
			t.Fatal(err)
		}
	}
	if loads.Load() != 3 || cache.Snapshot().Entries != 3 {
		t.Fatalf("loads/entries = %d/%d, want 3/3", loads.Load(), cache.Snapshot().Entries)
	}
	cache.deleteChannel(9)
	if cache.Snapshot().Entries != 1 {
		t.Fatalf("entries after channel invalidation = %d, want 1", cache.Snapshot().Entries)
	}
}

func TestChannelDifferenceBaseCacheDoesNotCacheErrors(t *testing.T) {
	cache := NewChannelDifferenceBaseCache(4, 1<<20, time.Minute)
	key := channelDifferenceBaseKey{channelID: 12, requestPts: 1, capturedPts: 2, limit: 100}
	want := errors.New("load failed")
	for range 2 {
		if _, err := cache.getOrLoad(context.Background(), key, func() (channelDifferenceBase, error) {
			return channelDifferenceBase{}, want
		}); !errors.Is(err, want) {
			t.Fatalf("err = %v, want %v", err, want)
		}
	}
	snapshot := cache.Snapshot()
	if snapshot.Entries != 0 || snapshot.Loads != 2 || snapshot.LoadErrors != 2 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestChannelDifferenceUnreadFlagsSkipDatabaseWithoutMentionCandidates(t *testing.T) {
	messages := []domain.ChannelMessage{{ChannelID: 12, ID: 7}}
	base := channelDifferenceBase{candidatesKnown: true, mentionCandidateIDs: map[int]struct{}{}}
	if err := populateChannelDifferenceUnreadFlags(context.Background(), nil, 99, messages, base); err != nil {
		t.Fatal(err)
	}
	if messages[0].Mentioned || messages[0].MediaUnread {
		t.Fatalf("empty candidate gate changed message flags: %+v", messages[0])
	}
}

func TestReadModelListenerInvalidatesChannelDifferenceBase(t *testing.T) {
	cache := NewChannelDifferenceBaseCache(4, 1<<20, time.Minute)
	key := channelDifferenceBaseKey{channelID: 14, requestPts: 1, capturedPts: 2, limit: 100}
	if _, err := cache.getOrLoad(context.Background(), key, func() (channelDifferenceBase, error) {
		return channelDifferenceBase{lastPts: 2}, nil
	}); err != nil {
		t.Fatal(err)
	}
	listener := NewReadModelChangeListener("", ReadModelCacheSet{ChannelDifferences: cache}, nil)
	listener.handlePayload(`{"model":"channel_difference_base","peer_type":"channel","peer_id":14}`)
	if cache.Snapshot().Entries != 0 {
		t.Fatalf("entries after retention invalidation = %d, want 0", cache.Snapshot().Entries)
	}
}
