package postgres

import (
	"context"
	"testing"

	"telesrv/internal/domain"
)

func TestChannelActiveMembershipGenerationCoversMonoforumVisibility(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)
	users := NewUserStore(pool)
	owner, err := users.Create(ctx, domain.User{AccessHash: 971, Phone: "+1886" + suffix + "01", FirstName: "MonoVersionOwner"})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	subscriber, err := users.Create(ctx, domain.User{AccessHash: 972, Phone: "+1886" + suffix + "02", FirstName: "MonoVersionSubscriber"})
	if err != nil {
		t.Fatalf("create subscriber: %v", err)
	}
	channels := NewChannelStore(pool)
	created, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID, Title: "Mono Version " + suffix, Broadcast: true, Date: 1700007110,
	})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	enabled, err := channels.SetPaidMessagesPrice(ctx, owner.ID, created.Channel.ID, 0, true)
	if err != nil {
		t.Fatalf("enable monoforum: %v", err)
	}
	monoID := enabled.Channel.LinkedMonoforumID
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id = ANY($1::bigint[])", []int64{created.Channel.ID, monoID})
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1::bigint[])", []int64{owner.ID, subscriber.ID})
	})
	version := func(userID int64) int64 {
		t.Helper()
		var value int64
		if err := pool.QueryRow(ctx, `
SELECT COALESCE((
    SELECT version
    FROM read_model_versions
    WHERE model = 'channel_active_memberships'
      AND owner_user_id = $1 AND peer_type = 'user' AND peer_id = $1
), 0)`, userID).Scan(&value); err != nil {
			t.Fatalf("read generation for %d: %v", userID, err)
		}
		return value
	}

	beforeSend := version(subscriber.ID)
	sent, err := channels.SendMonoforumMessage(ctx, domain.SendMonoforumMessageRequest{
		MonoforumID: monoID, SenderUserID: subscriber.ID,
		SavedPeer: domain.Peer{Type: domain.PeerTypeUser, ID: subscriber.ID},
		RandomID:  7711, Message: "visibility", Date: 1700007111,
	})
	if err != nil {
		t.Fatalf("send monoforum message: %v", err)
	}
	if after := version(subscriber.ID); after <= beforeSend {
		t.Fatalf("subscriber generation after send = %d, want > %d", after, beforeSend)
	}
	active, err := channels.ListActiveChannelIDsForUser(ctx, subscriber.ID, 0, 1000)
	if err != nil {
		t.Fatalf("list subscriber active IDs: %v", err)
	}
	if !containsInt64(active, monoID) {
		t.Fatalf("subscriber active IDs = %v, want monoforum %d", active, monoID)
	}

	beforeDelete := version(subscriber.ID)
	if _, err := pool.Exec(ctx, `
UPDATE channel_messages
SET deleted = true
WHERE channel_id = $1 AND id = $2`, monoID, sent.Message.ID); err != nil {
		t.Fatalf("delete saved-peer message: %v", err)
	}
	if after := version(subscriber.ID); after <= beforeDelete {
		t.Fatalf("subscriber generation after delete = %d, want > %d", after, beforeDelete)
	}
	active, err = channels.ListActiveChannelIDsForUser(ctx, subscriber.ID, 0, 1000)
	if err != nil {
		t.Fatalf("list subscriber active IDs after delete: %v", err)
	}
	if containsInt64(active, monoID) {
		t.Fatalf("subscriber active IDs after last message delete = %v, monoforum remained", active)
	}

	beforeRights := version(owner.ID)
	if _, err := pool.Exec(ctx, `
UPDATE channel_members
SET admin_rights = admin_rights || '{"ManageDirectMessages": true}'::jsonb,
    updated_at = now()
WHERE channel_id = $1 AND user_id = $2`, created.Channel.ID, owner.ID); err != nil {
		t.Fatalf("update manager rights: %v", err)
	}
	if after := version(owner.ID); after <= beforeRights {
		t.Fatalf("manager generation after rights = %d, want > %d", after, beforeRights)
	}

	beforeToggleOwner := version(owner.ID)
	beforeToggleSubscriber := version(subscriber.ID)
	if _, err := channels.SetPaidMessagesPrice(ctx, owner.ID, created.Channel.ID, 0, false); err != nil {
		t.Fatalf("disable monoforum: %v", err)
	}
	if after := version(owner.ID); after <= beforeToggleOwner {
		t.Fatalf("manager generation after disable = %d, want > %d", after, beforeToggleOwner)
	}
	// The subscriber no longer has a live message, so it is intentionally not
	// part of the toggle fan-out. A previous deleted row cannot manufacture a
	// new active-page dependency.
	if after := version(subscriber.ID); after != beforeToggleSubscriber {
		t.Fatalf("deleted-only subscriber generation after disable = %d, want %d", after, beforeToggleSubscriber)
	}
}
