package redisstore

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/redis/go-redis/v9"

	"telesrv/internal/store"
)

const (
	DefaultDialogListSnapshotTTL      = time.Hour
	dialogListSnapshotSchemaV7        = 7
	dialogListSnapshotMaxEncodedBytes = 8 << 20
	dialogListSnapshotMaxDecodedBytes = 8 << 20
)

var (
	dialogListSnapshotCodecOnce sync.Once
	dialogListSnapshotEncoder   *zstd.Encoder
	dialogListSnapshotDecoder   *zstd.Decoder
	dialogListSnapshotCodecErr  error
)

// DialogListSnapshotCache stores version-addressed materialized owner snapshots.
// PostgreSQL read-model hashes remain the authority; this cache never decides
// whether an entry is current.
type DialogListSnapshotCache struct {
	c   *redis.Client
	ttl time.Duration
}

func NewDialogListSnapshotCache(c *redis.Client, ttl time.Duration) *DialogListSnapshotCache {
	if ttl <= 0 {
		ttl = DefaultDialogListSnapshotTTL
	}
	return &DialogListSnapshotCache{c: c, ttl: ttl}
}

type dialogListSnapshotEnvelope struct {
	Schema int                                `json:"schema"`
	Key    store.DialogListSnapshotCacheKey   `json:"key"`
	Value  store.DialogListSnapshotCacheValue `json:"value"`
}

func dialogListSnapshotKey(key store.DialogListSnapshotCacheKey) string {
	return fmt.Sprintf(
		"dialog:list:snapshot:v7:%d:%d",
		key.UserID, key.OwnerHash,
	)
}

func (s *DialogListSnapshotCache) GetDialogListSnapshot(
	ctx context.Context,
	key store.DialogListSnapshotCacheKey,
) (store.DialogListSnapshotCacheValue, bool, error) {
	if s == nil || s.c == nil {
		return store.DialogListSnapshotCacheValue{}, false, fmt.Errorf("dialog list snapshot Redis cache unavailable")
	}
	if err := validateDialogListSnapshotKey(key); err != nil {
		return store.DialogListSnapshotCacheValue{}, false, err
	}
	redisKey := dialogListSnapshotKey(key)
	raw, err := s.c.Get(ctx, redisKey).Bytes()
	if err == redis.Nil {
		return store.DialogListSnapshotCacheValue{}, false, nil
	}
	if err != nil {
		return store.DialogListSnapshotCacheValue{}, false, fmt.Errorf("redis get dialog list snapshot: %w", err)
	}
	if len(raw) == 0 || len(raw) > dialogListSnapshotMaxEncodedBytes {
		_ = s.c.Del(ctx, redisKey).Err()
		return store.DialogListSnapshotCacheValue{}, false, fmt.Errorf("invalid dialog list snapshot size %d", len(raw))
	}
	_, decoder, err := dialogListSnapshotCodecs()
	if err != nil {
		return store.DialogListSnapshotCacheValue{}, false, err
	}
	decoded, err := decoder.DecodeAll(raw, nil)
	if err != nil {
		_ = s.c.Del(ctx, redisKey).Err()
		return store.DialogListSnapshotCacheValue{}, false, fmt.Errorf("decompress dialog list snapshot: %w", err)
	}
	if len(decoded) == 0 || len(decoded) > dialogListSnapshotMaxDecodedBytes {
		_ = s.c.Del(ctx, redisKey).Err()
		return store.DialogListSnapshotCacheValue{}, false, fmt.Errorf("invalid decoded dialog list snapshot size %d", len(decoded))
	}
	var envelope dialogListSnapshotEnvelope
	if err := json.Unmarshal(decoded, &envelope); err != nil {
		_ = s.c.Del(ctx, redisKey).Err()
		return store.DialogListSnapshotCacheValue{}, false, fmt.Errorf("decode dialog list snapshot: %w", err)
	}
	if envelope.Schema != dialogListSnapshotSchemaV7 || envelope.Key != key || envelope.Value.DependencyHash == 0 {
		_ = s.c.Del(ctx, redisKey).Err()
		return store.DialogListSnapshotCacheValue{}, false, fmt.Errorf("dialog list snapshot identity/schema mismatch")
	}
	return envelope.Value, true, nil
}

func (s *DialogListSnapshotCache) PutDialogListSnapshot(
	ctx context.Context,
	key store.DialogListSnapshotCacheKey,
	value store.DialogListSnapshotCacheValue,
) error {
	if s == nil || s.c == nil {
		return fmt.Errorf("dialog list snapshot Redis cache unavailable")
	}
	if err := validateDialogListSnapshotKey(key); err != nil {
		return err
	}
	if value.DependencyHash == 0 {
		return fmt.Errorf("invalid dialog list snapshot dependency hash")
	}
	decoded, err := json.Marshal(dialogListSnapshotEnvelope{
		Schema: dialogListSnapshotSchemaV7,
		Key:    key,
		Value:  value,
	})
	if err != nil {
		return fmt.Errorf("encode dialog list snapshot: %w", err)
	}
	if len(decoded) > dialogListSnapshotMaxDecodedBytes {
		return fmt.Errorf("dialog list snapshot exceeds %d decoded bytes: %d", dialogListSnapshotMaxDecodedBytes, len(decoded))
	}
	encoder, _, err := dialogListSnapshotCodecs()
	if err != nil {
		return err
	}
	raw := encoder.EncodeAll(decoded, nil)
	if len(raw) == 0 || len(raw) > dialogListSnapshotMaxEncodedBytes {
		return fmt.Errorf("dialog list snapshot exceeds %d encoded bytes: %d", dialogListSnapshotMaxEncodedBytes, len(raw))
	}
	if err := s.c.Set(ctx, dialogListSnapshotKey(key), raw, s.ttl).Err(); err != nil {
		return fmt.Errorf("redis set dialog list snapshot: %w", err)
	}
	return nil
}

func dialogListSnapshotCodecs() (*zstd.Encoder, *zstd.Decoder, error) {
	dialogListSnapshotCodecOnce.Do(func() {
		dialogListSnapshotEncoder, dialogListSnapshotCodecErr = zstd.NewWriter(
			nil,
			zstd.WithEncoderLevel(zstd.SpeedFastest),
		)
		if dialogListSnapshotCodecErr != nil {
			return
		}
		dialogListSnapshotDecoder, dialogListSnapshotCodecErr = zstd.NewReader(
			nil,
			zstd.WithDecoderMaxMemory(dialogListSnapshotMaxDecodedBytes),
			zstd.WithDecoderMaxWindow(dialogListSnapshotMaxDecodedBytes),
		)
	})
	if dialogListSnapshotCodecErr != nil {
		return nil, nil, fmt.Errorf("initialize dialog list snapshot codec: %w", dialogListSnapshotCodecErr)
	}
	return dialogListSnapshotEncoder, dialogListSnapshotDecoder, nil
}

func validateDialogListSnapshotKey(key store.DialogListSnapshotCacheKey) error {
	if key.UserID == 0 || key.OwnerHash == 0 {
		return fmt.Errorf("invalid dialog list snapshot key")
	}
	return nil
}

var _ store.DialogListSnapshotCache = (*DialogListSnapshotCache)(nil)
