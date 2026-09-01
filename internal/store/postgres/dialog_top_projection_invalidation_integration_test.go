package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"telesrv/internal/domain"
)

func TestDialogTopProjectionInvalidationTracksOnlyVisibleTopPayloads(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)

	users := NewUserStore(pool)
	owner, err := users.Create(ctx, domain.User{AccessHash: 1, Phone: "+1888" + suffix + "01", FirstName: "TopProjectionOwner"})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	friend, err := users.Create(ctx, domain.User{AccessHash: 2, Phone: "+1888" + suffix + "02", FirstName: "TopProjectionFriend"})
	if err != nil {
		t.Fatalf("create friend: %v", err)
	}
	var channelID int64
	t.Cleanup(func() {
		if channelID != 0 {
			_, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id = $1", channelID)
		}
		_, _ = pool.Exec(ctx, "DELETE FROM private_message_reactions WHERE user_id = ANY($1::bigint[]) OR message_sender_id = ANY($1::bigint[])", []int64{owner.ID, friend.ID})
		_, _ = pool.Exec(ctx, "DELETE FROM message_boxes WHERE owner_user_id = ANY($1::bigint[])", []int64{owner.ID, friend.ID})
		_, _ = pool.Exec(ctx, "DELETE FROM private_messages WHERE sender_user_id = ANY($1::bigint[])", []int64{owner.ID, friend.ID})
		_, _ = pool.Exec(ctx, "DELETE FROM user_update_events WHERE user_id = ANY($1::bigint[])", []int64{owner.ID, friend.ID})
		_, _ = pool.Exec(ctx, "DELETE FROM dialogs WHERE user_id = ANY($1::bigint[])", []int64{owner.ID, friend.ID})
		_, _ = pool.Exec(ctx, "DELETE FROM read_model_versions WHERE owner_user_id = ANY($1::bigint[]) OR (peer_type = 'user' AND peer_id = ANY($1::bigint[]))", []int64{owner.ID, friend.ID})
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1::bigint[])", []int64{owner.ID, friend.ID})
	})

	messages := NewMessageStore(pool)
	now := int(time.Now().Unix())
	older, err := messages.SendPrivateText(ctx, domain.SendPrivateTextRequest{
		SenderUserID: owner.ID, RecipientUserID: friend.ID, RandomID: time.Now().UnixNano(), Message: "older", Date: now,
	})
	if err != nil {
		t.Fatalf("send older private message: %v", err)
	}
	newer, err := messages.SendPrivateText(ctx, domain.SendPrivateTextRequest{
		SenderUserID: owner.ID, RecipientUserID: friend.ID, RandomID: time.Now().UnixNano() + 1, Message: "newer", Date: now + 1,
	})
	if err != nil {
		t.Fatalf("send newer private message: %v", err)
	}

	ownerDialogVersion := func() int64 {
		return testReadModelVersion(t, ctx, pool, "dialog_light", owner.ID, "user", friend.ID)
	}
	before := ownerDialogVersion()
	if _, err := pool.Exec(ctx, "UPDATE message_boxes SET body = body || '-edited' WHERE owner_user_id = $1 AND box_id = $2", owner.ID, older.SenderMessage.ID); err != nil {
		t.Fatalf("edit non-top private box: %v", err)
	}
	if got := ownerDialogVersion(); got != before {
		t.Fatalf("non-top private edit bumped dialog_light: before=%d after=%d", before, got)
	}
	if _, err := pool.Exec(ctx, "UPDATE message_boxes SET body = body || '-edited' WHERE owner_user_id = $1 AND box_id = $2", owner.ID, newer.SenderMessage.ID); err != nil {
		t.Fatalf("edit top private box: %v", err)
	}
	if got := ownerDialogVersion(); got <= before {
		t.Fatalf("top private edit did not bump dialog_light: before=%d after=%d", before, got)
	}

	before = ownerDialogVersion()
	if _, err := pool.Exec(ctx, `
INSERT INTO private_message_reactions
    (message_sender_id, private_message_id, user_id, reaction_type, reaction_value, reaction_date, chosen_order)
VALUES ($1, $2, $3, 'emoji', 'non-top', $4, 1)`, owner.ID, older.SenderMessage.UID, friend.ID, now+2); err != nil {
		t.Fatalf("insert non-top private reaction: %v", err)
	}
	if got := ownerDialogVersion(); got != before {
		t.Fatalf("non-top private reaction bumped dialog_light: before=%d after=%d", before, got)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO private_message_reactions
    (message_sender_id, private_message_id, user_id, reaction_type, reaction_value, reaction_date, chosen_order)
VALUES ($1, $2, $3, 'emoji', 'top', $4, 1)`, owner.ID, newer.SenderMessage.UID, friend.ID, now+3); err != nil {
		t.Fatalf("insert top private reaction: %v", err)
	}
	if got := ownerDialogVersion(); got <= before {
		t.Fatalf("top private reaction did not bump dialog_light: before=%d after=%d", before, got)
	}

	channels := NewChannelStore(pool)
	created, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID, Title: "Top Projection " + suffix, Megagroup: true, MemberUserIDs: []int64{friend.ID}, Date: now + 4,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channelID = created.Channel.ID
	oldChannelMessage, err := channels.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
		UserID: owner.ID, ChannelID: channelID, RandomID: time.Now().UnixNano() + 2, Message: "older channel", Date: now + 5,
	})
	if err != nil {
		t.Fatalf("send older channel message: %v", err)
	}
	topChannelMessage, err := channels.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
		UserID: owner.ID, ChannelID: channelID, RandomID: time.Now().UnixNano() + 3, Message: "top channel", Date: now + 6,
	})
	if err != nil {
		t.Fatalf("send top channel message: %v", err)
	}
	channelVersion := func() int64 {
		return testReadModelVersion(t, ctx, pool, "channel_base", 0, "channel", channelID)
	}
	before = channelVersion()
	if _, err := pool.Exec(ctx, "UPDATE channel_messages SET body = body || '-edited' WHERE channel_id = $1 AND id = $2", channelID, oldChannelMessage.Message.ID); err != nil {
		t.Fatalf("edit non-top channel message: %v", err)
	}
	if got := channelVersion(); got != before {
		t.Fatalf("non-top channel edit bumped channel_base: before=%d after=%d", before, got)
	}
	if _, err := pool.Exec(ctx, "UPDATE channel_messages SET body = body || '-edited' WHERE channel_id = $1 AND id = $2", channelID, topChannelMessage.Message.ID); err != nil {
		t.Fatalf("edit top channel message: %v", err)
	}
	if got := channelVersion(); got <= before {
		t.Fatalf("top channel edit did not bump channel_base: before=%d after=%d", before, got)
	}

	before = channelVersion()
	if _, err := pool.Exec(ctx, `
INSERT INTO channel_message_reactions
    (channel_id, message_id, reacted_user_id, sender_user_id, reaction_type, reaction_value, reaction_date, chosen_order)
VALUES ($1, $2, $3, $4, 'emoji', 'non-top', $5, 1)`, channelID, oldChannelMessage.Message.ID, friend.ID, owner.ID, now+7); err != nil {
		t.Fatalf("insert non-top channel reaction: %v", err)
	}
	if got := channelVersion(); got != before {
		t.Fatalf("non-top channel reaction bumped channel_base: before=%d after=%d", before, got)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO channel_message_reactions
    (channel_id, message_id, reacted_user_id, sender_user_id, reaction_type, reaction_value, reaction_date, chosen_order)
VALUES ($1, $2, $3, $4, 'emoji', 'top', $5, 1)`, channelID, topChannelMessage.Message.ID, friend.ID, owner.ID, now+8); err != nil {
		t.Fatalf("insert top channel reaction: %v", err)
	}
	if got := channelVersion(); got <= before {
		t.Fatalf("top channel reaction did not bump channel_base: before=%d after=%d", before, got)
	}
}

func testReadModelVersion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, model string, ownerUserID int64, peerType string, peerID int64) int64 {
	t.Helper()
	var version int64
	if err := pool.QueryRow(ctx, `
SELECT COALESCE((SELECT version
                 FROM read_model_versions
                 WHERE model = $1 AND owner_user_id = $2 AND peer_type = $3 AND peer_id = $4), 0)`,
		model, ownerUserID, peerType, peerID).Scan(&version); err != nil {
		t.Fatalf("read %s version: %v", model, err)
	}
	return version
}
