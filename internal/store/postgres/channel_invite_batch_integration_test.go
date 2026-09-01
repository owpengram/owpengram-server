package postgres

import (
	"context"
	"fmt"
	"testing"

	"telesrv/internal/domain"
)

func TestChannelStoreInviteBatchAdvancesDistinctReadModelsOncePostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)
	users := NewUserStore(pool)

	owner, err := users.Create(ctx, domain.User{
		AccessHash: 960001,
		Phone:      "+1960" + suffix + "00",
		FirstName:  "BatchInviteOwner",
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	members := make([]domain.User, 8)
	userIDs := make([]int64, len(members))
	for i := range members {
		members[i], err = users.Create(ctx, domain.User{
			AccessHash: int64(960100 + i),
			Phone:      fmt.Sprintf("+1960%s%02d", suffix, i+1),
			FirstName:  fmt.Sprintf("BatchInvite%02d", i+1),
		})
		if err != nil {
			t.Fatalf("create member %d: %v", i, err)
		}
		// Deliberately reverse the input. The store must establish one canonical
		// lock/write order independent of the request order.
		userIDs[len(members)-1-i] = members[i].ID
	}
	allUserIDs := append([]int64{owner.ID}, userIDs...)
	var channelID int64
	t.Cleanup(func() {
		if channelID != 0 {
			_, _ = pool.Exec(ctx, `DELETE FROM channels WHERE id = $1`, channelID)
		}
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1::bigint[])`, allUserIDs)
	})

	channels := NewChannelStore(pool)
	created, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID,
		Title:         "Batch Invite " + suffix,
		Megagroup:     true,
		Date:          1700019600,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channelID = created.Channel.ID

	version := func(model string, ownerID int64, peerType string, peerID int64) int64 {
		t.Helper()
		var got int64
		err := pool.QueryRow(ctx, `
SELECT version
FROM read_model_versions
WHERE model=$1 AND owner_user_id=$2 AND peer_type=$3 AND peer_id=$4`, model, ownerID, peerType, peerID).Scan(&got)
		if err != nil {
			return 0
		}
		return got
	}
	participantsBefore := version("channel_participants", 0, "channel", channelID)
	dialogOwnerBefore := make(map[int64]int64, len(userIDs))
	for _, userID := range userIDs {
		dialogOwnerBefore[userID] = version("dialog_owner", userID, "user", userID)
	}

	invited, err := channels.InviteToChannel(ctx, channelID, owner.ID, userIDs, 1700019601)
	if err != nil {
		t.Fatalf("batch invite: %v", err)
	}
	if len(invited.Members) != len(userIDs) {
		t.Fatalf("invited members = %d, want %d", len(invited.Members), len(userIDs))
	}
	if len(invited.Recipients) != 0 {
		t.Fatalf("durable invite recipients = %v, want realtime audience derived from session fabric", invited.Recipients)
	}
	if invited.Event.Pts != created.Channel.Pts+1 || invited.Event.PtsCount != 1 || invited.Channel.Pts != invited.Event.Pts {
		t.Fatalf("invite pts=(event:%d/%d channel:%d), want one slot after %d", invited.Event.Pts, invited.Event.PtsCount, invited.Channel.Pts, created.Channel.Pts)
	}
	if invited.Message.Action == nil || invited.Message.Action.Type != domain.ChannelActionChatAddUser || len(invited.Message.Action.UserIDs) != len(userIDs) {
		t.Fatalf("invite service action = %+v, want all invited users", invited.Message.Action)
	}

	if got := version("channel_participants", 0, "channel", channelID); got != participantsBefore+1 {
		t.Fatalf("channel participants version = %d, want %d", got, participantsBefore+1)
	}
	for _, userID := range userIDs {
		if got := version("channel_member", userID, "channel", channelID); got != 1 {
			t.Errorf("channel_member version user %d = %d, want 1", userID, got)
		}
		if got := version("dialog_light", userID, "channel", channelID); got != 1 {
			t.Errorf("dialog_light version user %d = %d, want 1", userID, got)
		}
		if got := version("channel_active_memberships", userID, "user", userID); got != 1 {
			t.Errorf("active memberships version user %d = %d, want 1", userID, got)
		}
		if got := version("dialog_owner", userID, "user", userID); got != dialogOwnerBefore[userID]+1 {
			t.Errorf("dialog_owner version user %d = %d, want %d", userID, got, dialogOwnerBefore[userID]+1)
		}
	}

	var memberRows, indexRows, dialogRows, adminRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM channel_members WHERE channel_id=$1 AND user_id=ANY($2::bigint[]) AND status='active'`, channelID, userIDs).Scan(&memberRows); err != nil {
		t.Fatalf("count member rows: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_channel_member_index WHERE channel_id=$1 AND user_id=ANY($2::bigint[]) AND status='active'`, channelID, userIDs).Scan(&indexRows); err != nil {
		t.Fatalf("count membership indexes: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM channel_dialogs WHERE channel_id=$1 AND user_id=ANY($2::bigint[]) AND unread_count=1 AND unread_reactions_count=0`, channelID, userIDs).Scan(&dialogRows); err != nil {
		t.Fatalf("count dialog rows: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM channel_admin_log_events WHERE channel_id=$1 AND event_type='participant_invite'`, channelID).Scan(&adminRows); err != nil {
		t.Fatalf("count invite admin logs: %v", err)
	}
	if memberRows != len(userIDs) || indexRows != len(userIDs) || dialogRows != len(userIDs) || adminRows != len(userIDs) {
		t.Fatalf("batch rows member/index/dialog/admin = %d/%d/%d/%d, want %d each", memberRows, indexRows, dialogRows, adminRows, len(userIDs))
	}
}
