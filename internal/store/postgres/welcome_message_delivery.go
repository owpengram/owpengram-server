package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

var _ store.WelcomeMessageDeliveryStore = (*WelcomeMessageStore)(nil)

// enqueueWelcomeMessageDeliveriesTx snapshots all templates for one bounded
// batch of inactive->active transitions. It takes one channel-scoped transaction
// lock, then uses two set operations: remove every older epoch and create one new
// event per distinct member. The separate lock statement is intentional: a waiter
// must acquire a fresh READ COMMITTED snapshot after the prior transaction commits.
func enqueueWelcomeMessageDeliveriesTx(ctx context.Context, tx pgx.Tx, channelID int64, members []domain.ChannelMember) error {
	if tx == nil || channelID <= 0 {
		return domain.ErrWelcomeMessageInvalid
	}
	if len(members) == 0 {
		return nil
	}
	type activation struct {
		userID   int64
		joinedAt int
	}
	byUser := make(map[int64]activation, len(members))
	for _, member := range members {
		if member.ChannelID != channelID || member.UserID <= 0 || member.Status != domain.ChannelMemberActive ||
			member.JoinedAt <= 0 || member.JoinedAt > math.MaxInt32 {
			return domain.ErrWelcomeMessageInvalid
		}
		byUser[member.UserID] = activation{userID: member.UserID, joinedAt: member.JoinedAt}
	}
	activations := make([]activation, 0, len(byUser))
	for _, item := range byUser {
		activations = append(activations, item)
	}
	sort.Slice(activations, func(i, j int) bool { return activations[i].userID < activations[j].userID })
	userIDs := make([]int64, len(activations))
	joinedAt := make([]int32, len(activations))
	for i, item := range activations {
		userIDs[i] = item.userID
		joinedAt[i] = int32(item.joinedAt)
	}
	if err := lockWelcomeMessageDeliveryChannelTx(ctx, tx, channelID); err != nil {
		return err
	}
	// A later activation physically supersedes every older pending or delivered
	// epoch. The current membership transaction serializes competing transitions.
	if _, err := tx.Exec(ctx, `
DELETE FROM welcome_message_deliveries
WHERE channel_id = $1 AND target_user_id = ANY($2::bigint[])`, channelID, userIDs); err != nil {
		return fmt.Errorf("supersede previous welcome message deliveries: %w", err)
	}
	if _, err := tx.Exec(ctx, `
WITH incoming AS MATERIALIZED (
  SELECT user_id, joined_at
  FROM unnest($2::bigint[], $3::integer[]) AS value(user_id, joined_at)
), join_events AS MATERIALIZED (
  SELECT nextval('welcome_message_join_event_id_seq') AS id, user_id, joined_at
  FROM incoming
)
INSERT INTO welcome_message_deliveries (
  join_event_id, channel_id, target_user_id, template_id, joined_at, content,
  created_at, next_attempt_at, expires_at
)
SELECT e.id, w.channel_id, e.user_id, w.id, e.joined_at, w.content,
       now(), now(), now() + interval '24 hours'
FROM welcome_messages w
CROSS JOIN join_events e
WHERE w.channel_id = $1
ORDER BY e.user_id, w.id`, channelID, userIDs, joinedAt); err != nil {
		return fmt.Errorf("enqueue welcome message deliveries: %w", err)
	}
	return nil
}

func deleteWelcomeMessageDeliveriesTx(ctx context.Context, tx pgx.Tx, channelID int64, userIDs []int64) error {
	if tx == nil || channelID <= 0 || len(userIDs) == 0 {
		return domain.ErrWelcomeMessageInvalid
	}
	ids := append([]int64(nil), userIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	deduped := ids[:0]
	for _, id := range ids {
		if id <= 0 {
			return domain.ErrWelcomeMessageInvalid
		}
		if len(deduped) == 0 || deduped[len(deduped)-1] != id {
			deduped = append(deduped, id)
		}
	}
	if err := lockWelcomeMessageDeliveryChannelTx(ctx, tx, channelID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
DELETE FROM welcome_message_deliveries
WHERE channel_id = $1 AND target_user_id = ANY($2::bigint[])`, channelID, deduped); err != nil {
		return fmt.Errorf("delete welcome message deliveries: %w", err)
	}
	return nil
}

func deleteChannelWelcomeMessageDeliveriesTx(ctx context.Context, tx pgx.Tx, channelID int64) error {
	if tx == nil || channelID <= 0 {
		return domain.ErrWelcomeMessageInvalid
	}
	if err := lockWelcomeMessageDeliveryChannelTx(ctx, tx, channelID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM welcome_message_deliveries WHERE channel_id = $1`, channelID); err != nil {
		return fmt.Errorf("delete channel welcome message deliveries: %w", err)
	}
	return nil
}

func lockWelcomeMessageDeliveryChannelTx(ctx context.Context, tx pgx.Tx, channelID int64) error {
	if _, err := tx.Exec(ctx, `
SELECT pg_advisory_xact_lock(hashtextextended('welcome-message-delivery:' || $1::bigint::text, 0))`, channelID); err != nil {
		return fmt.Errorf("lock channel welcome message deliveries: %w", err)
	}
	return nil
}

func (s *WelcomeMessageStore) ClaimWelcomeMessageDeliveries(
	ctx context.Context,
	owner string,
	now time.Time,
	limit int,
	lease time.Duration,
) ([]domain.WelcomeMessageDelivery, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("welcome message delivery store is not configured")
	}
	owner = strings.TrimSpace(owner)
	if owner == "" || len(owner) > 128 || now.IsZero() || limit <= 0 || lease <= 0 {
		return nil, domain.ErrWelcomeMessageInvalid
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(ctx, `
WITH leaders AS (
  SELECT d.id, d.join_event_id
  FROM welcome_message_deliveries d
  WHERE d.delivered_at IS NULL
    AND d.expires_at > $2
    AND d.next_attempt_at <= $2
    AND (d.lease_expires_at IS NULL OR d.lease_expires_at <= $2)
    AND d.id = (
      SELECT min(first.id)
      FROM welcome_message_deliveries first
      WHERE first.join_event_id = d.join_event_id AND first.delivered_at IS NULL
    )
  ORDER BY d.next_attempt_at, d.id
  LIMIT $3
  FOR UPDATE OF d SKIP LOCKED
), claimed AS (
UPDATE welcome_message_deliveries d
SET lease_owner = $1,
    lease_expires_at = $2 + $4::interval,
    attempt_count = d.attempt_count + 1
FROM leaders leader
WHERE d.join_event_id = leader.join_event_id
  AND d.delivered_at IS NULL
  AND d.expires_at > $2
  AND d.next_attempt_at <= $2
  AND (d.lease_expires_at IS NULL OR d.lease_expires_at <= $2)
RETURNING d.id, d.join_event_id, d.channel_id, d.target_user_id,
          d.template_id, d.ephemeral_id, d.joined_at, d.content,
          d.attempt_count, d.expires_at
)
SELECT * FROM claimed
ORDER BY join_event_id, template_id, id`, owner, now, limit, lease.String())
	if err != nil {
		return nil, fmt.Errorf("claim welcome message deliveries: %w", err)
	}
	defer rows.Close()
	deliveries := make([]domain.WelcomeMessageDelivery, 0, limit)
	for rows.Next() {
		var delivery domain.WelcomeMessageDelivery
		var content []byte
		if err := rows.Scan(
			&delivery.ID, &delivery.JoinEventID, &delivery.ChannelID, &delivery.TargetUserID,
			&delivery.TemplateID, &delivery.EphemeralID, &delivery.JoinedAt, &content,
			&delivery.AttemptCount, &delivery.ExpiresAt,
		); err != nil {
			return nil, fmt.Errorf("scan welcome message delivery: %w", err)
		}
		if err := json.Unmarshal(content, &delivery.Content); err != nil {
			return nil, fmt.Errorf("decode welcome message delivery %d: %w", delivery.ID, err)
		}
		if err := delivery.ValidateStored(now); err != nil {
			return nil, fmt.Errorf("validate welcome message delivery %d: %w", delivery.ID, err)
		}
		deliveries = append(deliveries, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate welcome message deliveries: %w", err)
	}
	sort.Slice(deliveries, func(i, j int) bool {
		if deliveries[i].JoinEventID != deliveries[j].JoinEventID {
			return deliveries[i].JoinEventID < deliveries[j].JoinEventID
		}
		if deliveries[i].TemplateID != deliveries[j].TemplateID {
			return deliveries[i].TemplateID < deliveries[j].TemplateID
		}
		return deliveries[i].ID < deliveries[j].ID
	})
	return deliveries, nil
}

func (s *WelcomeMessageStore) AckWelcomeMessageDeliveries(ctx context.Context, owner string, ids []int64, deliveredAt time.Time) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("welcome message delivery store is not configured")
	}
	ids, ok := normalizeWelcomeDeliveryIDs(ids)
	if strings.TrimSpace(owner) == "" || !ok || deliveredAt.IsZero() {
		return 0, domain.ErrWelcomeMessageInvalid
	}
	tag, err := s.db.Exec(ctx, `
UPDATE welcome_message_deliveries
SET delivered_at = $3, lease_owner = NULL, lease_expires_at = NULL,
    last_error = ''
WHERE id = ANY($1::bigint[]) AND lease_owner = $2
  AND delivered_at IS NULL AND expires_at > $3`, ids, owner, deliveredAt)
	if err != nil {
		return 0, fmt.Errorf("ack welcome message deliveries: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (s *WelcomeMessageStore) RetryWelcomeMessageDeliveries(ctx context.Context, owner string, ids []int64, nextAttempt time.Time, lastError string) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("welcome message delivery store is not configured")
	}
	ids, ok := normalizeWelcomeDeliveryIDs(ids)
	if strings.TrimSpace(owner) == "" || !ok || nextAttempt.IsZero() {
		return 0, domain.ErrWelcomeMessageInvalid
	}
	tag, err := s.db.Exec(ctx, `
UPDATE welcome_message_deliveries
SET next_attempt_at = LEAST($3, expires_at),
    lease_owner = NULL, lease_expires_at = NULL, last_error = $4
WHERE id = ANY($1::bigint[]) AND lease_owner = $2 AND delivered_at IS NULL`,
		ids, owner, nextAttempt, truncateWelcomeDeliveryError(lastError))
	if err != nil {
		return 0, fmt.Errorf("retry welcome message deliveries: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (s *WelcomeMessageStore) DeleteExpiredWelcomeMessageDeliveries(ctx context.Context, now time.Time, limit int) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("welcome message delivery store is not configured")
	}
	if now.IsZero() || limit <= 0 {
		return 0, domain.ErrWelcomeMessageInvalid
	}
	if limit > 5000 {
		limit = 5000
	}
	tag, err := s.db.Exec(ctx, `
WITH expired AS (
  SELECT id
  FROM welcome_message_deliveries
  WHERE expires_at <= $1
  ORDER BY expires_at, id
  LIMIT $2
  FOR UPDATE SKIP LOCKED
)
DELETE FROM welcome_message_deliveries d
USING expired e
WHERE d.id = e.id`, now, limit)
	if err != nil {
		return 0, fmt.Errorf("delete expired welcome message deliveries: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func truncateWelcomeDeliveryError(value string) string {
	const maxRunes = 1024
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxRunes])
}

func normalizeWelcomeDeliveryIDs(ids []int64) ([]int64, bool) {
	if len(ids) == 0 {
		return nil, false
	}
	result := append([]int64(nil), ids...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	for i, id := range result {
		if id <= 0 || (i > 0 && id == result[i-1]) {
			return nil, false
		}
	}
	return result, true
}
