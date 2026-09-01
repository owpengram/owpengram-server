package postgres

import (
	"context"
	"testing"

	"telesrv/deploy"
)

const (
	broadcastLeaseDeliveryMigrationUp   = "migrations/20260901000024_broadcast_lease_delivery_and_entities.up.sql"
	broadcastLeaseDeliveryMigrationDown = "migrations/20260901000024_broadcast_lease_delivery_and_entities.down.sql"
)

// TestBroadcastLeaseDeliveryMigrationBackfillsLegacyDataPostgres proves the
// ALTER-based migration in
// deploy/migrations/20260901000024_broadcast_lease_delivery_and_entities.up.sql
// applies cleanly against pre-existing broadcasts/broadcast_recipients rows
// shaped by the original 20260714003131_system_broadcasts.up.sql schema --
// specifically 'sent' recipient rows that predate private_message_id/
// message_box_id/pts tracking, which the new sent-tracking CHECK constraint
// must accept as a legitimate legacy case rather than reject.
func TestBroadcastLeaseDeliveryMigrationBackfillsLegacyDataPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	upSQL, err := deploy.Migrations.ReadFile(broadcastLeaseDeliveryMigrationUp)
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	downSQL, err := deploy.Migrations.ReadFile(broadcastLeaseDeliveryMigrationDown)
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin broadcast lease delivery migration test: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	// Return the schema to its pre-migration (20260714003131) shape.
	if _, err := tx.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("revert broadcast lease delivery migration: %v", err)
	}

	// Seed fixtures shaped exactly like production rows created before this
	// migration existed: a broadcast with only total_count, and recipient
	// rows in every legacy status -- including a 'sent' row that carries no
	// delivery-identifier tracking at all, since that tracking didn't exist
	// yet when it was created.
	var broadcastID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO public.broadcasts (message, target_mode, total_count, created_by)
VALUES ('legacy campaign', 'selected', 3, 'legacy-admin')
RETURNING id`).Scan(&broadcastID); err != nil {
		t.Fatalf("insert legacy broadcast fixture: %v", err)
	}
	rows := []struct {
		userID int64
		status string
	}{
		{userID: 9_100_000_000_030_001, status: "sent"},
		{userID: 9_100_000_000_030_002, status: "pending"},
		{userID: 9_100_000_000_030_003, status: "failed"},
	}
	for _, r := range rows {
		var sentAtClause string
		if r.status == "sent" {
			sentAtClause = ", sent_at = now()"
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO public.broadcast_recipients (broadcast_id, user_id, status)
VALUES ($1, $2, $3)`, broadcastID, r.userID, r.status); err != nil {
			t.Fatalf("insert legacy recipient fixture (status=%s): %v", r.status, err)
		}
		if sentAtClause != "" {
			if _, err := tx.Exec(ctx, `
UPDATE public.broadcast_recipients SET sent_at = now() WHERE broadcast_id = $1 AND user_id = $2`, broadcastID, r.userID); err != nil {
				t.Fatalf("stamp legacy sent_at fixture: %v", err)
			}
		}
	}

	// Re-apply the migration under test. This must not fail against the
	// legacy 'sent' row above (private_message_id/message_box_id/pts all
	// still at their just-added zero default).
	if _, err := tx.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("apply broadcast lease delivery migration over legacy data: %v", err)
	}

	var targetCount, materializedCount, sentCount, failedCount int64
	var enumerationDone bool
	if err := tx.QueryRow(ctx, `
SELECT target_count, materialized_count, sent_count, failed_count, enumeration_done
FROM public.broadcasts WHERE id = $1`, broadcastID).Scan(&targetCount, &materializedCount, &sentCount, &failedCount, &enumerationDone); err != nil {
		t.Fatalf("read migrated broadcast: %v", err)
	}
	if targetCount != 3 {
		t.Fatalf("target_count = %d, want 3 (renamed from total_count)", targetCount)
	}
	if materializedCount != 3 {
		t.Fatalf("materialized_count = %d, want 3 (backfilled from target_count)", materializedCount)
	}
	if sentCount != 1 {
		t.Fatalf("sent_count = %d, want 1 (backfilled from recipient rows)", sentCount)
	}
	if failedCount != 1 {
		t.Fatalf("failed_count = %d, want 1 (backfilled from recipient rows)", failedCount)
	}
	if !enumerationDone {
		t.Fatalf("enumeration_done = false, want true (pre-existing campaigns were fully enumerated at creation)")
	}

	var status string
	var privateMessageID int64
	var messageBoxID, pts int
	if err := tx.QueryRow(ctx, `
SELECT status, private_message_id, message_box_id, pts
FROM public.broadcast_recipients
WHERE broadcast_id = $1 AND user_id = $2`, broadcastID, rows[0].userID).Scan(&status, &privateMessageID, &messageBoxID, &pts); err != nil {
		t.Fatalf("read migrated legacy sent recipient: %v", err)
	}
	if status != "sent" || privateMessageID != 0 || messageBoxID != 0 || pts != 0 {
		t.Fatalf("legacy sent recipient = status=%q private_message_id=%d message_box_id=%d pts=%d, want sent/0/0/0 (untracked legacy case accepted)",
			status, privateMessageID, messageBoxID, pts)
	}

	// A properly-tracked 'sent' row (what new code always writes, via
	// CompleteBroadcastRecipient) must also satisfy the CHECK.
	if _, err := tx.Exec(ctx, `
INSERT INTO public.broadcast_recipients (broadcast_id, user_id, status, sent_at, private_message_id, message_box_id, pts)
VALUES ($1, $2, 'sent', now(), 1, 1, 1)`, broadcastID, int64(9_100_000_000_030_099)); err != nil {
		t.Fatalf("insert of a properly-tracked 'sent' row failed: %v", err)
	}

	// But a 'sent' row with only some tracking columns populated -- neither
	// the legacy all-zero case nor the fully-tracked case -- must still be
	// rejected.
	if _, err := tx.Exec(ctx, `
INSERT INTO public.broadcast_recipients (broadcast_id, user_id, status, sent_at, private_message_id)
VALUES ($1, $2, 'sent', now(), 1)`, broadcastID, int64(9_100_000_000_030_098)); err == nil {
		t.Fatalf("insert of a partially-tracked 'sent' row unexpectedly succeeded")
	}
}
