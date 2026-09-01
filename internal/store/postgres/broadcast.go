package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"telesrv/internal/domain"
	"telesrv/internal/store"
	"telesrv/internal/store/postgres/sqlcgen"
)

// BroadcastStore persists system broadcast campaigns (see
// deploy/migrations/20260714003131_system_broadcasts.up.sql, extended by
// deploy/migrations/20260901000024_broadcast_lease_delivery_and_entities.up.sql
// with entities, incremental "all"-mode materialization and lease-based
// delivery claims).
type BroadcastStore struct {
	db sqlcgen.DBTX
}

// NewBroadcastStore builds the store on a pgx pool or transaction.
func NewBroadcastStore(db sqlcgen.DBTX) *BroadcastStore {
	return &BroadcastStore{db: db}
}

var _ store.BroadcastStore = (*BroadcastStore)(nil)

const eligibleBroadcastUsersSQL = `
FROM users
WHERE NOT is_bot
  AND deleted_at IS NULL
  AND id <> ALL($1::bigint[])`

// PreviewBroadcastRecipients counts (and, for "selected", validates) the
// intended recipient set without creating anything.
func (s *BroadcastStore) PreviewBroadcastRecipients(ctx context.Context, mode domain.BroadcastTargetMode, selectedUserIDs []int64) (int64, error) {
	switch mode {
	case domain.BroadcastTargetAll:
		var count int64
		if err := s.db.QueryRow(ctx, `SELECT count(*) `+eligibleBroadcastUsersSQL, domain.SystemUserIDs()).Scan(&count); err != nil {
			return 0, fmt.Errorf("count broadcast recipients: %w", err)
		}
		if count == 0 {
			return 0, domain.ErrBroadcastNoRecipients
		}
		return count, nil
	case domain.BroadcastTargetSelected:
		return validateSelectedBroadcastUsers(ctx, s.db, selectedUserIDs)
	default:
		return 0, domain.ErrBroadcastInvalid
	}
}

func validateSelectedBroadcastUsers(ctx context.Context, db sqlcgen.DBTX, selectedUserIDs []int64) (int64, error) {
	if len(selectedUserIDs) == 0 {
		return 0, domain.ErrBroadcastNoRecipients
	}
	var count int64
	if err := db.QueryRow(ctx, `
SELECT count(*)
FROM users
WHERE id = ANY($1::bigint[])
  AND NOT is_bot
  AND deleted_at IS NULL
  AND id <> ALL($2::bigint[])`, selectedUserIDs, domain.SystemUserIDs()).Scan(&count); err != nil {
		return 0, fmt.Errorf("validate broadcast recipients: %w", err)
	}
	if count != int64(len(selectedUserIDs)) {
		return 0, domain.ErrBroadcastRecipientInvalid
	}
	return count, nil
}

// CreateBroadcast inserts the broadcast row. For "all" mode it only
// snapshots the current max eligible user id and target count -- recipient
// rows are inserted incrementally by MaterializeBroadcastRecipients, not
// here. For "selected" mode, whose recipient list is already bounded by
// domain.MaxBroadcastSelectedRecipients, every recipient row is inserted in
// the same transaction as the broadcast itself, deduplicating via
// ON CONFLICT DO NOTHING against the (broadcast_id, user_id) unique
// constraint (a hand-built selected list could otherwise carry a repeat).
func (s *BroadcastStore) CreateBroadcast(ctx context.Context, message string, entities []domain.MessageEntity, mode domain.BroadcastTargetMode, selectedUserIDs []int64, createdBy string) (domain.Broadcast, error) {
	entitiesJSON, err := encodeMessageEntities(entities)
	if err != nil {
		return domain.Broadcast{}, fmt.Errorf("encode broadcast entities: %w", err)
	}
	var out domain.Broadcast
	err = withTx(ctx, s.db, "create broadcast", func(tx pgx.Tx) error {
		switch mode {
		case domain.BroadcastTargetAll:
			var maxUserID, count int64
			if err := tx.QueryRow(ctx, `
SELECT COALESCE(max(id), 0), count(*) `+eligibleBroadcastUsersSQL, domain.SystemUserIDs()).Scan(&maxUserID, &count); err != nil {
				return fmt.Errorf("snapshot broadcast recipients: %w", err)
			}
			if count == 0 {
				return domain.ErrBroadcastNoRecipients
			}
			row := tx.QueryRow(ctx, `
INSERT INTO broadcasts (
  message, entities, target_mode, snapshot_max_user_id, enumeration_done,
  target_count, created_by
) VALUES ($1, $2::jsonb, 'all', $3, false, $4, $5)
RETURNING `+broadcastColumns,
				message, string(entitiesJSON), maxUserID, count, createdBy,
			)
			if err := scanBroadcastRow(row, &out); err != nil {
				return fmt.Errorf("insert all-user broadcast: %w", err)
			}
		case domain.BroadcastTargetSelected:
			count, err := validateSelectedBroadcastUsers(ctx, tx, selectedUserIDs)
			if err != nil {
				return err
			}
			row := tx.QueryRow(ctx, `
INSERT INTO broadcasts (
  message, entities, target_mode, enumeration_done, target_count,
  materialized_count, created_by
) VALUES ($1, $2::jsonb, 'selected', true, $3, $3, $4)
RETURNING `+broadcastColumns,
				message, string(entitiesJSON), count, createdBy,
			)
			if err := scanBroadcastRow(row, &out); err != nil {
				return fmt.Errorf("insert selected broadcast: %w", err)
			}
			if _, err := tx.Exec(ctx, `
INSERT INTO broadcast_recipients (broadcast_id, user_id)
SELECT $1, user_id
FROM unnest($2::bigint[]) AS selected(user_id)
ON CONFLICT (broadcast_id, user_id) DO NOTHING`, out.ID, selectedUserIDs); err != nil {
				return fmt.Errorf("insert selected broadcast recipients: %w", err)
			}
		default:
			return domain.ErrBroadcastInvalid
		}
		return nil
	})
	if err != nil {
		return domain.Broadcast{}, err
	}
	return out, nil
}

// MaterializeBroadcastRecipients advances one all-user campaign with a
// single bounded keyset INSERT, picking whichever "all"-mode campaign still
// has enumeration left (oldest first) under FOR UPDATE SKIP LOCKED, so
// concurrent worker cycles never step on each other's progress.
func (s *BroadcastStore) MaterializeBroadcastRecipients(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var inserted int
	err := s.db.QueryRow(ctx, `
WITH campaign AS (
  SELECT id, snapshot_max_user_id, enumeration_cursor_user_id
  FROM broadcasts
  WHERE target_mode = 'all' AND NOT enumeration_done
  ORDER BY id
  FOR UPDATE SKIP LOCKED
  LIMIT 1
), candidates AS (
  SELECT u.id
  FROM campaign c
  JOIN LATERAL (
    SELECT id
    FROM users
    WHERE id > c.enumeration_cursor_user_id
      AND id <= c.snapshot_max_user_id
      AND NOT is_bot
      AND deleted_at IS NULL
      AND id <> ALL($1::bigint[])
    ORDER BY id
    LIMIT $2
  ) u ON true
), materialized AS (
  INSERT INTO broadcast_recipients (broadcast_id, user_id)
  SELECT c.id, candidate.id
  FROM campaign c
  CROSS JOIN candidates candidate
  ON CONFLICT (broadcast_id, user_id) DO NOTHING
  RETURNING user_id
), progress AS (
  UPDATE broadcasts b
  SET enumeration_cursor_user_id = COALESCE((SELECT max(id) FROM candidates), b.snapshot_max_user_id),
      enumeration_done = (SELECT count(*) FROM candidates) < $2,
      materialized_count = b.materialized_count + (SELECT count(*) FROM materialized),
      target_count = CASE
        WHEN (SELECT count(*) FROM candidates) < $2
        THEN b.materialized_count + (SELECT count(*) FROM materialized)
        ELSE b.target_count
      END
  FROM campaign c
  WHERE b.id = c.id
  RETURNING b.id
)
SELECT count(*)::int FROM materialized`, domain.SystemUserIDs(), limit).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("materialize broadcast recipients: %w", err)
	}
	return inserted, nil
}

// ClaimBroadcastRecipients atomically leases up to limit eligible rows
// (pending, or processing under an expired lease) to leaseToken, joining
// each claim with its broadcast's message and entities.
func (s *BroadcastStore) ClaimBroadcastRecipients(ctx context.Context, leaseToken string, limit int, lease time.Duration) ([]store.BroadcastRecipientClaim, error) {
	if strings.TrimSpace(leaseToken) == "" || len(leaseToken) > 64 {
		return nil, domain.ErrBroadcastInvalid
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	leaseSeconds := int(lease / time.Second)
	if leaseSeconds <= 0 || leaseSeconds > 3600 {
		leaseSeconds = 30
	}
	rows, err := s.db.Query(ctx, `
WITH candidates AS (
  SELECT id
  FROM broadcast_recipients
  WHERE (status = 'pending' AND next_attempt_at <= now())
     OR (status = 'processing' AND lease_until <= now())
  ORDER BY id
  FOR UPDATE SKIP LOCKED
  LIMIT $1
), claimed AS (
  UPDATE broadcast_recipients r
  SET status = 'processing',
      attempts = attempts + 1,
      lease_token = $2,
      lease_until = now() + make_interval(secs => $3),
      updated_at = now()
  FROM candidates c
  WHERE r.id = c.id
  RETURNING r.id, r.broadcast_id, r.user_id, r.attempts
)
SELECT c.id, c.broadcast_id, c.user_id, c.attempts, b.message, b.entities::text
FROM claimed c
JOIN broadcasts b ON b.id = c.broadcast_id
ORDER BY c.id`, limit, leaseToken, leaseSeconds)
	if err != nil {
		return nil, fmt.Errorf("claim broadcast recipients: %w", err)
	}
	defer rows.Close()
	out := make([]store.BroadcastRecipientClaim, 0, limit)
	for rows.Next() {
		var item store.BroadcastRecipientClaim
		var entitiesJSON string
		item.LeaseToken = leaseToken
		if err := rows.Scan(&item.RecipientID, &item.BroadcastID, &item.UserID, &item.Attempts, &item.Message, &entitiesJSON); err != nil {
			return nil, fmt.Errorf("scan broadcast recipient claim: %w", err)
		}
		entities, err := decodeMessageEntities(entitiesJSON)
		if err != nil {
			return nil, fmt.Errorf("decode broadcast recipient claim entities: %w", err)
		}
		item.Entities = entities
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate broadcast recipient claims: %w", err)
	}
	return out, nil
}

// CompleteBroadcastRecipient closes a claimed row as delivered and advances
// its broadcast's sent_count in the same transaction.
func (s *BroadcastStore) CompleteBroadcastRecipient(ctx context.Context, claim store.BroadcastRecipientClaim, privateMessageID int64, messageBoxID int, pts int) error {
	return withTx(ctx, s.db, "complete broadcast recipient", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
UPDATE broadcast_recipients
SET status = 'sent', lease_token = '', lease_until = NULL,
    last_error = '', private_message_id = $3, message_box_id = $4,
    pts = $5, sent_at = now(), updated_at = now()
WHERE id = $1 AND status = 'processing' AND lease_token = $2`,
			claim.RecipientID, claim.LeaseToken, privateMessageID, messageBoxID, pts)
		if err != nil {
			return fmt.Errorf("complete broadcast recipient: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrBroadcastLeaseLost
		}
		if _, err := tx.Exec(ctx, `
UPDATE broadcasts SET sent_count = sent_count + 1 WHERE id = $1`, claim.BroadcastID); err != nil {
			return fmt.Errorf("advance broadcast sent count: %w", err)
		}
		return nil
	})
}

// ReleaseBroadcastRecipient returns a claimed row to 'pending' with backoff,
// or to the terminal 'failed' once domain.MaxBroadcastRecipientAttempts is
// reached, advancing failed_count in that terminal case.
func (s *BroadcastStore) ReleaseBroadcastRecipient(ctx context.Context, claim store.BroadcastRecipientClaim, cause string) error {
	if len(cause) > 500 {
		cause = cause[:500]
	}
	_, err := s.db.Exec(ctx, `
WITH changed AS (
  UPDATE broadcast_recipients
  SET status = CASE WHEN attempts >= $3 THEN 'failed' ELSE 'pending' END,
      next_attempt_at = CASE
        WHEN attempts >= $3 THEN next_attempt_at
        ELSE now() + make_interval(secs => LEAST(300, (1 << LEAST(attempts, 8))))
      END,
      lease_token = '',
      lease_until = NULL,
      last_error = $4,
      updated_at = now()
  WHERE id = $1
    AND status = 'processing'
    AND lease_token = $2
  RETURNING broadcast_id, status
)
UPDATE broadcasts b
SET failed_count = failed_count + 1
FROM changed c
WHERE b.id = c.broadcast_id AND c.status = 'failed'`, claim.RecipientID, claim.LeaseToken, domain.MaxBroadcastRecipientAttempts, cause)
	if err != nil {
		return fmt.Errorf("release broadcast recipient: %w", err)
	}
	return nil
}

const broadcastColumns = `
id, message, entities::text, target_mode, target_count, materialized_count,
sent_count, failed_count, enumeration_done, created_by, created_at`

func scanBroadcastRow(row interface{ Scan(...any) error }, item *domain.Broadcast) error {
	var entitiesJSON string
	if err := row.Scan(&item.ID, &item.Message, &entitiesJSON, &item.TargetMode, &item.TargetCount, &item.MaterializedCount,
		&item.SentCount, &item.FailedCount, &item.EnumerationDone, &item.CreatedBy, &item.CreatedAt); err != nil {
		return err
	}
	entities, err := decodeMessageEntities(entitiesJSON)
	if err != nil {
		return fmt.Errorf("decode broadcast entities: %w", err)
	}
	item.Entities = entities
	return nil
}

// ListBroadcasts pages broadcasts newest-first.
func (s *BroadcastStore) ListBroadcasts(ctx context.Context, beforeID int64, limit int) ([]domain.Broadcast, bool, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `SELECT `+broadcastColumns+`
FROM broadcasts
WHERE $1::bigint = 0 OR id < $1
ORDER BY id DESC
LIMIT $2`, beforeID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list broadcasts: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Broadcast, 0, limit+1)
	for rows.Next() {
		var item domain.Broadcast
		if err := scanBroadcastRow(rows, &item); err != nil {
			return nil, false, fmt.Errorf("scan broadcast: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate broadcasts: %w", err)
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

// BroadcastByID returns one broadcast.
func (s *BroadcastStore) BroadcastByID(ctx context.Context, id int64) (domain.Broadcast, bool, error) {
	var item domain.Broadcast
	err := scanBroadcastRow(s.db.QueryRow(ctx, `SELECT `+broadcastColumns+`
FROM broadcasts
WHERE id = $1`, id), &item)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Broadcast{}, false, nil
		}
		return domain.Broadcast{}, false, fmt.Errorf("get broadcast: %w", err)
	}
	return item, true, nil
}
