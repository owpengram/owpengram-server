package postgres

import (
	"context"
	"testing"

	"telesrv/internal/domain"
)

// Layer 233: channels.toggleForum carries a sidebar layout. It is stored in
// channels.forum_sidebar, read back with the channel, and cleared together
// with the forum.
func TestChannelStoreForumSidebarRoundTrip(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)
	users := NewUserStore(pool)
	owner, err := users.Create(ctx, domain.User{AccessHash: 93, Phone: "+1888" + suffix + "61", FirstName: "SidebarOwner"})
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
		Title:         "sidebar " + suffix,
		Megagroup:     true,
		Date:          1700000900,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channelID = created.Channel.ID

	forum, err := channels.SetForum(ctx, owner.ID, channelID, true, false, true)
	if err != nil {
		t.Fatalf("enable forum with sidebar: %v", err)
	}
	if !forum.Forum || !forum.ForumSidebar || forum.ForumTabs {
		t.Fatalf("set forum = forum %v tabs %v sidebar %v, want forum+sidebar", forum.Forum, forum.ForumTabs, forum.ForumSidebar)
	}
	view, err := channels.GetChannel(ctx, owner.ID, channelID)
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	if !view.Channel.ForumSidebar {
		t.Fatalf("forum_sidebar not stored")
	}

	off, err := channels.SetForum(ctx, owner.ID, channelID, false, false, true)
	if err != nil {
		t.Fatalf("disable forum: %v", err)
	}
	if off.Forum || off.ForumSidebar {
		t.Fatalf("disabled forum keeps sidebar: forum %v sidebar %v", off.Forum, off.ForumSidebar)
	}
	view, err = channels.GetChannel(ctx, owner.ID, channelID)
	if err != nil {
		t.Fatalf("get channel after disable: %v", err)
	}
	if view.Channel.ForumSidebar {
		t.Fatalf("forum_sidebar still stored after disabling the forum")
	}
}
