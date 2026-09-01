package redisstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

const (
	DefaultActiveChannelIDsPageTTL = 24 * time.Hour
	activeChannelIDsPageSchemaV1   = 1
	activeChannelIDsPageMaxBytes   = 64 << 10
)

type ActiveChannelIDsPageCache struct {
	c   *redis.Client
	ttl time.Duration
}

func NewActiveChannelIDsPageCache(c *redis.Client, ttl time.Duration) *ActiveChannelIDsPageCache {
	if ttl <= 0 {
		ttl = DefaultActiveChannelIDsPageTTL
	}
	return &ActiveChannelIDsPageCache{c: c, ttl: ttl}
}

type activeChannelIDsPageEnvelope struct {
	Schema     int                           `json:"schema"`
	Key        store.ActiveChannelIDsPageKey `json:"key"`
	ChannelIDs []int64                       `json:"channel_ids"`
}

func activeChannelIDsPageRedisKey(key store.ActiveChannelIDsPageKey) string {
	return fmt.Sprintf(
		"channel:active-ids:page:v1:%d:%d:%d:%d",
		key.UserID, key.Generation, key.AfterChannelID, key.Limit,
	)
}

func (s *ActiveChannelIDsPageCache) GetActiveChannelIDsPage(
	ctx context.Context,
	key store.ActiveChannelIDsPageKey,
) ([]int64, bool, error) {
	if s == nil || s.c == nil {
		return nil, false, fmt.Errorf("active channel IDs Redis cache unavailable")
	}
	if err := validateActiveChannelIDsPageKey(key); err != nil {
		return nil, false, err
	}
	redisKey := activeChannelIDsPageRedisKey(key)
	raw, err := s.c.Get(ctx, redisKey).Bytes()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("redis get active channel IDs page: %w", err)
	}
	if len(raw) == 0 || len(raw) > activeChannelIDsPageMaxBytes {
		_ = s.c.Del(ctx, redisKey).Err()
		return nil, false, fmt.Errorf("invalid active channel IDs page size %d", len(raw))
	}
	var envelope activeChannelIDsPageEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		_ = s.c.Del(ctx, redisKey).Err()
		return nil, false, fmt.Errorf("decode active channel IDs page: %w", err)
	}
	if envelope.Schema != activeChannelIDsPageSchemaV1 || envelope.Key != key {
		_ = s.c.Del(ctx, redisKey).Err()
		return nil, false, fmt.Errorf("active channel IDs page identity/schema mismatch")
	}
	if err := validateActiveChannelIDsPage(key, envelope.ChannelIDs); err != nil {
		_ = s.c.Del(ctx, redisKey).Err()
		return nil, false, err
	}
	return append([]int64(nil), envelope.ChannelIDs...), true, nil
}

func (s *ActiveChannelIDsPageCache) PutActiveChannelIDsPage(
	ctx context.Context,
	key store.ActiveChannelIDsPageKey,
	channelIDs []int64,
) error {
	if s == nil || s.c == nil {
		return fmt.Errorf("active channel IDs Redis cache unavailable")
	}
	if err := validateActiveChannelIDsPageKey(key); err != nil {
		return err
	}
	if err := validateActiveChannelIDsPage(key, channelIDs); err != nil {
		return err
	}
	raw, err := json.Marshal(activeChannelIDsPageEnvelope{
		Schema: activeChannelIDsPageSchemaV1, Key: key, ChannelIDs: channelIDs,
	})
	if err != nil {
		return fmt.Errorf("encode active channel IDs page: %w", err)
	}
	if len(raw) > activeChannelIDsPageMaxBytes {
		return fmt.Errorf("active channel IDs page exceeds %d bytes: %d", activeChannelIDsPageMaxBytes, len(raw))
	}
	if err := s.c.Set(ctx, activeChannelIDsPageRedisKey(key), raw, s.ttl).Err(); err != nil {
		return fmt.Errorf("redis set active channel IDs page: %w", err)
	}
	return nil
}

func validateActiveChannelIDsPageKey(key store.ActiveChannelIDsPageKey) error {
	if key.UserID == 0 || key.Generation == 0 || key.AfterChannelID < 0 ||
		key.Limit <= 0 || key.Limit > domain.MaxSynchronousChannelDialogFanout {
		return fmt.Errorf("invalid active channel IDs page key")
	}
	return nil
}

func validateActiveChannelIDsPage(key store.ActiveChannelIDsPageKey, channelIDs []int64) error {
	if len(channelIDs) > key.Limit {
		return fmt.Errorf("active channel IDs page has %d rows, limit %d", len(channelIDs), key.Limit)
	}
	previous := key.AfterChannelID
	for _, channelID := range channelIDs {
		if channelID <= previous {
			return fmt.Errorf("active channel IDs page is not strictly ordered after %d", previous)
		}
		previous = channelID
	}
	return nil
}

var _ store.ActiveChannelIDsPageCache = (*ActiveChannelIDsPageCache)(nil)
