package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"

	"github.com/jackc/pgx/v5"

	"telesrv/internal/domain"
	"telesrv/internal/store"
	"telesrv/internal/store/postgres/sqlcgen"
)

type WelcomeMessageStore struct {
	db sqlcgen.DBTX
}

func NewWelcomeMessageStore(db sqlcgen.DBTX) *WelcomeMessageStore {
	return &WelcomeMessageStore{db: db}
}

var _ store.WelcomeMessageStore = (*WelcomeMessageStore)(nil)

const welcomeMessageColumns = `id, creator_user_id, date, edit_date, random_id,
       content, create_fingerprint, version`

type welcomeMessageRow interface {
	Scan(dest ...any) error
}

func (s *WelcomeMessageStore) CreateWelcomeMessage(ctx context.Context, req domain.CreateWelcomeMessageRequest) (stored domain.WelcomeMessage, created bool, err error) {
	if s == nil || s.db == nil {
		return domain.WelcomeMessage{}, false, fmt.Errorf("welcome message store is not configured")
	}
	if err := req.Validate(); err != nil {
		return domain.WelcomeMessage{}, false, err
	}
	content, err := json.Marshal(req.Content)
	if err != nil {
		return domain.WelcomeMessage{}, false, fmt.Errorf("marshal welcome message content: %w", err)
	}
	err = withTx(ctx, s.db, "create welcome message", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
INSERT INTO welcome_message_peers (channel_id)
VALUES ($1)
ON CONFLICT (channel_id) DO NOTHING`, req.Peer.ID); err != nil {
			return fmt.Errorf("ensure welcome message peer: %w", err)
		}
		var nextID int
		var revision int64
		if err := tx.QueryRow(ctx, `
SELECT next_id, revision
FROM welcome_message_peers
WHERE channel_id = $1
FOR UPDATE`, req.Peer.ID).Scan(&nextID, &revision); err != nil {
			return fmt.Errorf("lock welcome message peer: %w", err)
		}
		existing, err := scanWelcomeMessage(tx.QueryRow(ctx, `
SELECT `+welcomeMessageColumns+`
FROM welcome_messages
WHERE channel_id = $1 AND creator_user_id = $2 AND random_id = $3`,
			req.Peer.ID, req.CreatorUserID, req.RandomID), req.Peer)
		if err == nil {
			if !bytes.Equal(existing.CreateFingerprint[:], req.CreateFingerprint[:]) {
				return domain.ErrWelcomeMessageRandomIDConflict
			}
			stored = existing
			created = false
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("lookup welcome message idempotency key: %w", err)
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM welcome_messages WHERE channel_id = $1`, req.Peer.ID).Scan(&count); err != nil {
			return fmt.Errorf("count welcome messages: %w", err)
		}
		if count >= domain.MaxWelcomeMessagesPerPeer {
			return domain.ErrWelcomeMessageLimit
		}
		if nextID <= 0 || nextID >= domain.MaxMessageBoxID {
			return domain.ErrWelcomeMessageInvalid
		}
		nextRevision, err := domain.NextWelcomeRevision(revision)
		if err != nil {
			return err
		}
		stored, err = scanWelcomeMessage(tx.QueryRow(ctx, `
INSERT INTO welcome_messages (
  channel_id, id, creator_user_id, date, edit_date, random_id,
  content, create_fingerprint, version
) VALUES ($1,$2,$3,$4,0,$5,$6::jsonb,$7,1)
RETURNING `+welcomeMessageColumns,
			req.Peer.ID, nextID, req.CreatorUserID, req.Date, req.RandomID,
			content, req.CreateFingerprint[:]), req.Peer)
		if err != nil {
			return fmt.Errorf("insert welcome message: %w", err)
		}
		if _, err := tx.Exec(ctx, `
UPDATE welcome_message_peers
SET next_id = $2, revision = $3, updated_at = now()
WHERE channel_id = $1`, req.Peer.ID, nextID+1, nextRevision); err != nil {
			return fmt.Errorf("advance welcome message peer: %w", err)
		}
		created = true
		return nil
	})
	return stored, created, err
}

func (s *WelcomeMessageStore) EditWelcomeMessage(ctx context.Context, req domain.EditWelcomeMessageRequest) (stored domain.WelcomeMessage, err error) {
	if s == nil || s.db == nil {
		return domain.WelcomeMessage{}, fmt.Errorf("welcome message store is not configured")
	}
	if err := req.Validate(); err != nil {
		return domain.WelcomeMessage{}, err
	}
	err = withTx(ctx, s.db, "edit welcome message", func(tx pgx.Tx) error {
		var revision int64
		if err := tx.QueryRow(ctx, `
SELECT revision FROM welcome_message_peers WHERE channel_id = $1 FOR UPDATE`, req.Peer.ID).Scan(&revision); errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrWelcomeMessageNotFound
		} else if err != nil {
			return fmt.Errorf("lock welcome message peer: %w", err)
		}
		current, err := scanWelcomeMessage(tx.QueryRow(ctx, `
SELECT `+welcomeMessageColumns+` FROM welcome_messages
WHERE channel_id = $1 AND id = $2`, req.Peer.ID, req.ID), req.Peer)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrWelcomeMessageNotFound
		}
		if err != nil {
			return fmt.Errorf("get welcome message for edit: %w", err)
		}
		content, err := req.Fields.Apply(current.Content)
		if err != nil {
			return err
		}
		if reflect.DeepEqual(content, current.Content) {
			return domain.ErrWelcomeMessageNotModified
		}
		if current.Version >= math.MaxInt64 {
			return domain.ErrWelcomeMessageRevisionOverflow
		}
		nextRevision, err := domain.NextWelcomeRevision(revision)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(content)
		if err != nil {
			return fmt.Errorf("marshal edited welcome message content: %w", err)
		}
		stored, err = scanWelcomeMessage(tx.QueryRow(ctx, `
UPDATE welcome_messages
SET content = $3::jsonb, edit_date = GREATEST(date, $4),
    version = version + 1, updated_at = now()
WHERE channel_id = $1 AND id = $2
RETURNING `+welcomeMessageColumns, req.Peer.ID, req.ID, raw, req.EditDate), req.Peer)
		if err != nil {
			return fmt.Errorf("update welcome message: %w", err)
		}
		if _, err := tx.Exec(ctx, `
UPDATE welcome_message_peers SET revision = $2, updated_at = now() WHERE channel_id = $1`,
			req.Peer.ID, nextRevision); err != nil {
			return fmt.Errorf("advance welcome message revision: %w", err)
		}
		return nil
	})
	return stored, err
}

func (s *WelcomeMessageStore) ListWelcomeMessages(ctx context.Context, peer domain.Peer, hash int64) (result domain.WelcomeMessageList, err error) {
	if s == nil || s.db == nil {
		return result, fmt.Errorf("welcome message store is not configured")
	}
	if peer.Type != domain.PeerTypeChannel || peer.ID <= 0 || hash < 0 {
		return result, domain.ErrWelcomeMessageInvalid
	}
	err = withTx(ctx, s.db, "list welcome messages", func(tx pgx.Tx) error {
		var revision int64
		err := tx.QueryRow(ctx, `
SELECT revision FROM welcome_message_peers WHERE channel_id = $1 FOR SHARE`, peer.ID).Scan(&revision)
		if errors.Is(err, pgx.ErrNoRows) {
			revision = domain.InitialWelcomeRevision
		} else if err != nil {
			return fmt.Errorf("lock welcome message peer for read: %w", err)
		}
		result.Hash = revision
		if hash == revision {
			result.NotModified = true
			return nil
		}
		rows, err := tx.Query(ctx, `
SELECT `+welcomeMessageColumns+` FROM welcome_messages
WHERE channel_id = $1 ORDER BY id`, peer.ID)
		if err != nil {
			return fmt.Errorf("list welcome messages: %w", err)
		}
		defer rows.Close()
		result.Messages = make([]domain.WelcomeMessage, 0, domain.MaxWelcomeMessagesPerPeer)
		for rows.Next() {
			message, err := scanWelcomeMessage(rows, peer)
			if err != nil {
				return fmt.Errorf("scan welcome message list: %w", err)
			}
			result.Messages = append(result.Messages, message)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate welcome messages: %w", err)
		}
		return nil
	})
	return result, err
}

func (s *WelcomeMessageStore) DeleteWelcomeMessage(ctx context.Context, peer domain.Peer, id int) (succeeded bool, err error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("welcome message store is not configured")
	}
	if peer.Type != domain.PeerTypeChannel || peer.ID <= 0 || id <= 0 || id > domain.MaxMessageBoxID {
		return false, domain.ErrWelcomeMessageInvalid
	}
	err = withTx(ctx, s.db, "delete welcome message", func(tx pgx.Tx) error {
		var nextID int
		var revision int64
		if err := tx.QueryRow(ctx, `
SELECT next_id, revision FROM welcome_message_peers WHERE channel_id = $1 FOR UPDATE`, peer.ID).Scan(&nextID, &revision); errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrWelcomeMessageNotFound
		} else if err != nil {
			return fmt.Errorf("lock welcome message peer: %w", err)
		}
		tag, err := tx.Exec(ctx, `DELETE FROM welcome_messages WHERE channel_id = $1 AND id = $2`, peer.ID, id)
		if err != nil {
			return fmt.Errorf("delete welcome message: %w", err)
		}
		if tag.RowsAffected() == 0 {
			if id < nextID {
				succeeded = true
				return nil
			}
			return domain.ErrWelcomeMessageNotFound
		}
		nextRevision, err := domain.NextWelcomeRevision(revision)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
UPDATE welcome_message_peers SET revision = $2, updated_at = now() WHERE channel_id = $1`, peer.ID, nextRevision); err != nil {
			return fmt.Errorf("advance welcome message revision: %w", err)
		}
		succeeded = true
		return nil
	})
	return succeeded, err
}

func (s *WelcomeMessageStore) DeleteAllWelcomeMessages(ctx context.Context, peer domain.Peer) (succeeded bool, err error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("welcome message store is not configured")
	}
	if peer.Type != domain.PeerTypeChannel || peer.ID <= 0 {
		return false, domain.ErrWelcomeMessageInvalid
	}
	err = withTx(ctx, s.db, "delete all welcome messages", func(tx pgx.Tx) error {
		var revision int64
		err := tx.QueryRow(ctx, `
SELECT revision FROM welcome_message_peers WHERE channel_id = $1 FOR UPDATE`, peer.ID).Scan(&revision)
		if errors.Is(err, pgx.ErrNoRows) {
			succeeded = true
			return nil
		}
		if err != nil {
			return fmt.Errorf("lock welcome message peer: %w", err)
		}
		tag, err := tx.Exec(ctx, `DELETE FROM welcome_messages WHERE channel_id = $1`, peer.ID)
		if err != nil {
			return fmt.Errorf("delete all welcome messages: %w", err)
		}
		if tag.RowsAffected() > 0 {
			nextRevision, err := domain.NextWelcomeRevision(revision)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
UPDATE welcome_message_peers SET revision = $2, updated_at = now() WHERE channel_id = $1`, peer.ID, nextRevision); err != nil {
				return fmt.Errorf("advance welcome message revision: %w", err)
			}
		}
		succeeded = true
		return nil
	})
	return succeeded, err
}

func (s *WelcomeMessageStore) HasWelcomeMessages(ctx context.Context, peer domain.Peer) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("welcome message store is not configured")
	}
	if peer.Type != domain.PeerTypeChannel || peer.ID <= 0 {
		return false, domain.ErrWelcomeMessageInvalid
	}
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS (
  SELECT 1 FROM welcome_messages WHERE channel_id = $1
)`, peer.ID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check welcome messages: %w", err)
	}
	return exists, nil
}

func scanWelcomeMessage(row welcomeMessageRow, peer domain.Peer) (domain.WelcomeMessage, error) {
	var (
		message     domain.WelcomeMessage
		contentRaw  []byte
		fingerprint []byte
		version     int64
	)
	if err := row.Scan(&message.ID, &message.CreatorUserID, &message.Date, &message.EditDate,
		&message.RandomID, &contentRaw, &fingerprint, &version); err != nil {
		return domain.WelcomeMessage{}, err
	}
	if len(fingerprint) != sha256Size || version <= 0 {
		return domain.WelcomeMessage{}, domain.ErrWelcomeMessageInvalid
	}
	if err := json.Unmarshal(contentRaw, &message.Content); err != nil {
		return domain.WelcomeMessage{}, fmt.Errorf("decode welcome message content: %w", err)
	}
	message.Peer = peer
	message.Version = uint64(version)
	copy(message.CreateFingerprint[:], fingerprint)
	if err := message.ValidateStored(); err != nil {
		return domain.WelcomeMessage{}, err
	}
	return message, nil
}

const sha256Size = 32
