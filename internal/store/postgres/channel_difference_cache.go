package postgres

import (
	"context"
	"strconv"
	"sync/atomic"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/readmodelcache"
)

type channelDifferenceBaseKey struct {
	channelID     int64
	requestPts    int
	capturedPts   int
	capturedTopID int
	limit         int
}

// channelDifferenceBase contains only viewer-independent durable facts. Access,
// available-min, monoforum visibility, unread flags and dialog state are applied
// after the cache lookup by ListChannelDifference.
type channelDifferenceBase struct {
	retainedThroughPts int
	lastPts            int
	tooLong            bool
	events             []domain.ChannelUpdateEvent
	messages           []domain.ChannelMessage
	// mentionCandidateIDs is a viewer-independent sparse gate sourced from
	// channel_unread_mention_index. When candidatesKnown is true, messages not
	// present in this set cannot have a viewer mention overlay and must not cause
	// a channel_unread_mentions query.
	mentionCandidateIDs map[int]struct{}
	candidatesKnown     bool
}

type ChannelDifferenceCacheSnapshot struct {
	Entries    int
	Weight     int64
	Hits       uint64
	Misses     uint64
	Loads      uint64
	LoadErrors uint64
}

// ChannelDifferenceBaseCache deduplicates immutable channel event/message pages
// shared by many viewers catching up from the same cursor. It never stores a
// permission decision or a final ChannelDifference response.
type ChannelDifferenceBaseCache struct {
	cache *readmodelcache.Cache[channelDifferenceBaseKey, channelDifferenceBase]

	hits       atomic.Uint64
	misses     atomic.Uint64
	loads      atomic.Uint64
	loadErrors atomic.Uint64
}

func NewChannelDifferenceBaseCache(maxEntries int, maxWeight int64, ttl time.Duration) *ChannelDifferenceBaseCache {
	cache := readmodelcache.New[channelDifferenceBaseKey, channelDifferenceBase](readmodelcache.Config[channelDifferenceBaseKey, channelDifferenceBase]{
		MaxEntries: maxEntries,
		MaxWeight:  maxWeight,
		TTL:        ttl,
		Clone:      cloneChannelDifferenceBase,
		Weight:     channelDifferenceBaseWeight,
		KeyString: func(key channelDifferenceBaseKey) string {
			return strconv.FormatInt(key.channelID, 10) + ":" +
				strconv.Itoa(key.requestPts) + ":" + strconv.Itoa(key.capturedPts) + ":" +
				strconv.Itoa(key.capturedTopID) + ":" + strconv.Itoa(key.limit)
		},
	})
	if cache == nil {
		return nil
	}
	return &ChannelDifferenceBaseCache{cache: cache}
}

func (c *ChannelDifferenceBaseCache) getOrLoad(
	ctx context.Context,
	key channelDifferenceBaseKey,
	load func() (channelDifferenceBase, error),
) (channelDifferenceBase, error) {
	if c == nil {
		return load()
	}
	if _, ok := c.cache.Peek(key); ok {
		c.hits.Add(1)
	} else {
		c.misses.Add(1)
	}
	return c.cache.GetOrLoad(ctx, key, func() (channelDifferenceBase, error) {
		c.loads.Add(1)
		value, err := load()
		if err != nil {
			c.loadErrors.Add(1)
		}
		return value, err
	})
}

func (c *ChannelDifferenceBaseCache) deleteChannel(channelID int64) {
	if c == nil || channelID == 0 {
		return
	}
	c.cache.InvalidateWhere(func(key channelDifferenceBaseKey) bool { return key.channelID == channelID })
}

func (c *ChannelDifferenceBaseCache) flush() {
	if c == nil {
		return
	}
	c.cache.Flush()
}

func (c *ChannelDifferenceBaseCache) Snapshot() ChannelDifferenceCacheSnapshot {
	if c == nil {
		return ChannelDifferenceCacheSnapshot{}
	}
	return ChannelDifferenceCacheSnapshot{
		Entries:    c.cache.Len(),
		Weight:     c.cache.Weight(),
		Hits:       c.hits.Load(),
		Misses:     c.misses.Load(),
		Loads:      c.loads.Load(),
		LoadErrors: c.loadErrors.Load(),
	}
}

func cloneChannelDifferenceBase(base channelDifferenceBase) channelDifferenceBase {
	candidates := base.mentionCandidateIDs
	base.events = append([]domain.ChannelUpdateEvent(nil), base.events...)
	for i := range base.events {
		base.events[i].MessageIDs = append([]int(nil), base.events[i].MessageIDs...)
		base.events[i].UserIDs = append([]int64(nil), base.events[i].UserIDs...)
		base.events[i].Message = cloneChannelTopMessage(base.events[i].Message)
	}
	base.messages = append([]domain.ChannelMessage(nil), base.messages...)
	for i := range base.messages {
		base.messages[i] = cloneChannelTopMessage(base.messages[i])
	}
	if base.mentionCandidateIDs != nil {
		base.mentionCandidateIDs = make(map[int]struct{}, len(base.mentionCandidateIDs))
		for id := range candidates {
			base.mentionCandidateIDs[id] = struct{}{}
		}
	}
	return base
}

func channelDifferenceBaseWeight(base channelDifferenceBase) int64 {
	weight := int64(96 + len(base.events)*192 + len(base.messages)*192 + len(base.mentionCandidateIDs)*16)
	for _, event := range base.events {
		weight += int64(len(event.MessageIDs)*8 + len(event.UserIDs)*8)
		weight += channelDifferenceMessageWeight(event.Message)
	}
	for _, message := range base.messages {
		weight += channelDifferenceMessageWeight(message)
	}
	return weight
}

func channelDifferenceMessageWeight(message domain.ChannelMessage) int64 {
	if message.ID == 0 {
		return 0
	}
	weight := int64(len(message.Body) + len(message.PostAuthor) + len(message.Entities)*48)
	if message.RichMessage != nil {
		weight += int64(len(message.RichMessage.Blocks) + len(message.RichMessage.BotAPIProjection))
	}
	if message.Action != nil {
		weight += int64(len(message.Action.Title) + len(message.Action.UserIDs)*8 + len(message.Action.TodoItems)*64)
	}
	return weight
}
