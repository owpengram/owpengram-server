package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestDialogOwnerReadModelSeedsAndAdvancesExactlyOnce(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	suffix := time.Now().UnixNano() % 1_000_000_000
	ownerID := int64(7_100_000_000) + suffix
	channelID := int64(8_100_000_000) + suffix
	phone := fmt.Sprintf("199%011d", suffix)
	if _, err := tx.Exec(ctx, `
INSERT INTO users (id, access_hash, phone, first_name)
VALUES ($1, $2, $3, 'dialog-owner-test')`, ownerID, ownerID+17, phone); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	assertDialogOwnerVersion(t, ctx, tx, ownerID, 1)

	if _, err := tx.Exec(ctx, `
INSERT INTO dialogs (user_id, peer_type, peer_id, top_message_id, top_message_date)
VALUES ($1, 'user', $2, 1, 10)`, ownerID, ownerID+1); err != nil {
		t.Fatalf("insert private dialog: %v", err)
	}
	assertDialogOwnerVersion(t, ctx, tx, ownerID, 2)
	if _, err := tx.Exec(ctx, `
UPDATE dialogs SET unread_count = 1, updated_at = now()
WHERE user_id = $1 AND peer_type = 'user' AND peer_id = $2`, ownerID, ownerID+1); err != nil {
		t.Fatalf("update private dialog: %v", err)
	}
	assertDialogOwnerVersion(t, ctx, tx, ownerID, 3)

	if _, err := tx.Exec(ctx, `
INSERT INTO dialog_drafts (user_id, peer_type, peer_id, date, draft)
VALUES ($1, 'user', $2, 11, '{"message":"draft"}'::jsonb)`, ownerID, ownerID+1); err != nil {
		t.Fatalf("insert draft: %v", err)
	}
	assertDialogOwnerVersion(t, ctx, tx, ownerID, 4)

	if _, err := tx.Exec(ctx, `
INSERT INTO channels (id, access_hash, creator_user_id, title, megagroup, date)
VALUES ($1, $2, $3, 'dialog-owner-channel', true, 12)`, channelID, channelID+19, ownerID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO channel_dialogs (user_id, channel_id, top_message_id, top_message_date)
VALUES ($1, $2, 1, 12)`, ownerID, channelID); err != nil {
		t.Fatalf("insert channel dialog: %v", err)
	}
	assertDialogOwnerVersion(t, ctx, tx, ownerID, 5)

	if _, err := tx.Exec(ctx, `
INSERT INTO channel_members (channel_id, user_id, role, status, joined_at)
VALUES ($1, $2, 'creator', 'active', 12)`, channelID, ownerID); err != nil {
		t.Fatalf("insert channel member: %v", err)
	}
	assertDialogOwnerVersion(t, ctx, tx, ownerID, 6)
	if _, err := tx.Exec(ctx, `
UPDATE channel_members SET read_inbox_max_id = 1, updated_at = now()
WHERE channel_id = $1 AND user_id = $2`, channelID, ownerID); err != nil {
		t.Fatalf("update channel member: %v", err)
	}
	assertDialogOwnerVersion(t, ctx, tx, ownerID, 7)

	// Every other exact-dialog dependency path (private top-message edits,
	// reactions, contacts/profile fan-out) converges through this helper.
	if _, err := tx.Exec(ctx, `SELECT public.telesrv_bump_dialog_light($1, 'user', $2)`, ownerID, ownerID+1); err != nil {
		t.Fatalf("bump exact dialog dependency: %v", err)
	}
	assertDialogOwnerVersion(t, ctx, tx, ownerID, 8)
}

func assertDialogOwnerVersion(t *testing.T, ctx context.Context, tx pgx.Tx, ownerID, want int64) {
	t.Helper()
	var got int64
	if err := tx.QueryRow(ctx, `
SELECT version
FROM read_model_versions
WHERE model = 'dialog_owner'
  AND owner_user_id = $1
  AND peer_type = 'user'
  AND peer_id = $1`, ownerID).Scan(&got); err != nil {
		t.Fatalf("read dialog_owner version: %v", err)
	}
	if got != want {
		t.Fatalf("dialog_owner version = %d, want %d", got, want)
	}
}
