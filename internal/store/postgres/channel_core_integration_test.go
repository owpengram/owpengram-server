package postgres

import (
	"context"
	"testing"

	"telesrv/internal/domain"
)

func TestChannelCreateInitialPtsBaselinePostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)
	users := NewUserStore(pool)
	owner := createTestUser(t, ctx, users, "+1887"+suffix+"80", "PtsOwner", "")
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
		Title:         "Initial pts " + suffix,
		Megagroup:     true,
		Date:          1_700_001_180,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channelID = created.Channel.ID
	if created.Channel.Pts != domain.FirstChannelEventPts || created.Message.Pts != domain.FirstChannelEventPts ||
		created.Event.Pts != domain.FirstChannelEventPts || created.Event.PtsCount != 1 {
		t.Fatalf("create result = channel:%+v message:%+v event:%+v, want first event 2/1", created.Channel, created.Message, created.Event)
	}

	var channelPts, messagePts, eventPts, eventPtsCount, retainedFloor, latestPts int
	if err := pool.QueryRow(ctx, `
SELECT c.pts, m.pts, e.pts, e.pts_count, cp.retained_through_pts, cp.latest_pts
FROM channels c
JOIN channel_messages m ON m.channel_id = c.id AND m.id = c.top_message_id
JOIN channel_update_events e ON e.channel_id = c.id AND e.message_id = m.id
JOIN channel_update_checkpoints cp ON cp.channel_id = c.id
WHERE c.id = $1`, channelID).Scan(
		&channelPts, &messagePts, &eventPts, &eventPtsCount, &retainedFloor, &latestPts,
	); err != nil {
		t.Fatalf("read persisted initial pts: %v", err)
	}
	if channelPts != domain.FirstChannelEventPts || messagePts != domain.FirstChannelEventPts ||
		eventPts != domain.FirstChannelEventPts || eventPtsCount != 1 ||
		retainedFloor != domain.InitialChannelPts || latestPts != domain.FirstChannelEventPts {
		t.Fatalf("persisted pts = channel:%d message:%d event:%d/%d checkpoint:%d/%d, want 2/2/2/1/1/2",
			channelPts, messagePts, eventPts, eventPtsCount, retainedFloor, latestPts)
	}
	fromBaseline, err := channels.ListChannelDifference(ctx, domain.ChannelDifferenceRequest{
		UserID: owner.ID, ChannelID: channelID, Pts: domain.InitialChannelPts, Limit: 10,
	})
	if err != nil {
		t.Fatalf("difference from baseline: %v", err)
	}
	if fromBaseline.TooLong || len(fromBaseline.Events) != 1 || fromBaseline.Events[0].Pts != domain.FirstChannelEventPts {
		t.Fatalf("difference from baseline = %+v, want create event at pts=2", fromBaseline)
	}
	fromZero, err := channels.ListChannelDifference(ctx, domain.ChannelDifferenceRequest{
		UserID: owner.ID, ChannelID: channelID, Pts: 0, Limit: 10,
	})
	if err != nil {
		t.Fatalf("difference from zero: %v", err)
	}
	if !fromZero.TooLong || fromZero.Pts != domain.FirstChannelEventPts || len(fromZero.NewMessages) != 1 {
		t.Fatalf("difference from zero = %+v, want complete snapshot at pts=2", fromZero)
	}
}

func TestChannelStoreGetChannelsBatchesVisibleAndPublicPreview(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)
	users := NewUserStore(pool)
	owner := createTestUser(t, ctx, users, "+1887"+suffix+"81", "BatchOwner", "")
	member := createTestUser(t, ctx, users, "+1887"+suffix+"82", "BatchMember", "")
	viewer := createTestUser(t, ctx, users, "+1887"+suffix+"83", "BatchViewer", "")
	var channelIDs []int64
	t.Cleanup(func() {
		if len(channelIDs) != 0 {
			_, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id = ANY($1::bigint[])", channelIDs)
		}
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1::bigint[])", []int64{owner.ID, member.ID, viewer.ID})
	})

	channels := NewChannelStore(pool)
	joined, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID,
		MemberUserIDs: []int64{viewer.ID},
		Title:         "batch joined " + suffix,
		Megagroup:     true,
		Date:          1700001200,
	})
	if err != nil {
		t.Fatalf("create joined channel: %v", err)
	}
	channelIDs = append(channelIDs, joined.Channel.ID)
	joinedWallpaper := &domain.Wallpaper{
		ID:     8181,
		NoFile: true,
		Settings: domain.WallpaperSettings{
			HasBackgroundColor: true,
			BackgroundColor:    0x66ccaa,
		},
	}
	if _, err := channels.SetChannelWallpaper(ctx, domain.SetChannelWallpaperRequest{
		UserID:    owner.ID,
		ChannelID: joined.Channel.ID,
		Wallpaper: joinedWallpaper,
		Date:      1700001201,
	}); err != nil {
		t.Fatalf("set joined wallpaper: %v", err)
	}
	publicCreated, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID,
		Title:         "batch public " + suffix,
		Broadcast:     true,
		Date:          1700001210,
	})
	if err != nil {
		t.Fatalf("create public channel: %v", err)
	}
	channelIDs = append(channelIDs, publicCreated.Channel.ID)
	publicChannel, err := channels.UpdateUsername(ctx, domain.UpdateChannelUsernameRequest{
		UserID:    owner.ID,
		ChannelID: publicCreated.Channel.ID,
		Username:  "batchpub" + suffix,
	})
	if err != nil {
		t.Fatalf("set public username: %v", err)
	}
	publicWallpaper := &domain.Wallpaper{
		ID:     8182,
		NoFile: true,
		Settings: domain.WallpaperSettings{
			HasBackgroundColor: true,
			BackgroundColor:    0x5aa0ee,
		},
	}
	if _, err := channels.SetChannelWallpaper(ctx, domain.SetChannelWallpaperRequest{
		UserID:    owner.ID,
		ChannelID: publicCreated.Channel.ID,
		Wallpaper: publicWallpaper,
		Date:      1700001211,
	}); err != nil {
		t.Fatalf("set public wallpaper: %v", err)
	}
	private, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID,
		MemberUserIDs: []int64{member.ID},
		Title:         "batch private " + suffix,
		Megagroup:     true,
		Date:          1700001220,
	})
	if err != nil {
		t.Fatalf("create private channel: %v", err)
	}
	channelIDs = append(channelIDs, private.Channel.ID)

	views, err := channels.GetChannels(ctx, viewer.ID, []int64{joined.Channel.ID, publicChannel.ID, private.Channel.ID, joined.Channel.ID})
	if err != nil {
		t.Fatalf("get channels batch: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("views len = %d, want joined + public preview", len(views))
	}
	if views[0].Channel.ID != joined.Channel.ID || views[0].Self.Status != domain.ChannelMemberActive {
		t.Fatalf("first view = %+v, want active joined channel", views[0])
	}
	if !domain.WallpaperEqual(views[0].Channel.Wallpaper, joinedWallpaper) {
		t.Fatalf("joined wallpaper = %+v, want %+v", views[0].Channel.Wallpaper, joinedWallpaper)
	}
	if views[0].Dialog.UserID != viewer.ID || views[0].Dialog.ChannelID != joined.Channel.ID || views[0].Dialog.TopMessageID == 0 {
		t.Fatalf("joined dialog = %+v, want viewer dialog", views[0].Dialog)
	}
	if views[1].Channel.ID != publicChannel.ID || views[1].Self.Status != domain.ChannelMemberLeft {
		t.Fatalf("second view = %+v, want public preview", views[1])
	}
	if !domain.WallpaperEqual(views[1].Channel.Wallpaper, publicWallpaper) {
		t.Fatalf("public wallpaper = %+v, want %+v", views[1].Channel.Wallpaper, publicWallpaper)
	}
	if views[1].Dialog.UserID != viewer.ID || views[1].Dialog.ChannelID != publicChannel.ID || views[1].Dialog.TopMessageID != views[1].Channel.TopMessageID {
		t.Fatalf("public preview dialog = %+v, want synthetic preview dialog", views[1].Dialog)
	}
}
