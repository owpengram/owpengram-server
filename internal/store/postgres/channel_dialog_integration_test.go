package postgres

import (
	"context"
	"telesrv/internal/domain"
	"testing"
)

func TestChannelStoreListDialogsUsesDateAndOffset(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)

	users := NewUserStore(pool)
	owner, err := users.Create(ctx, domain.User{
		AccessHash: 35,
		Phone:      "+1777" + suffix + "03",
		FirstName:  "DialogPageOwner",
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	var channelIDs []int64
	t.Cleanup(func() {
		if len(channelIDs) > 0 {
			_, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id = ANY($1::bigint[])", channelIDs)
		}
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", owner.ID)
	})

	channels := NewChannelStore(pool)
	older, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID,
		Title:         "Older Dialog " + suffix,
		Megagroup:     true,
		Date:          1700000310,
	})
	if err != nil {
		t.Fatalf("create older channel: %v", err)
	}
	channelIDs = append(channelIDs, older.Channel.ID)
	newer, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID,
		Title:         "Newer Dialog " + suffix,
		Megagroup:     true,
		Date:          1700000320,
	})
	if err != nil {
		t.Fatalf("create newer channel: %v", err)
	}
	channelIDs = append(channelIDs, newer.Channel.ID)

	first, err := channels.ListChannelDialogs(ctx, owner.ID, domain.DialogFilter{Limit: 1})
	if err != nil {
		t.Fatalf("list first channel dialogs: %v", err)
	}
	if len(first.Dialogs) != 1 || first.Dialogs[0].Peer.ID != newer.Channel.ID {
		t.Fatalf("first page dialogs = %+v, want newer channel by top date", first.Dialogs)
	}

	next, err := channels.ListChannelDialogs(ctx, owner.ID, domain.DialogFilter{
		OffsetDate:    first.Dialogs[0].TopMessageDate,
		OffsetID:      first.Dialogs[0].TopMessage,
		HasOffsetPeer: true,
		OffsetPeer:    first.Dialogs[0].Peer,
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("list next channel dialogs: %v", err)
	}
	if len(next.Dialogs) != 1 || next.Dialogs[0].Peer.ID != older.Channel.ID {
		t.Fatalf("next page dialogs = %+v, want older channel without repeating offset peer", next.Dialogs)
	}
}

func TestChannelStoreListDialogsWarmsDialogCache(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)

	users := NewUserStore(pool)
	owner, err := users.Create(ctx, domain.User{
		AccessHash: 36,
		Phone:      "+1777" + suffix + "13",
		FirstName:  "DialogWarmOwner",
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	var channelID int64
	t.Cleanup(func() {
		if channelID != 0 {
			_, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id = $1", channelID)
		}
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", owner.ID)
	})

	cache := NewChannelDialogCache(16)
	memberCache := NewChannelMemberCache(16)
	channels := NewChannelStore(pool, WithChannelDialogCache(cache), WithChannelMemberCache(memberCache))
	created, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID,
		Title:         "Dialog Warm " + suffix,
		Megagroup:     true,
		Date:          1700000325,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channelID = created.Channel.ID

	list, err := channels.ListChannelDialogs(ctx, owner.ID, domain.DialogFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list channel dialogs: %v", err)
	}
	if len(list.Dialogs) != 1 || list.Dialogs[0].Peer.ID != channelID {
		t.Fatalf("dialogs = %+v, want created channel", list.Dialogs)
	}

	cached, ok := cache.get(owner.ID, channelID)
	if !ok {
		t.Fatalf("channel dialog cache was not warmed")
	}
	if cached.TopMessageID != list.Dialogs[0].TopMessage ||
		cached.TopMessageDate != list.Dialogs[0].TopMessageDate ||
		cached.ReadInboxMaxID != list.Dialogs[0].ReadInboxMaxID {
		t.Fatalf("cached dialog = %+v, listed dialog = %+v", cached, list.Dialogs[0])
	}
}

func TestChannelStoreMaterializedSnapshotWarmsExactDialogCache(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)

	users := NewUserStore(pool)
	owner, err := users.Create(ctx, domain.User{
		AccessHash: 37,
		Phone:      "+1777" + suffix + "14",
		FirstName:  "SnapshotWarmOwner",
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	var channelID int64
	t.Cleanup(func() {
		if channelID != 0 {
			_, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id = $1", channelID)
		}
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", owner.ID)
	})

	cache := NewChannelDialogCache(16)
	memberCache := NewChannelMemberCache(16)
	channels := NewChannelStore(pool, WithChannelDialogCache(cache), WithChannelMemberCache(memberCache))
	created, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID,
		Title:         "Snapshot Warm " + suffix,
		Megagroup:     true,
		Date:          1700000326,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channelID = created.Channel.ID
	if _, err := pool.Exec(ctx, `
UPDATE channel_dialogs
SET default_send_as_peer_type = 'channel', default_send_as_peer_id = $2
WHERE user_id = $1 AND channel_id = $2`, owner.ID, channelID); err != nil {
		t.Fatalf("seed default send as: %v", err)
	}

	ownerSnapshot, err := channels.ListAllBuiltinChannelDialogSnapshot(ctx, owner.ID)
	if err != nil {
		t.Fatalf("list materialized owner snapshot: %v", err)
	}
	if len(ownerSnapshot.Dialogs) != 1 || ownerSnapshot.Dialogs[0].DefaultSendAs == nil ||
		ownerSnapshot.Dialogs[0].DefaultSendAs.Type != domain.PeerTypeChannel ||
		ownerSnapshot.Dialogs[0].DefaultSendAs.ID != channelID ||
		ownerSnapshot.Dialogs[0].ChannelMember == nil ||
		ownerSnapshot.Dialogs[0].ChannelMember.UserID != owner.ID ||
		ownerSnapshot.Dialogs[0].ChannelMember.ChannelID != channelID ||
		ownerSnapshot.Dialogs[0].ChannelMember.Status != domain.ChannelMemberActive {
		t.Fatalf("owner snapshot default send as = %+v", ownerSnapshot.Dialogs)
	}
	if _, ok := cache.get(owner.ID, channelID); ok {
		t.Fatal("owner snapshot scan alone must not warm cache before shared hydration")
	}
	if _, err := channels.HydrateChannelDialogSnapshot(ctx, owner.ID, ownerSnapshot.Dialogs); err != nil {
		t.Fatalf("hydrate materialized owner snapshot: %v", err)
	}
	cached, ok := cache.get(owner.ID, channelID)
	if !ok || cached.TopMessageID != ownerSnapshot.Dialogs[0].TopMessage ||
		cached.DefaultSendAs == nil || cached.DefaultSendAs.Type != domain.PeerTypeChannel ||
		cached.DefaultSendAs.ID != channelID {
		t.Fatalf("exact warmed dialog = %+v ok=%v", cached, ok)
	}
	warmedMember, ok := memberCache.get(channelID, owner.ID)
	if !ok || warmedMember.ChannelID != channelID || warmedMember.UserID != owner.ID ||
		warmedMember.Status != domain.ChannelMemberActive || warmedMember.Role != domain.ChannelRoleCreator {
		t.Fatalf("exact warmed member = %+v ok=%v", warmedMember, ok)
	}
}

func TestChannelStoreListDialogsScansChannelWallpaper(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)

	users := NewUserStore(pool)
	owner, err := users.Create(ctx, domain.User{
		AccessHash: 38,
		Phone:      "+1777" + suffix + "14",
		FirstName:  "DialogWallpaperOwner",
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	var channelID int64
	t.Cleanup(func() {
		if channelID != 0 {
			_, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id = $1", channelID)
		}
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", owner.ID)
	})

	channels := NewChannelStore(pool)
	created, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID,
		Title:         "Dialog Wallpaper " + suffix,
		Megagroup:     true,
		Date:          1700000326,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channelID = created.Channel.ID
	wallpaper := &domain.Wallpaper{
		ID:     9914,
		NoFile: true,
		Settings: domain.WallpaperSettings{
			HasBackgroundColor: true,
			BackgroundColor:    0x8fd15b,
			HasIntensity:       true,
			Intensity:          35,
		},
	}
	if _, err := channels.SetChannelWallpaper(ctx, domain.SetChannelWallpaperRequest{
		UserID:    owner.ID,
		ChannelID: channelID,
		Wallpaper: wallpaper,
		Date:      1700000327,
	}); err != nil {
		t.Fatalf("set channel wallpaper: %v", err)
	}

	list, err := channels.ListChannelDialogs(ctx, owner.ID, domain.DialogFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list channel dialogs: %v", err)
	}
	if len(list.Channels) != 1 || list.Channels[0].ID != channelID {
		t.Fatalf("channels = %+v, want wallpaper channel", list.Channels)
	}
	if !domain.WallpaperEqual(list.Channels[0].Wallpaper, wallpaper) {
		t.Fatalf("listed wallpaper = %+v, want %+v", list.Channels[0].Wallpaper, wallpaper)
	}
}

func TestChannelStoreListDialogsDerivesRecipientTopWithoutWriteFanout(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)

	users := NewUserStore(pool)
	owner, err := users.Create(ctx, domain.User{
		AccessHash: 41,
		Phone:      "+1777" + suffix + "31",
		FirstName:  "DialogTopOwner",
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	member, err := users.Create(ctx, domain.User{
		AccessHash: 42,
		Phone:      "+1777" + suffix + "32",
		FirstName:  "DialogTopMember",
	})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	var channelID int64
	t.Cleanup(func() {
		if channelID != 0 {
			_, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id = $1", channelID)
		}
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1::bigint[])", []int64{owner.ID, member.ID})
	})

	channels := NewChannelStore(pool)
	created, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID,
		Title:         "Dialog Top " + suffix,
		Megagroup:     true,
		MemberUserIDs: []int64{member.ID},
		Date:          1700000330,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channelID = created.Channel.ID
	if _, err := channels.ReadChannelHistory(ctx, domain.ReadChannelHistoryRequest{
		UserID:    member.ID,
		ChannelID: channelID,
		MaxID:     created.Message.ID,
		Date:      1700000331,
	}); err != nil {
		t.Fatalf("read initial service message: %v", err)
	}
	sent, err := channels.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
		UserID:    owner.ID,
		ChannelID: channelID,
		RandomID:  9331,
		Message:   "recipient top without write fanout",
		Date:      1700000332,
	})
	if err != nil {
		t.Fatalf("send channel message: %v", err)
	}

	list, err := channels.ListChannelDialogs(ctx, member.ID, domain.DialogFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list recipient channel dialogs: %v", err)
	}
	if len(list.Dialogs) != 1 {
		t.Fatalf("dialogs = %+v, want one channel dialog", list.Dialogs)
	}
	dialog := list.Dialogs[0]
	if dialog.TopMessage != sent.Message.ID || dialog.TopMessageDate != sent.Message.Date || dialog.UnreadCount != 1 {
		t.Fatalf("recipient dialog = %+v, want top sent message and unread=1", dialog)
	}
	if len(list.Messages) != 1 || list.Messages[0].ID != sent.Message.ID {
		t.Fatalf("dialog messages = %+v, want sent top message", list.Messages)
	}
}

func TestChannelStoreListDialogsSeeksBeyondQueryWindow(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)

	users := NewUserStore(pool)
	owner, err := users.Create(ctx, domain.User{
		AccessHash: 36,
		Phone:      "+1777" + suffix + "04",
		FirstName:  "DialogSeekOwner",
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	count := channelDialogQueryLimit + 5
	ids := make([]int64, count)
	baseID := owner.ID * 1000
	for i := range ids {
		ids[i] = baseID + int64(i+1)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id = ANY($1::bigint[])", ids)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", owner.ID)
	})
	if _, err := pool.Exec(ctx, `
INSERT INTO channels (
    id, access_hash, creator_user_id, title, broadcast, megagroup,
    participants_count, admins_count, top_message_id, pts, date
)
SELECT id, id + 900000, $2, 'Bulk Dialog ' || ord, false, true, 1, 1, 1, 1, (1700000400 + ord)::int
FROM unnest($1::bigint[]) WITH ORDINALITY AS t(id, ord)`, ids, owner.ID); err != nil {
		t.Fatalf("bulk insert channels: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO channel_members (channel_id, user_id, role, status, joined_at)
SELECT id, $2, 'creator', 'active', 1700000400
FROM unnest($1::bigint[]) AS t(id)`, ids, owner.ID); err != nil {
		t.Fatalf("bulk insert channel members: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO user_channel_member_index (user_id, channel_id, status, megagroup, broadcast, deleted, role)
SELECT $2, id, 'active', true, false, false, 'creator'
FROM unnest($1::bigint[]) AS t(id)`, ids, owner.ID); err != nil {
		t.Fatalf("bulk insert channel member index: %v", err)
	}

	channels := NewChannelStore(pool)
	headers, err := channels.ListChannelDialogSnapshotHeaders(ctx, owner.ID, domain.DialogFilter{})
	if err != nil {
		t.Fatalf("list channel dialog snapshot headers: %v", err)
	}
	if len(headers.Dialogs) != count || headers.Count != count || len(headers.Messages) != 0 || len(headers.Channels) != 0 {
		t.Fatalf("snapshot headers dialogs=%d count=%d messages=%d channels=%d, want %d lightweight headers",
			len(headers.Dialogs), headers.Count, len(headers.Messages), len(headers.Channels), count)
	}
	if headers.Dialogs[0].Peer.ID != ids[len(ids)-1] || headers.Dialogs[len(headers.Dialogs)-1].Peer.ID != ids[0] {
		t.Fatalf("snapshot header bounds = %d..%d, want %d..%d",
			headers.Dialogs[0].Peer.ID, headers.Dialogs[len(headers.Dialogs)-1].Peer.ID, ids[len(ids)-1], ids[0])
	}
	var cursor domain.Dialog
	var sixth domain.ChannelDialogList
	for page := 0; page < 6; page++ {
		filter := domain.DialogFilter{Limit: 100}
		if page > 0 {
			filter.OffsetDate = cursor.TopMessageDate
			filter.OffsetID = cursor.TopMessage
			filter.HasOffsetPeer = true
			filter.OffsetPeer = cursor.Peer
		}
		got, err := channels.ListChannelDialogs(ctx, owner.ID, filter)
		if err != nil {
			t.Fatalf("list channel dialogs page %d: %v", page+1, err)
		}
		if len(got.Dialogs) == 0 {
			t.Fatalf("page %d unexpectedly empty after cursor %+v", page+1, cursor)
		}
		if page < 5 && got.Count <= len(got.Dialogs) {
			t.Fatalf("page %d count = %d dialogs = %d, want bounded has-more signal", page+1, got.Count, len(got.Dialogs))
		}
		cursor = got.Dialogs[len(got.Dialogs)-1]
		if page == 5 {
			sixth = got
		}
	}
	if len(sixth.Dialogs) != 5 {
		t.Fatalf("sixth page len = %d, want remaining 5 beyond query window", len(sixth.Dialogs))
	}
	if sixth.Dialogs[0].Peer.ID != ids[4] || sixth.Dialogs[4].Peer.ID != ids[0] {
		t.Fatalf("sixth page dialogs = %+v, want oldest five descending by date", sixth.Dialogs)
	}

	included, err := channels.ListChannelDialogs(ctx, owner.ID, domain.DialogFilter{
		Folder: &domain.DialogFolder{
			IncludePeers: []domain.DialogFolderPeer{{
				Peer: domain.Peer{Type: domain.PeerTypeChannel, ID: ids[0]},
			}},
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("list included channel dialog beyond query window: %v", err)
	}
	if len(included.Dialogs) != 1 || included.Dialogs[0].Peer.ID != ids[0] {
		t.Fatalf("included dialogs = %+v, want oldest included channel beyond query window", included.Dialogs)
	}
}

func TestChannelStoreListDialogsFolderFiltersBeforeQueryLimit(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)

	users := NewUserStore(pool)
	owner, err := users.Create(ctx, domain.User{
		AccessHash: 37,
		Phone:      "+1777" + suffix + "05",
		FirstName:  "DialogFolderOwner",
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	count := channelDialogQueryLimit + 5
	ids := make([]int64, count)
	baseID := owner.ID*1000 + 100000
	for i := range ids {
		ids[i] = baseID + int64(i+1)
	}
	archivedID := ids[0]
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id = ANY($1::bigint[])", ids)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", owner.ID)
	})
	if _, err := pool.Exec(ctx, `
INSERT INTO channels (
    id, access_hash, creator_user_id, title, broadcast, megagroup,
    participants_count, admins_count, top_message_id, pts, date
)
SELECT id, id + 910000, $2, 'Folder Dialog ' || ord, false, true, 1, 1, 1, 1, (1700000500 + ord)::int
FROM unnest($1::bigint[]) WITH ORDINALITY AS t(id, ord)`, ids, owner.ID); err != nil {
		t.Fatalf("bulk insert channels: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO channel_members (channel_id, user_id, role, status, joined_at)
SELECT id, $2, 'creator', 'active', 1700000500
FROM unnest($1::bigint[]) AS t(id)`, ids, owner.ID); err != nil {
		t.Fatalf("bulk insert channel members: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO user_channel_member_index (user_id, channel_id, status, megagroup, broadcast, deleted, role)
SELECT $2, id, 'active', true, false, false, 'creator'
FROM unnest($1::bigint[]) AS t(id)`, ids, owner.ID); err != nil {
		t.Fatalf("bulk insert channel member index: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO channel_dialogs (user_id, channel_id, folder_id, top_message_id, top_message_date)
VALUES ($1, $2, $3, 1, 1700000500)`, owner.ID, archivedID, domain.DialogArchiveFolderID); err != nil {
		t.Fatalf("archive oldest channel dialog: %v", err)
	}

	archive, err := NewChannelStore(pool).ListChannelDialogs(ctx, owner.ID, domain.DialogFilter{
		HasFolderID: true,
		FolderID:    domain.DialogArchiveFolderID,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("list archive channel dialogs: %v", err)
	}
	if len(archive.Dialogs) != 1 || archive.Dialogs[0].Peer.ID != archivedID {
		t.Fatalf("archive dialogs = %+v, want archived channel beyond first query window", archive.Dialogs)
	}
	archiveHeaders, err := NewChannelStore(pool).ListChannelDialogSnapshotHeaders(ctx, owner.ID, domain.DialogFilter{
		HasFolderID: true,
		FolderID:    domain.DialogArchiveFolderID,
	})
	if err != nil {
		t.Fatalf("list archive channel snapshot headers: %v", err)
	}
	if len(archiveHeaders.Dialogs) != 1 || archiveHeaders.Dialogs[0].Peer.ID != archivedID {
		t.Fatalf("archive snapshot headers = %+v, want archived channel", archiveHeaders.Dialogs)
	}
	allHeaders, err := NewChannelStore(pool).ListAllBuiltinChannelDialogSnapshot(ctx, owner.ID)
	if err != nil {
		t.Fatalf("list all built-in channel snapshot headers: %v", err)
	}
	if len(allHeaders.Dialogs) != count {
		t.Fatalf("all built-in snapshot headers = %d, want %d", len(allHeaders.Dialogs), count)
	}
	foundArchived := false
	for _, dialog := range allHeaders.Dialogs {
		if dialog.Peer.ID == archivedID {
			foundArchived = dialog.FolderID == domain.DialogArchiveFolderID
			break
		}
	}
	if !foundArchived {
		t.Fatalf("all built-in snapshot did not retain archived channel %d", archivedID)
	}
}
