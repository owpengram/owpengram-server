package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"telesrv/internal/domain"
	storepkg "telesrv/internal/store"
)

func TestStoryStoreReadMaxHiddenAndPeerMaxIDsPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	owner, viewer := createStoryTestUsers(t, ctx, pool)
	store := NewStoryStore(pool)
	ownerPeer := domain.Peer{Type: domain.PeerTypeUser, ID: owner.ID}

	for _, id := range []int{1, 2} {
		if _, err := store.UpsertStory(ctx, domain.UpsertStoryRequest{Story: domain.Story{
			Owner:      ownerPeer,
			ID:         id,
			Date:       1700000100 + id,
			ExpireDate: 1700001000,
			Public:     true,
			Caption:    "pg story",
		}}); err != nil {
			t.Fatalf("upsert story %d: %v", id, err)
		}
	}

	read, err := store.MarkRead(ctx, viewer.ID, ownerPeer, 2, 1700000200)
	if err != nil {
		t.Fatalf("mark read: %v", err)
	}
	if !read.Advanced || read.MaxReadID != 2 {
		t.Fatalf("read = %+v, want advanced max 2", read)
	}
	read, err = store.MarkRead(ctx, viewer.ID, ownerPeer, 1, 1700000201)
	if err != nil {
		t.Fatalf("mark lower read: %v", err)
	}
	if read.Advanced || read.MaxReadID != 2 {
		t.Fatalf("lower read = %+v, want unchanged max 2", read)
	}

	peerStories, err := store.GetPeerStories(ctx, viewer.ID, ownerPeer, 1700000202)
	if err != nil {
		t.Fatalf("get peer stories: %v", err)
	}
	if peerStories.MaxReadID != 2 || len(peerStories.Stories) != 2 {
		t.Fatalf("peer stories = %+v, want max read 2 and two stories", peerStories)
	}
	recent, err := store.GetPeerMaxIDs(ctx, viewer.ID, []domain.Peer{ownerPeer}, 1700000202)
	if err != nil {
		t.Fatalf("peer max ids: %v", err)
	}
	if len(recent) != 1 || recent[0].MaxID != 2 {
		t.Fatalf("recent = %+v, want max id 2", recent)
	}

	list, err := store.ListActiveStories(ctx, viewer.ID, false, 1700000202, 100)
	if err != nil {
		t.Fatalf("list active stories: %v", err)
	}
	if list.Count != 1 || len(list.Peers) != 1 || len(list.Stories) != 2 {
		t.Fatalf("active list = %+v, want one peer with two stories", list)
	}
	ownerActive, err := store.ListOwnerActiveStories(ctx, ownerPeer, 1700000202, 100)
	if err != nil {
		t.Fatalf("list owner active stories: %v", err)
	}
	if len(ownerActive.Stories) != 2 {
		t.Fatalf("owner active stories = %+v, want two stories", ownerActive.Stories)
	}
	for _, story := range ownerActive.Stories {
		if story.Out || story.Views.HasViewers || story.SentReaction != nil {
			t.Fatalf("owner active story fanout snapshot = %+v, want no out/views/reaction", story)
		}
	}
	if err := store.SetPeerHidden(ctx, viewer.ID, ownerPeer, true); err != nil {
		t.Fatalf("set hidden: %v", err)
	}
	hiddenStates, err := store.GetPeerHiddenStates(ctx, viewer.ID, []domain.Peer{ownerPeer})
	if err != nil {
		t.Fatalf("get hidden states: %v", err)
	}
	if !hiddenStates[ownerPeer] {
		t.Fatalf("hidden states = %+v, want owner hidden", hiddenStates)
	}
	projections, err := store.GetPeerStoryProjections(ctx, viewer.ID, []domain.Peer{ownerPeer}, 1700000202)
	if err != nil {
		t.Fatalf("get story peer projections: %v", err)
	}
	if len(projections) != 1 || projections[0].Peer != ownerPeer || projections[0].Recent.MaxID != 2 || !projections[0].Hidden {
		t.Fatalf("story peer projections = %+v, want max id 2 hidden owner", projections)
	}
	expirations, err := store.ActiveStoryPeerExpirations(ctx, []domain.Peer{ownerPeer}, 1700000202)
	if err != nil || expirations[ownerPeer] != 1700001000 {
		t.Fatalf("active story peer expirations = %+v, %v; want owner=1700001000", expirations, err)
	}
	hiddenPeers, err := store.ListHiddenStoryPeers(ctx, viewer.ID)
	if err != nil || len(hiddenPeers) != 1 || hiddenPeers[0] != ownerPeer {
		t.Fatalf("hidden story peer snapshot = %+v, %v; want owner", hiddenPeers, err)
	}
	list, err = store.ListActiveStories(ctx, viewer.ID, false, 1700000202, 100)
	if err != nil {
		t.Fatalf("list visible after hidden: %v", err)
	}
	if list.Count != 0 || len(list.Stories) != 0 {
		t.Fatalf("visible list after hidden = %+v, want empty", list)
	}
	list, err = store.ListActiveStories(ctx, viewer.ID, true, 1700000202, 100)
	if err != nil {
		t.Fatalf("list hidden stories: %v", err)
	}
	if list.Count != 1 || len(list.Stories) != 2 {
		t.Fatalf("hidden list = %+v, want hidden peer stories", list)
	}
	if err := store.SetPeerHidden(ctx, viewer.ID, ownerPeer, false); err != nil {
		t.Fatalf("clear hidden: %v", err)
	}
	hiddenStates, err = store.GetPeerHiddenStates(ctx, viewer.ID, []domain.Peer{ownerPeer})
	if err != nil {
		t.Fatalf("get hidden states after clear: %v", err)
	}
	if hiddenStates[ownerPeer] {
		t.Fatalf("hidden states after clear = %+v, want owner visible", hiddenStates)
	}
	hiddenPeers, err = store.ListHiddenStoryPeers(ctx, viewer.ID)
	if err != nil || len(hiddenPeers) != 0 {
		t.Fatalf("hidden story peer snapshot after clear = %+v, %v; want empty", hiddenPeers, err)
	}
	expirations, err = store.ActiveStoryPeerExpirations(ctx, []domain.Peer{ownerPeer}, 1700001000)
	if err != nil || len(expirations) != 0 {
		t.Fatalf("active story peer expirations at boundary = %+v, %v; want empty", expirations, err)
	}
}

func TestStoryStoreListActiveStoriesPaginatesByPeerPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)
	users := NewUserStore(pool)
	viewer, err := users.Create(ctx, domain.User{AccessHash: 9100, Phone: "+1881" + suffix + "00", FirstName: "StoryViewer"})
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	owners := make([]domain.User, 0, 3)
	ownerIDs := make([]int64, 0, 3)
	for i := 0; i < 3; i++ {
		owner, err := users.Create(ctx, domain.User{AccessHash: int64(9101 + i), Phone: "+1881" + suffix + "0" + string(rune('1'+i)), FirstName: "StoryOwner"})
		if err != nil {
			t.Fatalf("create owner %d: %v", i, err)
		}
		owners = append(owners, owner)
		ownerIDs = append(ownerIDs, owner.ID)
	}
	t.Cleanup(func() {
		cleanupStoryPagingTestRows(t, context.Background(), pool, viewer.ID, ownerIDs)
	})
	store := NewStoryStore(pool)
	for i, owner := range owners {
		if _, err := store.UpsertStory(ctx, domain.UpsertStoryRequest{Story: domain.Story{
			Owner:      domain.Peer{Type: domain.PeerTypeUser, ID: owner.ID},
			ID:         1,
			Date:       1700000300 - i*100,
			ExpireDate: 1700001000,
			Public:     true,
		}}); err != nil {
			t.Fatalf("upsert owner %d story: %v", owner.ID, err)
		}
	}
	if _, err := store.UpsertStory(ctx, domain.UpsertStoryRequest{Story: domain.Story{
		Owner:      domain.Peer{Type: domain.PeerTypeUser, ID: owners[0].ID},
		ID:         2,
		Date:       1700000250,
		ExpireDate: 1700001000,
		Public:     true,
	}}); err != nil {
		t.Fatalf("upsert second story for first owner: %v", err)
	}

	first, err := store.ListActiveStoriesPage(ctx, viewer.ID, false, 1700000400, domain.StoryListCursor{}, 2)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if first.Count != 3 || !first.HasMore || len(first.Peers) != 2 {
		t.Fatalf("first page = count %d more %v peers %d, want count 3 more true peers 2", first.Count, first.HasMore, len(first.Peers))
	}
	if first.Peers[0].Peer.ID != owners[0].ID || len(first.Peers[0].Stories) != 2 {
		t.Fatalf("first peer = %+v stories %d, want first owner with two stories", first.Peers[0].Peer, len(first.Peers[0].Stories))
	}

	next, err := store.ListActiveStoriesPage(ctx, viewer.ID, false, 1700000400, domain.StoryListCursor{
		Set:  true,
		Date: 1700000200,
		Peer: domain.Peer{Type: domain.PeerTypeUser, ID: owners[1].ID},
	}, 2)
	if err != nil {
		t.Fatalf("next page: %v", err)
	}
	if next.Count != 3 || next.HasMore || len(next.Peers) != 1 || next.Peers[0].Peer.ID != owners[2].ID {
		t.Fatalf("next page = %+v, want final page with third owner", next)
	}
	digest, err := store.ActiveStoriesDigest(ctx, viewer.ID, false, 1700000400)
	if err != nil {
		t.Fatalf("digest active stories: %v", err)
	}
	if digest.Count != 3 {
		t.Fatalf("digest count = %d, want 3", digest.Count)
	}
	if _, err := store.MarkRead(ctx, viewer.ID, domain.Peer{Type: domain.PeerTypeUser, ID: owners[0].ID}, 2, 1700000401); err != nil {
		t.Fatalf("mark read for digest: %v", err)
	}
	changed, err := store.ActiveStoriesDigest(ctx, viewer.ID, false, 1700000400)
	if err != nil {
		t.Fatalf("changed digest active stories: %v", err)
	}
	if changed.Hash == digest.Hash {
		t.Fatalf("digest hash unchanged after read boundary: %#x", changed.Hash)
	}
}

func TestStoryStoreSelfUserViewAndReactionDoNotPolluteOwnerInteractionsPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	owner, _ := createStoryTestUsers(t, ctx, pool)
	store := NewStoryStore(pool)
	ownerPeer := domain.Peer{Type: domain.PeerTypeUser, ID: owner.ID}
	if _, err := store.UpsertStory(ctx, domain.UpsertStoryRequest{Story: domain.Story{
		Owner:      ownerPeer,
		ID:         1,
		Date:       1700000100,
		ExpireDate: 1700001000,
		Public:     true,
		Caption:    "pg self view",
	}}); err != nil {
		t.Fatalf("upsert story: %v", err)
	}

	created, err := store.IncrementViews(ctx, owner.ID, ownerPeer, []int{1}, 1700000200)
	if err != nil {
		t.Fatalf("increment self view: %v", err)
	}
	if created != 0 {
		t.Fatalf("created self views = %d, want 0", created)
	}
	if _, err := store.SetReaction(ctx, owner.ID, ownerPeer, 1, &domain.MessageReaction{
		Type:     domain.MessageReactionEmoji,
		Emoticon: "🔥",
	}, 1700000201); err != domain.ErrStoryPeerInvalid {
		t.Fatalf("self reaction err = %v, want ErrStoryPeerInvalid", err)
	}
	list, err := store.ListStoryViews(ctx, domain.StoryViewListRequest{
		ViewerUserID: owner.ID,
		Owner:        ownerPeer,
		StoryID:      1,
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("list story views: %v", err)
	}
	if list.Count != 0 || list.ViewsCount != 0 || list.ReactionsCount != 0 || len(list.Views) != 0 {
		t.Fatalf("self view list = %+v, want empty counters", list)
	}
}

func TestStoryStoreBlocklistHidesOwnerStoriesFromViewerPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	owner, blockedViewer := createStoryTestUsers(t, ctx, pool)
	users := NewUserStore(pool)
	otherViewer, err := users.Create(ctx, domain.User{AccessHash: 8103, Phone: "+1771999903" + randomSuffix(t), FirstName: "StoryOtherViewer"})
	if err != nil {
		t.Fatalf("create other viewer: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, otherViewer.ID); err != nil {
			t.Fatalf("cleanup other viewer: %v", err)
		}
	})

	store := NewStoryStore(pool)
	ownerPeer := domain.Peer{Type: domain.PeerTypeUser, ID: owner.ID}
	if _, err := store.UpsertStory(ctx, domain.UpsertStoryRequest{Story: domain.Story{
		Owner:      ownerPeer,
		ID:         1,
		Date:       1700000100,
		ExpireDate: 1700001000,
		Public:     true,
		Caption:    "pg blocked story",
	}}); err != nil {
		t.Fatalf("upsert story: %v", err)
	}
	if created, err := store.IncrementViews(ctx, blockedViewer.ID, ownerPeer, []int{1}, 1700000105); err != nil || created != 1 {
		t.Fatalf("pre-block increment = %d, %v, want 1 nil", created, err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO contact_blocks (owner_user_id, blocked_user_id, date)
VALUES ($1, $2, $3)
ON CONFLICT (owner_user_id, blocked_user_id) DO UPDATE SET date = EXCLUDED.date`,
		owner.ID, blockedViewer.ID, int32(1700000200)); err != nil {
		t.Fatalf("insert contact block: %v", err)
	}

	peerStories, err := store.GetPeerStories(ctx, blockedViewer.ID, ownerPeer, 1700000201)
	if err != nil {
		t.Fatalf("get blocked peer stories: %v", err)
	}
	if len(peerStories.Stories) != 0 {
		t.Fatalf("blocked peer stories = %+v, want empty", peerStories.Stories)
	}
	exact, err := store.GetStoriesByID(ctx, blockedViewer.ID, ownerPeer, []int{1}, 1700000201)
	if err != nil {
		t.Fatalf("get blocked stories by id: %v", err)
	}
	if len(exact.Stories) != 0 {
		t.Fatalf("blocked exact stories = %+v, want empty", exact.Stories)
	}
	recent, err := store.GetPeerMaxIDs(ctx, blockedViewer.ID, []domain.Peer{ownerPeer}, 1700000201)
	if err != nil {
		t.Fatalf("get blocked peer max ids: %v", err)
	}
	if len(recent) != 1 || recent[0].MaxID != 0 {
		t.Fatalf("blocked recent = %+v, want max id 0", recent)
	}
	active, err := store.ListActiveStories(ctx, blockedViewer.ID, false, 1700000201, 100)
	if err != nil {
		t.Fatalf("list blocked active stories: %v", err)
	}
	if active.Count != 0 || len(active.Stories) != 0 {
		t.Fatalf("blocked active stories = %+v, want empty", active)
	}
	if created, err := store.IncrementViews(ctx, blockedViewer.ID, ownerPeer, []int{1}, 1700000210); err != nil || created != 0 {
		t.Fatalf("blocked increment = %d, %v, want 0 nil", created, err)
	}
	if _, err := store.SetReaction(ctx, blockedViewer.ID, ownerPeer, 1, &domain.MessageReaction{
		Type:     domain.MessageReactionEmoji,
		Emoticon: "🔥",
	}, 1700000211); err != domain.ErrStoryNotFound {
		t.Fatalf("blocked reaction err = %v, want ErrStoryNotFound", err)
	}

	ownerStories, err := store.GetPeerStories(ctx, owner.ID, ownerPeer, 1700000201)
	if err != nil {
		t.Fatalf("owner get peer stories: %v", err)
	}
	if len(ownerStories.Stories) != 1 {
		t.Fatalf("owner peer stories = %+v, want one story", ownerStories.Stories)
	}
	otherStories, err := store.GetPeerStories(ctx, otherViewer.ID, ownerPeer, 1700000201)
	if err != nil {
		t.Fatalf("other get peer stories: %v", err)
	}
	if len(otherStories.Stories) != 1 {
		t.Fatalf("other peer stories = %+v, want one story", otherStories.Stories)
	}
	if created, err := store.IncrementViews(ctx, otherViewer.ID, ownerPeer, []int{1}, 1700000220); err != nil || created != 1 {
		t.Fatalf("other increment = %d, %v, want 1 nil", created, err)
	}
	list, err := store.ListStoryViews(ctx, domain.StoryViewListRequest{
		ViewerUserID: owner.ID,
		Owner:        ownerPeer,
		StoryID:      1,
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("list story views: %v", err)
	}
	got := map[int64]domain.StoryView{}
	for _, view := range list.Views {
		got[view.ViewerID] = view
	}
	if list.Count != 2 || len(list.Views) != 2 {
		t.Fatalf("view list = %+v, want historical blocked viewer and other viewer", list)
	}
	if !got[blockedViewer.ID].BlockedMyStoriesFrom {
		t.Fatalf("blocked viewer row = %+v, want blocked_my_stories_from", got[blockedViewer.ID])
	}
	if got[otherViewer.ID].BlockedMyStoriesFrom {
		t.Fatalf("other viewer row = %+v, want not blocked", got[otherViewer.ID])
	}
}

func TestStoryStoreChannelStoriesRequireActiveMemberPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)
	users := NewUserStore(pool)
	creator, err := users.Create(ctx, domain.User{AccessHash: 8111, Phone: "+1773" + suffix + "01", FirstName: "StoryChannelCreator"})
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}
	member, err := users.Create(ctx, domain.User{AccessHash: 8112, Phone: "+1773" + suffix + "02", FirstName: "StoryChannelMember"})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	outsider, err := users.Create(ctx, domain.User{AccessHash: 8113, Phone: "+1773" + suffix + "03", FirstName: "StoryChannelOutsider"})
	if err != nil {
		t.Fatalf("create outsider: %v", err)
	}

	channels := NewChannelStore(pool)
	createdChannel, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: creator.ID,
		Title:         "Story Gate " + suffix,
		Broadcast:     true,
		MemberUserIDs: []int64{member.ID},
		Date:          1700000300,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channelPeer := domain.Peer{Type: domain.PeerTypeChannel, ID: createdChannel.Channel.ID}
	userIDs := []int64{creator.ID, member.ID, outsider.ID}
	t.Cleanup(func() {
		cleanupChannelStoryTestRows(t, context.Background(), pool, channelPeer.ID, userIDs)
	})

	store := NewStoryStore(pool)
	createdStory, err := store.CreateStory(ctx, domain.StoryCreateRequest{
		Owner:    channelPeer,
		RandomID: 1700000301,
		Date:     1700000301,
		Period:   86400,
		Public:   true,
		Caption:  "member-only channel story",
	})
	if err != nil {
		t.Fatalf("create channel story: %v", err)
	}

	creatorStories, err := store.GetPeerStories(ctx, creator.ID, channelPeer, 1700000310)
	if err != nil {
		t.Fatalf("creator get channel peer stories: %v", err)
	}
	if len(creatorStories.Stories) != 1 {
		t.Fatalf("creator channel stories = %+v, want one story", creatorStories.Stories)
	}
	memberStories, err := store.GetPeerStories(ctx, member.ID, channelPeer, 1700000310)
	if err != nil {
		t.Fatalf("member get channel peer stories: %v", err)
	}
	if len(memberStories.Stories) != 1 {
		t.Fatalf("member channel stories = %+v, want one story", memberStories.Stories)
	}
	memberActive, err := store.ListActiveStories(ctx, member.ID, false, 1700000310, 100)
	if err != nil {
		t.Fatalf("member list active stories: %v", err)
	}
	if !storyListContains(memberActive, channelPeer, createdStory.Story.ID) {
		t.Fatalf("member active stories = %+v, want channel story %d", memberActive.Stories, createdStory.Story.ID)
	}
	memberProjection, err := store.GetPeerStoryProjections(ctx, member.ID, []domain.Peer{channelPeer}, 1700000310)
	if err != nil {
		t.Fatalf("member get projections: %v", err)
	}
	if len(memberProjection) != 1 || memberProjection[0].Recent.MaxID != createdStory.Story.ID {
		t.Fatalf("member projection = %+v, want story max id %d", memberProjection, createdStory.Story.ID)
	}

	outsiderStories, err := store.GetPeerStories(ctx, outsider.ID, channelPeer, 1700000310)
	if err != nil {
		t.Fatalf("outsider get channel peer stories: %v", err)
	}
	if len(outsiderStories.Stories) != 0 {
		t.Fatalf("outsider channel stories = %+v, want empty", outsiderStories.Stories)
	}
	exact, err := store.GetStoriesByID(ctx, outsider.ID, channelPeer, []int{createdStory.Story.ID}, 1700000310)
	if err != nil {
		t.Fatalf("outsider get stories by id: %v", err)
	}
	if len(exact.Stories) != 0 {
		t.Fatalf("outsider exact channel stories = %+v, want empty", exact.Stories)
	}
	recent, err := store.GetPeerMaxIDs(ctx, outsider.ID, []domain.Peer{channelPeer}, 1700000310)
	if err != nil {
		t.Fatalf("outsider get peer max ids: %v", err)
	}
	if len(recent) != 1 || recent[0].MaxID != 0 {
		t.Fatalf("outsider recent = %+v, want max id 0", recent)
	}
	projections, err := store.GetPeerStoryProjections(ctx, outsider.ID, []domain.Peer{channelPeer}, 1700000310)
	if err != nil {
		t.Fatalf("outsider get projections: %v", err)
	}
	if len(projections) != 1 || projections[0].Recent.MaxID != 0 {
		t.Fatalf("outsider projection = %+v, want max id 0", projections)
	}
	active, err := store.ListActiveStories(ctx, outsider.ID, false, 1700000310, 100)
	if err != nil {
		t.Fatalf("outsider list active stories: %v", err)
	}
	if storyListContains(active, channelPeer, createdStory.Story.ID) {
		t.Fatalf("outsider active stories = %+v, want no channel story %d", active.Stories, createdStory.Story.ID)
	}
	if created, err := store.IncrementViews(ctx, outsider.ID, channelPeer, []int{createdStory.Story.ID}, 1700000311); err != nil || created != 0 {
		t.Fatalf("outsider increment = %d, %v, want 0 nil", created, err)
	}
	if _, err := store.SetReaction(ctx, outsider.ID, channelPeer, createdStory.Story.ID, &domain.MessageReaction{
		Type:     domain.MessageReactionEmoji,
		Emoticon: "👍",
	}, 1700000312); !errors.Is(err, domain.ErrStoryNotFound) {
		t.Fatalf("outsider reaction err = %v, want ErrStoryNotFound", err)
	}

	if _, err := pool.Exec(ctx, `
UPDATE channel_members
SET banned_rights = '{"ViewMessages":true}'::jsonb
WHERE channel_id = $1 AND user_id = $2`, channelPeer.ID, member.ID); err != nil {
		t.Fatalf("ban member view messages: %v", err)
	}
	bannedStories, err := store.GetPeerStories(ctx, member.ID, channelPeer, 1700000313)
	if err != nil {
		t.Fatalf("banned member get channel peer stories: %v", err)
	}
	if len(bannedStories.Stories) != 0 {
		t.Fatalf("banned member stories = %+v, want empty", bannedStories.Stories)
	}
	if _, err := pool.Exec(ctx, `
UPDATE channel_members
SET banned_rights = '{}'::jsonb,
    status = 'left',
    left_at = $3
WHERE channel_id = $1 AND user_id = $2`, channelPeer.ID, member.ID, 1700000314); err != nil {
		t.Fatalf("mark member left: %v", err)
	}
	leftStories, err := store.GetPeerStories(ctx, member.ID, channelPeer, 1700000315)
	if err != nil {
		t.Fatalf("left member get channel peer stories: %v", err)
	}
	if len(leftStories.Stories) != 0 {
		t.Fatalf("left member stories = %+v, want empty", leftStories.Stories)
	}
}

func TestStoryStoreMediaAreasRoundTripEditClearPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	owner, _ := createStoryTestUsers(t, ctx, pool)
	store := NewStoryStore(pool)
	ownerPeer := domain.Peer{Type: domain.PeerTypeUser, ID: owner.ID}

	created, err := store.CreateStory(ctx, domain.StoryCreateRequest{
		Owner:      ownerPeer,
		RandomID:   1700000251,
		Date:       1700000250,
		Period:     86400,
		Public:     true,
		MediaAreas: []domain.StoryMediaArea{testPGStoryMediaArea("🔥", 10)},
	})
	if err != nil {
		t.Fatalf("create story: %v", err)
	}
	assertPGStoryMediaArea(t, created.Story, "🔥", 10)

	afterRestart := NewStoryStore(pool)
	list, err := afterRestart.GetStoriesByID(ctx, owner.ID, ownerPeer, []int{created.Story.ID}, 1700000260)
	if err != nil {
		t.Fatalf("get story after restart: %v", err)
	}
	if len(list.Stories) != 1 {
		t.Fatalf("story after restart = %+v, want one story", list.Stories)
	}
	assertPGStoryMediaArea(t, list.Stories[0], "🔥", 10)

	edited, err := afterRestart.EditStory(ctx, domain.StoryEditRequest{
		Owner:            ownerPeer,
		ID:               created.Story.ID,
		UpdateMediaAreas: true,
		MediaAreas:       []domain.StoryMediaArea{testPGStoryURLMediaArea("https://example.com/story/link", 25)},
	})
	if err != nil {
		t.Fatalf("edit story media areas: %v", err)
	}
	assertPGStoryURLMediaArea(t, edited.Story, "https://example.com/story/link", 25)

	reloaded, err := NewStoryStore(pool).GetStoriesByID(ctx, owner.ID, ownerPeer, []int{created.Story.ID}, 1700000261)
	if err != nil {
		t.Fatalf("reload edited story: %v", err)
	}
	assertPGStoryURLMediaArea(t, reloaded.Stories[0], "https://example.com/story/link", 25)

	geoEdited, err := NewStoryStore(pool).EditStory(ctx, domain.StoryEditRequest{
		Owner:            ownerPeer,
		ID:               created.Story.ID,
		UpdateMediaAreas: true,
		MediaAreas:       []domain.StoryMediaArea{testPGStoryGeoPointMediaArea(31.2304, 121.4737, 35)},
	})
	if err != nil {
		t.Fatalf("edit story geo media area: %v", err)
	}
	assertPGStoryGeoPointMediaArea(t, geoEdited.Story, 31.2304, 121.4737, 35)

	reloaded, err = NewStoryStore(pool).GetStoriesByID(ctx, owner.ID, ownerPeer, []int{created.Story.ID}, 1700000262)
	if err != nil {
		t.Fatalf("reload geo edited story: %v", err)
	}
	assertPGStoryGeoPointMediaArea(t, reloaded.Stories[0], 31.2304, 121.4737, 35)

	venueEdited, err := NewStoryStore(pool).EditStory(ctx, domain.StoryEditRequest{
		Owner:            ownerPeer,
		ID:               created.Story.ID,
		UpdateMediaAreas: true,
		MediaAreas:       []domain.StoryMediaArea{testPGStoryVenueMediaArea("Inline Cafe", 31.231, 121.474, 40)},
	})
	if err != nil {
		t.Fatalf("edit story venue media area: %v", err)
	}
	assertPGStoryVenueMediaArea(t, venueEdited.Story, "Inline Cafe", 31.231, 121.474, 40)

	reloaded, err = NewStoryStore(pool).GetStoriesByID(ctx, owner.ID, ownerPeer, []int{created.Story.ID}, 1700000263)
	if err != nil {
		t.Fatalf("reload venue edited story: %v", err)
	}
	assertPGStoryVenueMediaArea(t, reloaded.Stories[0], "Inline Cafe", 31.231, 121.474, 40)

	weatherEdited, err := NewStoryStore(pool).EditStory(ctx, domain.StoryEditRequest{
		Owner:            ownerPeer,
		ID:               created.Story.ID,
		UpdateMediaAreas: true,
		MediaAreas:       []domain.StoryMediaArea{testPGStoryWeatherMediaArea("☀️", 22.5, 0x00cc6600, 45)},
	})
	if err != nil {
		t.Fatalf("edit story weather media area: %v", err)
	}
	assertPGStoryWeatherMediaArea(t, weatherEdited.Story, "☀️", 22.5, 0x00cc6600, 45)

	reloaded, err = NewStoryStore(pool).GetStoriesByID(ctx, owner.ID, ownerPeer, []int{created.Story.ID}, 1700000264)
	if err != nil {
		t.Fatalf("reload weather edited story: %v", err)
	}
	assertPGStoryWeatherMediaArea(t, reloaded.Stories[0], "☀️", 22.5, 0x00cc6600, 45)

	channelPostEdited, err := NewStoryStore(pool).EditStory(ctx, domain.StoryEditRequest{
		Owner:            ownerPeer,
		ID:               created.Story.ID,
		UpdateMediaAreas: true,
		MediaAreas:       []domain.StoryMediaArea{testPGStoryChannelPostMediaArea(777001, 42, 50)},
	})
	if err != nil {
		t.Fatalf("edit story channel post media area: %v", err)
	}
	assertPGStoryChannelPostMediaArea(t, channelPostEdited.Story, 777001, 42, 50)

	reloaded, err = NewStoryStore(pool).GetStoriesByID(ctx, owner.ID, ownerPeer, []int{created.Story.ID}, 1700000265)
	if err != nil {
		t.Fatalf("reload channel post edited story: %v", err)
	}
	assertPGStoryChannelPostMediaArea(t, reloaded.Stories[0], 777001, 42, 50)

	starGiftEdited, err := NewStoryStore(pool).EditStory(ctx, domain.StoryEditRequest{
		Owner:            ownerPeer,
		ID:               created.Story.ID,
		UpdateMediaAreas: true,
		MediaAreas:       []domain.StoryMediaArea{testPGStoryStarGiftMediaArea("Gift.Series_01-42", 55)},
	})
	if err != nil {
		t.Fatalf("edit story star gift media area: %v", err)
	}
	assertPGStoryStarGiftMediaArea(t, starGiftEdited.Story, "Gift.Series_01-42", 55)

	reloaded, err = NewStoryStore(pool).GetStoriesByID(ctx, owner.ID, ownerPeer, []int{created.Story.ID}, 1700000266)
	if err != nil {
		t.Fatalf("reload star gift edited story: %v", err)
	}
	assertPGStoryStarGiftMediaArea(t, reloaded.Stories[0], "Gift.Series_01-42", 55)

	cleared, err := NewStoryStore(pool).EditStory(ctx, domain.StoryEditRequest{
		Owner:            ownerPeer,
		ID:               created.Story.ID,
		UpdateMediaAreas: true,
	})
	if err != nil {
		t.Fatalf("clear story media areas: %v", err)
	}
	if len(cleared.Story.MediaAreas) != 0 {
		t.Fatalf("cleared story media areas = %+v, want none", cleared.Story.MediaAreas)
	}
	reloaded, err = NewStoryStore(pool).GetStoriesByID(ctx, owner.ID, ownerPeer, []int{created.Story.ID}, 1700000267)
	if err != nil {
		t.Fatalf("reload cleared story: %v", err)
	}
	if len(reloaded.Stories) != 1 || len(reloaded.Stories[0].MediaAreas) != 0 {
		t.Fatalf("reloaded cleared story = %+v, want no media areas", reloaded.Stories)
	}
}

func TestStoryStoreArchiveCountAndSeekPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	owner, _ := createStoryTestUsers(t, ctx, pool)
	store := NewStoryStore(pool)
	ownerPeer := domain.Peer{Type: domain.PeerTypeUser, ID: owner.ID}
	fixtures := []domain.Story{
		{Owner: ownerPeer, ID: 1, Date: 1700000271, ExpireDate: 1700000300, Public: true},
		{Owner: ownerPeer, ID: 2, Date: 1700000272, ExpireDate: 1700000500, Public: true},
		{Owner: ownerPeer, ID: 3, Date: 1700000273, ExpireDate: 1700000300, Public: true, Pinned: true},
		{Owner: ownerPeer, ID: 4, Date: 1700000274, ExpireDate: 1700000300, Public: true, Deleted: true},
		{Owner: ownerPeer, ID: 5, Date: 1700000275, ExpireDate: 1700000300, Public: true},
	}
	for _, story := range fixtures {
		if _, err := store.UpsertStory(ctx, domain.UpsertStoryRequest{Story: story}); err != nil {
			t.Fatalf("upsert story %d: %v", story.ID, err)
		}
	}

	countOnly, err := store.ListStoriesArchive(ctx, owner.ID, ownerPeer, 0, 0, 1700000400)
	if err != nil {
		t.Fatalf("count archive stories: %v", err)
	}
	if countOnly.Count != 3 || len(countOnly.Stories) != 0 {
		t.Fatalf("count-only archive = %+v, want count 3 and no page", countOnly)
	}
	first, err := store.ListStoriesArchive(ctx, owner.ID, ownerPeer, 0, 2, 1700000400)
	if err != nil {
		t.Fatalf("list archive first page: %v", err)
	}
	if first.Count != 3 || len(first.Stories) != 2 || first.Stories[0].ID != 5 || first.Stories[1].ID != 3 || !first.Stories[1].Pinned {
		t.Fatalf("first archive page = %+v, want count 3 ids 5,3 with pinned story", first)
	}
	second, err := store.ListStoriesArchive(ctx, owner.ID, ownerPeer, 3, 2, 1700000400)
	if err != nil {
		t.Fatalf("list archive second page: %v", err)
	}
	if second.Count != 3 || len(second.Stories) != 1 || second.Stories[0].ID != 1 {
		t.Fatalf("second archive page = %+v, want count 3 id 1", second)
	}
}

func TestStoryStoreViewsAndReactionPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	owner, viewer := createStoryTestUsers(t, ctx, pool)
	store := NewStoryStore(pool)
	ownerPeer := domain.Peer{Type: domain.PeerTypeUser, ID: owner.ID}
	if _, err := store.UpsertStory(ctx, domain.UpsertStoryRequest{Story: domain.Story{
		Owner:      ownerPeer,
		ID:         1,
		Date:       1700000300,
		ExpireDate: 1700001000,
		Public:     true,
	}}); err != nil {
		t.Fatalf("upsert story: %v", err)
	}

	if created, err := store.IncrementViews(ctx, viewer.ID, ownerPeer, []int{1, 1}, 1700000301); err != nil || created != 1 {
		t.Fatalf("increment views first = %d, %v; want 1 nil", created, err)
	}
	if created, err := store.IncrementViews(ctx, viewer.ID, ownerPeer, []int{1}, 1700000302); err != nil || created != 0 {
		t.Fatalf("increment views duplicate = %d, %v; want 0 nil", created, err)
	}
	if _, err := store.IncrementViews(ctx, viewer.ID, ownerPeer, nil, 1700000302); !errors.Is(err, domain.ErrStoryIDInvalid) {
		t.Fatalf("empty increment views err = %v, want ErrStoryIDInvalid", err)
	}
	like := &domain.MessageReaction{Type: domain.MessageReactionEmoji, Emoticon: "like"}
	fire := &domain.MessageReaction{Type: domain.MessageReactionEmoji, Emoticon: "fire"}
	res, err := store.SetReaction(ctx, viewer.ID, ownerPeer, 1, like, 1700000303)
	if err != nil {
		t.Fatalf("set reaction: %v", err)
	}
	if !res.Changed || res.Story.Views.ViewsCount != 1 || res.Story.Views.ReactionsCount != 1 {
		t.Fatalf("reaction result = %+v, want one view and one reaction", res)
	}
	res, err = store.SetReaction(ctx, viewer.ID, ownerPeer, 1, fire, 1700000304)
	if err != nil {
		t.Fatalf("replace reaction: %v", err)
	}
	if !res.Changed || res.Story.Views.ReactionsCount != 1 || len(res.Story.Views.Reactions) != 1 || res.Story.Views.Reactions[0].Reaction.Emoticon != "fire" {
		t.Fatalf("replace result = %+v, want only fire reaction", res)
	}
	res, err = store.SetReaction(ctx, viewer.ID, ownerPeer, 1, fire, 1700000305)
	if err != nil {
		t.Fatalf("retry same reaction: %v", err)
	}
	if res.Changed || res.Date != 1700000304 || res.Story.Views.ReactionsCount != 1 || len(res.Story.Views.Reactions) != 1 || res.Story.Views.Reactions[0].Reaction.Emoticon != "fire" {
		t.Fatalf("retry same reaction result = %+v, want unchanged fire reaction at original date", res)
	}
	views, err := store.ListStoryViews(ctx, domain.StoryViewListRequest{
		ViewerUserID: owner.ID,
		Owner:        ownerPeer,
		StoryID:      1,
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("list story views: %v", err)
	}
	if views.Count != 1 || views.ViewsCount != 1 || views.ReactionsCount != 1 || len(views.Views) != 1 || views.Views[0].ViewerID != viewer.ID {
		t.Fatalf("views list = %+v, want one viewer with reaction", views)
	}
	reactions, err := store.ListStoryReactions(ctx, domain.StoryReactionListRequest{
		ViewerUserID: owner.ID,
		Owner:        ownerPeer,
		StoryID:      1,
		Reaction:     fire,
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("list story reactions: %v", err)
	}
	if reactions.Count != 1 || len(reactions.Reactions) != 1 || reactions.Reactions[0].ViewerID != viewer.ID {
		t.Fatalf("reactions list = %+v, want one filtered reaction", reactions)
	}
	if reactions.Reactions[0].Date != 1700000304 {
		t.Fatalf("reaction date after retry = %d, want original date", reactions.Reactions[0].Date)
	}
	custom := &domain.MessageReaction{Type: domain.MessageReactionCustomEmoji, DocumentID: 12345}
	res, err = store.SetReaction(ctx, viewer.ID, ownerPeer, 1, custom, 1700000305)
	if err != nil {
		t.Fatalf("replace with custom reaction: %v", err)
	}
	if !res.Changed || res.Story.Views.ReactionsCount != 1 || len(res.Story.Views.Reactions) != 1 || res.Story.Views.Reactions[0].Reaction.DocumentID != 12345 {
		t.Fatalf("custom replace result = %+v, want one custom reaction", res)
	}
	res, err = store.SetReaction(ctx, viewer.ID, ownerPeer, 1, custom, 1700000306)
	if err != nil {
		t.Fatalf("retry same custom reaction: %v", err)
	}
	if res.Changed || res.Date != 1700000305 || res.Story.Views.ReactionsCount != 1 || len(res.Story.Views.Reactions) != 1 || res.Story.Views.Reactions[0].Reaction.DocumentID != 12345 {
		t.Fatalf("retry same custom reaction result = %+v, want unchanged custom reaction at original date", res)
	}
	reactions, err = store.ListStoryReactions(ctx, domain.StoryReactionListRequest{
		ViewerUserID: owner.ID,
		Owner:        ownerPeer,
		StoryID:      1,
		Reaction:     custom,
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("list custom story reactions: %v", err)
	}
	if reactions.Count != 1 || len(reactions.Reactions) != 1 || reactions.Reactions[0].ViewerID != viewer.ID || reactions.Reactions[0].Date != 1700000305 || reactions.Reactions[0].Reaction == nil || reactions.Reactions[0].Reaction.DocumentID != 12345 {
		t.Fatalf("custom story reactions = %+v, want original custom reaction date", reactions)
	}
	if _, err := store.ListStoryViews(ctx, domain.StoryViewListRequest{
		ViewerUserID: owner.ID,
		Owner:        ownerPeer,
		StoryID:      1,
		Offset:       "bad",
		Limit:        10,
	}); !errors.Is(err, domain.ErrStoryOffsetInvalid) {
		t.Fatalf("bad story views offset err = %v, want ErrStoryOffsetInvalid", err)
	}
	if _, err := store.ListStoryReactions(ctx, domain.StoryReactionListRequest{
		ViewerUserID: owner.ID,
		Owner:        ownerPeer,
		StoryID:      1,
		Offset:       "1:1700000304:2001",
		Limit:        10,
	}); !errors.Is(err, domain.ErrStoryOffsetInvalid) {
		t.Fatalf("bad story reactions offset err = %v, want ErrStoryOffsetInvalid", err)
	}
	res, err = store.SetReaction(ctx, viewer.ID, ownerPeer, 1, nil, 1700000307)
	if err != nil {
		t.Fatalf("clear reaction: %v", err)
	}
	if !res.Changed || res.Story.Views.ReactionsCount != 0 || len(res.Story.Views.Reactions) != 0 {
		t.Fatalf("clear result = %+v, want no reactions", res)
	}

	afterRestart := NewStoryStore(pool)
	list, err := afterRestart.GetStoriesByID(ctx, viewer.ID, ownerPeer, []int{1}, 1700000307)
	if err != nil {
		t.Fatalf("get story after restart: %v", err)
	}
	if len(list.Stories) != 1 || list.Stories[0].Views.ViewsCount != 1 || list.Stories[0].Views.ReactionsCount != 0 || list.Stories[0].SentReaction != nil {
		t.Fatalf("story after restart = %+v, want durable view with cleared reaction", list.Stories)
	}
}

func TestStoryStoreExpiredUnpinnedStoriesDoNotAcceptNewInteractionsPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	owner, viewer := createStoryTestUsers(t, ctx, pool)
	_, otherViewer := createStoryTestUsers(t, ctx, pool)
	store := NewStoryStore(pool)
	ownerPeer := domain.Peer{Type: domain.PeerTypeUser, ID: owner.ID}
	for _, story := range []domain.Story{
		{Owner: ownerPeer, ID: 1, Date: 1700000100, ExpireDate: 1700000150, Public: true},
		{Owner: ownerPeer, ID: 2, Date: 1700000101, ExpireDate: 1700000150, Public: true, Pinned: true},
	} {
		if _, err := store.UpsertStory(ctx, domain.UpsertStoryRequest{Story: story}); err != nil {
			t.Fatalf("upsert story %d: %v", story.ID, err)
		}
	}

	if created, err := store.IncrementViews(ctx, viewer.ID, ownerPeer, []int{1}, 1700000200); err != nil || created != 0 {
		t.Fatalf("expired unpinned increment = %d, %v; want 0 nil", created, err)
	}
	if _, err := store.SetReaction(ctx, viewer.ID, ownerPeer, 1, &domain.MessageReaction{Type: domain.MessageReactionEmoji, Emoticon: "like"}, 1700000200); !errors.Is(err, domain.ErrStoryNotFound) {
		t.Fatalf("expired unpinned reaction err = %v, want ErrStoryNotFound", err)
	}
	if created, err := store.IncrementViews(ctx, viewer.ID, ownerPeer, []int{2}, 1700000200); err != nil || created != 1 {
		t.Fatalf("expired pinned increment = %d, %v; want 1 nil", created, err)
	}
	res, err := store.SetReaction(ctx, otherViewer.ID, ownerPeer, 2, &domain.MessageReaction{Type: domain.MessageReactionEmoji, Emoticon: "fire"}, 1700000201)
	if err != nil {
		t.Fatalf("expired pinned reaction: %v", err)
	}
	if !res.Changed || res.Story.Views.ViewsCount != 2 || res.Story.Views.ReactionsCount != 1 {
		t.Fatalf("expired pinned reaction result = %+v, want accepted profile interaction", res)
	}
}

func TestStoryStoreForwardRoundTripEditAndClonePostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	owner, sourceUser := createStoryTestUsers(t, ctx, pool)
	store := NewStoryStore(pool)
	ownerPeer := domain.Peer{Type: domain.PeerTypeUser, ID: owner.ID}
	source := domain.Peer{Type: domain.PeerTypeUser, ID: sourceUser.ID}
	created, err := store.CreateStory(ctx, domain.StoryCreateRequest{
		Owner:    ownerPeer,
		RandomID: 202,
		Date:     1700000600,
		Period:   86400,
		Public:   true,
		Forward: &domain.StoryForward{
			From:     source,
			StoryID:  7,
			Modified: true,
		},
	})
	if err != nil {
		t.Fatalf("create story: %v", err)
	}
	assertPGStoryForward(t, created.Story, source, 7, true)
	created.Story.Forward.From.ID = 9999
	created.Story.Forward.StoryID = 99

	list, err := store.GetStoriesByID(ctx, owner.ID, ownerPeer, []int{created.Story.ID}, 1700000601)
	if err != nil {
		t.Fatalf("get story by id: %v", err)
	}
	if len(list.Stories) != 1 {
		t.Fatalf("stories by id = %+v, want one story", list.Stories)
	}
	assertPGStoryForward(t, list.Stories[0], source, 7, true)

	edited, err := store.EditStory(ctx, domain.StoryEditRequest{
		Owner:         ownerPeer,
		ID:            created.Story.ID,
		UpdateCaption: true,
		Caption:       "edited repost caption",
	})
	if err != nil {
		t.Fatalf("edit story caption: %v", err)
	}
	assertPGStoryForward(t, edited.Story, source, 7, true)
	edited.Story.Forward.From.ID = 9998

	list, err = store.GetStoriesByID(ctx, owner.ID, ownerPeer, []int{created.Story.ID}, 1700000602)
	if err != nil {
		t.Fatalf("get edited story by id: %v", err)
	}
	assertPGStoryForward(t, list.Stories[0], source, 7, true)

	hidden, err := store.CreateStory(ctx, domain.StoryCreateRequest{
		Owner:    ownerPeer,
		RandomID: 203,
		Date:     1700000603,
		Period:   86400,
		Public:   true,
		Forward: &domain.StoryForward{
			FromName: "Alice Hidden",
			StoryID:  8,
		},
	})
	if err != nil {
		t.Fatalf("create hidden-author story: %v", err)
	}
	assertPGStoryForwardName(t, hidden.Story, "Alice Hidden", 8, false)
	hidden.Story.Forward.From = source
	hidden.Story.Forward.FromName = "mutated"
	hidden.Story.Forward.StoryID = 88

	list, err = store.GetStoriesByID(ctx, owner.ID, ownerPeer, []int{hidden.Story.ID}, 1700000604)
	if err != nil {
		t.Fatalf("get hidden-author story by id: %v", err)
	}
	if len(list.Stories) != 1 {
		t.Fatalf("hidden-author stories by id = %+v, want one story", list.Stories)
	}
	assertPGStoryForwardName(t, list.Stories[0], "Alice Hidden", 8, false)

	editedHidden, err := store.EditStory(ctx, domain.StoryEditRequest{
		Owner:         ownerPeer,
		ID:            hidden.Story.ID,
		UpdateCaption: true,
		Caption:       "edited hidden repost caption",
	})
	if err != nil {
		t.Fatalf("edit hidden-author story caption: %v", err)
	}
	assertPGStoryForwardName(t, editedHidden.Story, "Alice Hidden", 8, false)
}

func TestStoryStorePublicRepostForwardCountAndViewsListPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	sourceUser, repostUser := createStoryTestUsers(t, ctx, pool)
	store := NewStoryStore(pool)
	sourceOwner := domain.Peer{Type: domain.PeerTypeUser, ID: sourceUser.ID}
	repostOwner := domain.Peer{Type: domain.PeerTypeUser, ID: repostUser.ID}
	source, err := store.CreateStory(ctx, domain.StoryCreateRequest{
		Owner:    sourceOwner,
		RandomID: 301,
		Date:     1700000700,
		Period:   86400,
		Public:   true,
	})
	if err != nil {
		t.Fatalf("create source story: %v", err)
	}
	repost, err := store.CreateStory(ctx, domain.StoryCreateRequest{
		Owner:    repostOwner,
		RandomID: 302,
		Date:     1700000701,
		Period:   86400,
		Public:   true,
		Forward: &domain.StoryForward{
			From:    sourceOwner,
			StoryID: source.Story.ID,
		},
	})
	if err != nil {
		t.Fatalf("create repost story: %v", err)
	}
	sourceList, err := store.GetStoriesByID(ctx, sourceUser.ID, sourceOwner, []int{source.Story.ID}, 1700000702)
	if err != nil {
		t.Fatalf("get source story: %v", err)
	}
	if len(sourceList.Stories) != 1 || sourceList.Stories[0].Views.ForwardsCount != 1 {
		t.Fatalf("source story = %+v, want forwards_count=1", sourceList.Stories)
	}
	views, err := store.ListStoryViews(ctx, domain.StoryViewListRequest{
		ViewerUserID:  sourceUser.ID,
		Owner:         sourceOwner,
		StoryID:       source.Story.ID,
		Limit:         20,
		ForwardsFirst: true,
	})
	if err != nil {
		t.Fatalf("list story views: %v", err)
	}
	if views.Count != 1 || views.ForwardsCount != 1 || len(views.Views) != 1 || views.Views[0].Repost == nil {
		t.Fatalf("views list = %+v, want one repost interaction", views)
	}
	if views.Views[0].Repost.Owner != repostOwner || views.Views[0].Repost.ID != repost.Story.ID {
		t.Fatalf("repost interaction = %+v, want owner %+v story %d", views.Views[0].Repost, repostOwner, repost.Story.ID)
	}
	if _, err := store.DeleteStories(ctx, repostOwner, []int{repost.Story.ID}, 1700000703); err != nil {
		t.Fatalf("delete repost: %v", err)
	}
	sourceList, err = store.GetStoriesByID(ctx, sourceUser.ID, sourceOwner, []int{source.Story.ID}, 1700000703)
	if err != nil {
		t.Fatalf("get source story after delete: %v", err)
	}
	if len(sourceList.Stories) != 1 || sourceList.Stories[0].Views.ForwardsCount != 0 {
		t.Fatalf("source story after delete = %+v, want forwards_count=0", sourceList.Stories)
	}
}

func TestStoryStorePublicForwardListReturnsRepostsOnlyPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	sourceUser, repostUser := createStoryTestUsers(t, ctx, pool)
	store := NewStoryStore(pool)
	sourceOwner := domain.Peer{Type: domain.PeerTypeChannel, ID: sourceUser.ID}
	repostOwner := domain.Peer{Type: domain.PeerTypeChannel, ID: repostUser.ID}
	privateRepostOwner := domain.Peer{Type: domain.PeerTypeChannel, ID: repostUser.ID + 1000}
	source, err := store.CreateStory(ctx, domain.StoryCreateRequest{
		Owner:    sourceOwner,
		RandomID: 311,
		Date:     1700000710,
		Period:   86400,
		Public:   true,
	})
	if err != nil {
		t.Fatalf("create source story: %v", err)
	}
	repost, err := store.CreateStory(ctx, domain.StoryCreateRequest{
		Owner:    repostOwner,
		RandomID: 312,
		Date:     1700000711,
		Period:   86400,
		Public:   true,
		Forward: &domain.StoryForward{
			From:    sourceOwner,
			StoryID: source.Story.ID,
		},
	})
	if err != nil {
		t.Fatalf("create public repost: %v", err)
	}
	hiddenRepost, err := store.CreateStory(ctx, domain.StoryCreateRequest{
		Owner:    repostOwner,
		RandomID: 314,
		Date:     1700000713,
		Period:   86400,
		Public:   true,
		Forward: &domain.StoryForward{
			Source:   sourceOwner,
			FromName: "Hidden Source",
			StoryID:  source.Story.ID,
		},
	})
	if err != nil {
		t.Fatalf("create hidden-source public repost: %v", err)
	}
	if _, err := store.CreateStory(ctx, domain.StoryCreateRequest{
		Owner:    privateRepostOwner,
		RandomID: 313,
		Date:     1700000712,
		Period:   86400,
		Public:   false,
		Forward: &domain.StoryForward{
			From:    sourceOwner,
			StoryID: source.Story.ID,
		},
	}); err != nil {
		t.Fatalf("create private repost: %v", err)
	}
	list, err := store.ListStoryPublicForwards(ctx, domain.StoryPublicForwardListRequest{
		ViewerUserID: sourceUser.ID,
		Owner:        sourceOwner,
		StoryID:      source.Story.ID,
		Limit:        20,
	})
	if err != nil {
		t.Fatalf("list story public forwards: %v", err)
	}
	if list.Count != 2 || len(list.Forwards) != 2 || list.Forwards[0].Repost == nil || list.Forwards[1].Repost == nil {
		t.Fatalf("public forwards = %+v, want two public reposts", list)
	}
	if list.Forwards[0].Repost.ID != hiddenRepost.Story.ID ||
		list.Forwards[0].Repost.Forward == nil ||
		list.Forwards[0].Repost.Forward.From != (domain.Peer{}) ||
		list.Forwards[0].Repost.Forward.Source != sourceOwner ||
		list.Forwards[0].Repost.Forward.FromName != "Hidden Source" {
		t.Fatalf("hidden public forward repost = %+v, want from_name-only header with source %+v", list.Forwards[0].Repost, sourceOwner)
	}
	if list.Forwards[1].Repost.Owner != repostOwner || list.Forwards[1].Repost.ID != repost.Story.ID {
		t.Fatalf("public forward repost = %+v, want owner %+v story %d", list.Forwards[1].Repost, repostOwner, repost.Story.ID)
	}
	if _, err := store.DeleteStories(ctx, repostOwner, []int{repost.Story.ID}, 1700000714); err != nil {
		t.Fatalf("delete repost: %v", err)
	}
	afterVisibleDelete, err := store.ListStoryPublicForwards(ctx, domain.StoryPublicForwardListRequest{
		ViewerUserID: sourceUser.ID,
		Owner:        sourceOwner,
		StoryID:      source.Story.ID,
		Limit:        20,
	})
	if err != nil {
		t.Fatalf("list story public forwards after delete: %v", err)
	}
	if afterVisibleDelete.Count != 1 || len(afterVisibleDelete.Forwards) != 1 || afterVisibleDelete.Forwards[0].Repost == nil ||
		afterVisibleDelete.Forwards[0].Repost.ID != hiddenRepost.Story.ID {
		t.Fatalf("public forwards after visible delete = %+v, want hidden repost only", afterVisibleDelete)
	}
	if _, err := store.DeleteStories(ctx, repostOwner, []int{hiddenRepost.Story.ID}, 1700000715); err != nil {
		t.Fatalf("delete hidden repost: %v", err)
	}
	empty, err := store.ListStoryPublicForwards(ctx, domain.StoryPublicForwardListRequest{
		ViewerUserID: sourceUser.ID,
		Owner:        sourceOwner,
		StoryID:      source.Story.ID,
		Limit:        20,
	})
	if err != nil {
		t.Fatalf("list story public forwards after hidden delete: %v", err)
	}
	if empty.Count != 0 || len(empty.Forwards) != 0 {
		t.Fatalf("public forwards after delete = %+v, want empty", empty)
	}
}

func TestStoryStorePublicRepostStoryReactionsListPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	sourceUser, repostUser := createStoryTestUsers(t, ctx, pool)
	store := NewStoryStore(pool)
	channels := NewChannelStore(pool)
	createdChannel, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: sourceUser.ID,
		Title:         "Story Reactions " + randomSuffix(t),
		Broadcast:     true,
		MemberUserIDs: []int64{repostUser.ID},
		Date:          1700000790,
	})
	if err != nil {
		t.Fatalf("create source channel: %v", err)
	}
	sourceOwner := domain.Peer{Type: domain.PeerTypeChannel, ID: createdChannel.Channel.ID}
	t.Cleanup(func() {
		cleanupChannelStoryTestRows(t, context.Background(), pool, sourceOwner.ID, []int64{sourceUser.ID, repostUser.ID})
	})
	repostOwner := domain.Peer{Type: domain.PeerTypeUser, ID: repostUser.ID}
	source, err := store.CreateStory(ctx, domain.StoryCreateRequest{
		Owner:    sourceOwner,
		RandomID: 401,
		Date:     1700000800,
		Period:   86400,
		Public:   true,
	})
	if err != nil {
		t.Fatalf("create source story: %v", err)
	}
	if _, err := store.SetReaction(ctx, repostUser.ID, sourceOwner, source.Story.ID, &domain.MessageReaction{
		Type:     domain.MessageReactionEmoji,
		Emoticon: "🔥",
	}, 1700000801); err != nil {
		t.Fatalf("set reaction: %v", err)
	}
	repost, err := store.CreateStory(ctx, domain.StoryCreateRequest{
		Owner:    repostOwner,
		RandomID: 402,
		Date:     1700000802,
		Period:   86400,
		Public:   true,
		Forward: &domain.StoryForward{
			From:    sourceOwner,
			StoryID: source.Story.ID,
		},
	})
	if err != nil {
		t.Fatalf("create repost story: %v", err)
	}
	list, err := store.ListStoryReactions(ctx, domain.StoryReactionListRequest{
		ViewerUserID:  sourceUser.ID,
		Owner:         sourceOwner,
		StoryID:       source.Story.ID,
		Limit:         20,
		ForwardsFirst: true,
	})
	if err != nil {
		t.Fatalf("list story reactions: %v", err)
	}
	if list.Count != 2 || len(list.Reactions) != 2 || list.Reactions[0].Repost == nil {
		t.Fatalf("reaction list = %+v, want repost first plus reaction", list)
	}
	if list.Reactions[0].Repost.Owner != repostOwner || list.Reactions[0].Repost.ID != repost.Story.ID {
		t.Fatalf("repost reaction = %+v, want owner %+v story %d", list.Reactions[0].Repost, repostOwner, repost.Story.ID)
	}
	filtered, err := store.ListStoryReactions(ctx, domain.StoryReactionListRequest{
		ViewerUserID: sourceUser.ID,
		Owner:        sourceOwner,
		StoryID:      source.Story.ID,
		Limit:        20,
		Reaction: &domain.MessageReaction{
			Type:     domain.MessageReactionEmoji,
			Emoticon: "🔥",
		},
	})
	if err != nil {
		t.Fatalf("list filtered story reactions: %v", err)
	}
	if filtered.Count != 1 || len(filtered.Reactions) != 1 || filtered.Reactions[0].Repost != nil || filtered.Reactions[0].ViewerID != repostUser.ID {
		t.Fatalf("filtered reaction list = %+v, want only emoji reactor", filtered)
	}
	if _, err := store.DeleteStories(ctx, repostOwner, []int{repost.Story.ID}, 1700000803); err != nil {
		t.Fatalf("delete repost: %v", err)
	}
	list, err = store.ListStoryReactions(ctx, domain.StoryReactionListRequest{
		ViewerUserID:  sourceUser.ID,
		Owner:         sourceOwner,
		StoryID:       source.Story.ID,
		Limit:         20,
		ForwardsFirst: true,
	})
	if err != nil {
		t.Fatalf("list story reactions after delete: %v", err)
	}
	if list.Count != 1 || len(list.Reactions) != 1 || list.Reactions[0].Repost != nil {
		t.Fatalf("reaction list after delete = %+v, want only durable reaction", list)
	}
}

func testPGStoryMediaArea(emoticon string, x float64) domain.StoryMediaArea {
	return domain.StoryMediaArea{
		Kind: domain.StoryMediaAreaSuggestedReaction,
		Coordinates: domain.StoryMediaAreaCoordinates{
			X:         x,
			Y:         10,
			W:         10,
			H:         10,
			Rotation:  15,
			Radius:    8,
			HasRadius: true,
		},
		Dark:     true,
		Flipped:  true,
		Reaction: &domain.MessageReaction{Type: domain.MessageReactionEmoji, Emoticon: emoticon},
	}
}

func assertPGStoryMediaArea(t *testing.T, story domain.Story, wantEmoticon string, wantX float64) {
	t.Helper()
	if len(story.MediaAreas) != 1 {
		t.Fatalf("story media areas = %+v, want one area", story.MediaAreas)
	}
	area := story.MediaAreas[0]
	if area.Kind != domain.StoryMediaAreaSuggestedReaction || area.Reaction == nil {
		t.Fatalf("story media area = %+v, want suggested reaction", area)
	}
	if area.Reaction.Type != domain.MessageReactionEmoji || area.Reaction.Emoticon != wantEmoticon {
		t.Fatalf("story media area reaction = %+v, want emoji %q", area.Reaction, wantEmoticon)
	}
	if area.Coordinates.X != wantX || area.Coordinates.Y != 10 || area.Coordinates.W != 10 || area.Coordinates.H != 10 ||
		area.Coordinates.Rotation != 15 || !area.Coordinates.HasRadius || area.Coordinates.Radius != 8 {
		t.Fatalf("story media area coordinates = %+v, want x %v y/w/h 10 rotation 15 radius 8", area.Coordinates, wantX)
	}
	if !area.Dark || !area.Flipped {
		t.Fatalf("story media area flags = dark %v flipped %v, want true/true", area.Dark, area.Flipped)
	}
}

func testPGStoryURLMediaArea(url string, x float64) domain.StoryMediaArea {
	return domain.StoryMediaArea{
		Kind: domain.StoryMediaAreaURL,
		Coordinates: domain.StoryMediaAreaCoordinates{
			X:        x,
			Y:        10,
			W:        20,
			H:        10,
			Rotation: 15,
		},
		URL: url,
	}
}

func assertPGStoryURLMediaArea(t *testing.T, story domain.Story, wantURL string, wantX float64) {
	t.Helper()
	if len(story.MediaAreas) != 1 {
		t.Fatalf("story media areas = %+v, want one url area", story.MediaAreas)
	}
	area := story.MediaAreas[0]
	if area.Kind != domain.StoryMediaAreaURL || area.URL != wantURL {
		t.Fatalf("story media area = %+v, want url %q", area, wantURL)
	}
	if area.Coordinates.X != wantX || area.Coordinates.Y != 10 || area.Coordinates.W != 20 || area.Coordinates.H != 10 ||
		area.Coordinates.Rotation != 15 {
		t.Fatalf("story media area coordinates = %+v, want x %v y 10 w 20 h 10 rotation 15", area.Coordinates, wantX)
	}
}

func testPGStoryGeoPointMediaArea(lat, long, x float64) domain.StoryMediaArea {
	return domain.StoryMediaArea{
		Kind: domain.StoryMediaAreaGeoPoint,
		Coordinates: domain.StoryMediaAreaCoordinates{
			X:        x,
			Y:        12,
			W:        22,
			H:        11,
			Rotation: 20,
		},
		Geo: &domain.MessageGeoPoint{
			Lat:        lat,
			Long:       long,
			AccessHash: 123456,
		},
		GeoAddress: &domain.StoryGeoPointAddress{
			CountryISO2: "CN",
			State:       "Shanghai",
			City:        "Shanghai",
			Street:      "People Square",
		},
	}
}

func assertPGStoryGeoPointMediaArea(t *testing.T, story domain.Story, wantLat, wantLong, wantX float64) {
	t.Helper()
	if len(story.MediaAreas) != 1 {
		t.Fatalf("story media areas = %+v, want one geo area", story.MediaAreas)
	}
	area := story.MediaAreas[0]
	if area.Kind != domain.StoryMediaAreaGeoPoint || area.Geo == nil {
		t.Fatalf("story media area = %+v, want geo point", area)
	}
	if area.Geo.Lat != wantLat || area.Geo.Long != wantLong || area.Geo.AccessHash != 123456 {
		t.Fatalf("story media area geo = %+v, want lat %v long %v access_hash 123456", area.Geo, wantLat, wantLong)
	}
	if area.GeoAddress == nil || area.GeoAddress.CountryISO2 != "CN" || area.GeoAddress.State != "Shanghai" || area.GeoAddress.City != "Shanghai" || area.GeoAddress.Street != "People Square" {
		t.Fatalf("story media area address = %+v, want CN/Shanghai/Shanghai/People Square", area.GeoAddress)
	}
	if area.Coordinates.X != wantX || area.Coordinates.Y != 12 || area.Coordinates.W != 22 || area.Coordinates.H != 11 ||
		area.Coordinates.Rotation != 20 {
		t.Fatalf("story media area coordinates = %+v, want x %v y 12 w 22 h 11 rotation 20", area.Coordinates, wantX)
	}
}

func testPGStoryVenueMediaArea(title string, lat, long, x float64) domain.StoryMediaArea {
	venue := &domain.MessageVenue{
		Geo:       domain.MessageGeoPoint{Lat: lat, Long: long, AccessHash: 123456},
		Title:     title,
		Address:   "Inline Street",
		Provider:  "gplaces",
		VenueID:   "venue-id",
		VenueType: "cafe",
	}
	return domain.StoryMediaArea{
		Kind: domain.StoryMediaAreaVenue,
		Coordinates: domain.StoryMediaAreaCoordinates{
			X:        x,
			Y:        13,
			W:        23,
			H:        12,
			Rotation: 22,
		},
		Geo:   &venue.Geo,
		Venue: venue,
	}
}

func assertPGStoryVenueMediaArea(t *testing.T, story domain.Story, wantTitle string, wantLat, wantLong, wantX float64) {
	t.Helper()
	if len(story.MediaAreas) != 1 {
		t.Fatalf("story media areas = %+v, want one venue area", story.MediaAreas)
	}
	area := story.MediaAreas[0]
	if area.Kind != domain.StoryMediaAreaVenue || area.Venue == nil || area.Geo == nil {
		t.Fatalf("story media area = %+v, want venue", area)
	}
	if area.Venue.Title != wantTitle || area.Venue.Address != "Inline Street" || area.Venue.Provider != "gplaces" || area.Venue.VenueID != "venue-id" || area.Venue.VenueType != "cafe" {
		t.Fatalf("story venue = %+v, want %q/Inline Street/gplaces/venue-id/cafe", area.Venue, wantTitle)
	}
	if area.Venue.Geo.Lat != wantLat || area.Venue.Geo.Long != wantLong || area.Venue.Geo.AccessHash != 123456 ||
		area.Geo.Lat != wantLat || area.Geo.Long != wantLong || area.Geo.AccessHash != 123456 {
		t.Fatalf("story venue geo = venue %+v area %+v, want lat %v long %v access_hash 123456", area.Venue.Geo, area.Geo, wantLat, wantLong)
	}
	if area.Coordinates.X != wantX || area.Coordinates.Y != 13 || area.Coordinates.W != 23 || area.Coordinates.H != 12 ||
		area.Coordinates.Rotation != 22 {
		t.Fatalf("story media area coordinates = %+v, want x %v y 13 w 23 h 12 rotation 22", area.Coordinates, wantX)
	}
}

func testPGStoryWeatherMediaArea(emoji string, temperatureC float64, color int, x float64) domain.StoryMediaArea {
	return domain.StoryMediaArea{
		Kind: domain.StoryMediaAreaWeather,
		Coordinates: domain.StoryMediaAreaCoordinates{
			X:        x,
			Y:        14,
			W:        24,
			H:        12,
			Rotation: 25,
		},
		WeatherEmoji: emoji,
		TemperatureC: temperatureC,
		Color:        color,
	}
}

func assertPGStoryWeatherMediaArea(t *testing.T, story domain.Story, wantEmoji string, wantTemperatureC float64, wantColor int, wantX float64) {
	t.Helper()
	if len(story.MediaAreas) != 1 {
		t.Fatalf("story media areas = %+v, want one weather area", story.MediaAreas)
	}
	area := story.MediaAreas[0]
	if area.Kind != domain.StoryMediaAreaWeather {
		t.Fatalf("story media area = %+v, want weather", area)
	}
	if area.WeatherEmoji != wantEmoji || area.TemperatureC != wantTemperatureC || area.Color != wantColor {
		t.Fatalf("story weather area = emoji %q temp %v color %d, want %q/%v/%d", area.WeatherEmoji, area.TemperatureC, area.Color, wantEmoji, wantTemperatureC, wantColor)
	}
	if area.Coordinates.X != wantX || area.Coordinates.Y != 14 || area.Coordinates.W != 24 || area.Coordinates.H != 12 ||
		area.Coordinates.Rotation != 25 {
		t.Fatalf("story media area coordinates = %+v, want x %v y 14 w 24 h 12 rotation 25", area.Coordinates, wantX)
	}
}

func testPGStoryChannelPostMediaArea(channelID int64, msgID int, x float64) domain.StoryMediaArea {
	return domain.StoryMediaArea{
		Kind: domain.StoryMediaAreaChannelPost,
		Coordinates: domain.StoryMediaAreaCoordinates{
			X:        x,
			Y:        15,
			W:        25,
			H:        12,
			Rotation: 27,
		},
		ChannelID: channelID,
		MsgID:     msgID,
	}
}

func assertPGStoryChannelPostMediaArea(t *testing.T, story domain.Story, wantChannelID int64, wantMsgID int, wantX float64) {
	t.Helper()
	if len(story.MediaAreas) != 1 {
		t.Fatalf("story media areas = %+v, want one channel post area", story.MediaAreas)
	}
	area := story.MediaAreas[0]
	if area.Kind != domain.StoryMediaAreaChannelPost || area.ChannelID != wantChannelID || area.MsgID != wantMsgID {
		t.Fatalf("story channel post area = %+v, want channel %d msg %d", area, wantChannelID, wantMsgID)
	}
	if area.Coordinates.X != wantX || area.Coordinates.Y != 15 || area.Coordinates.W != 25 || area.Coordinates.H != 12 ||
		area.Coordinates.Rotation != 27 {
		t.Fatalf("story media area coordinates = %+v, want x %v y 15 w 25 h 12 rotation 27", area.Coordinates, wantX)
	}
}

func testPGStoryStarGiftMediaArea(slug string, x float64) domain.StoryMediaArea {
	return domain.StoryMediaArea{
		Kind: domain.StoryMediaAreaStarGift,
		Coordinates: domain.StoryMediaAreaCoordinates{
			X:        x,
			Y:        16,
			W:        26,
			H:        12,
			Rotation: 29,
		},
		StarGiftSlug: slug,
	}
}

func assertPGStoryStarGiftMediaArea(t *testing.T, story domain.Story, wantSlug string, wantX float64) {
	t.Helper()
	if len(story.MediaAreas) != 1 {
		t.Fatalf("story media areas = %+v, want one star gift area", story.MediaAreas)
	}
	area := story.MediaAreas[0]
	if area.Kind != domain.StoryMediaAreaStarGift || area.StarGiftSlug != wantSlug {
		t.Fatalf("story star gift area = %+v, want slug %q", area, wantSlug)
	}
	if area.Coordinates.X != wantX || area.Coordinates.Y != 16 || area.Coordinates.W != 26 || area.Coordinates.H != 12 ||
		area.Coordinates.Rotation != 29 {
		t.Fatalf("story media area coordinates = %+v, want x %v y 16 w 26 h 12 rotation 29", area.Coordinates, wantX)
	}
}

func assertPGStoryForward(t *testing.T, story domain.Story, wantSource domain.Peer, wantStoryID int, wantModified bool) {
	t.Helper()
	if story.Forward == nil {
		t.Fatalf("story forward is nil, want source %+v story %d", wantSource, wantStoryID)
	}
	if story.Forward.From != wantSource || story.Forward.StoryID != wantStoryID || story.Forward.Modified != wantModified {
		t.Fatalf("story forward = %+v, want source %+v story %d modified %v", story.Forward, wantSource, wantStoryID, wantModified)
	}
}

func assertPGStoryForwardName(t *testing.T, story domain.Story, wantName string, wantStoryID int, wantModified bool) {
	t.Helper()
	if story.Forward == nil {
		t.Fatalf("story forward is nil, want from_name %q story %d", wantName, wantStoryID)
	}
	if story.Forward.From != (domain.Peer{}) || story.Forward.FromName != wantName ||
		story.Forward.StoryID != wantStoryID || story.Forward.Modified != wantModified {
		t.Fatalf("story forward = %+v, want from_name %q story %d modified %v", story.Forward, wantName, wantStoryID, wantModified)
	}
}

func TestStoryStoreViewListFiltersPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	owner, contactViewer := createStoryTestUsers(t, ctx, pool)
	users := NewUserStore(pool)
	stranger, err := users.Create(ctx, domain.User{AccessHash: 8103, Phone: "+1771" + randomSuffix(t) + "03", FirstName: "Bob", LastName: "Stranger"})
	if err != nil {
		t.Fatalf("create stranger: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, stranger.ID); err != nil {
			t.Fatalf("cleanup stranger: %v", err)
		}
	})
	store := NewStoryStore(pool)
	ownerPeer := domain.Peer{Type: domain.PeerTypeUser, ID: owner.ID}
	if _, err := store.UpsertStory(ctx, domain.UpsertStoryRequest{Story: domain.Story{
		Owner:      ownerPeer,
		ID:         1,
		Date:       1700000400,
		ExpireDate: 1700001000,
		Public:     true,
	}}); err != nil {
		t.Fatalf("upsert story: %v", err)
	}
	if _, err := store.IncrementViews(ctx, contactViewer.ID, ownerPeer, []int{1}, 1700000401); err != nil {
		t.Fatalf("increment contact view: %v", err)
	}
	if _, err := store.IncrementViews(ctx, stranger.ID, ownerPeer, []int{1}, 1700000402); err != nil {
		t.Fatalf("increment stranger view: %v", err)
	}
	viewerIDs, err := store.ListStoryViewerIDs(ctx, ownerPeer, 1, 10)
	if err != nil {
		t.Fatalf("list story viewer ids: %v", err)
	}
	if len(viewerIDs) != 2 || viewerIDs[0] != contactViewer.ID || viewerIDs[1] != stranger.ID {
		t.Fatalf("viewer ids = %v, want contact and stranger ascending", viewerIDs)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO contacts (user_id, contact_user_id, mutual, contact_first_name, contact_last_name, contact_phone)
VALUES ($1, $2, false, 'Close', 'Friend', '7001')
ON CONFLICT (user_id, contact_user_id) DO UPDATE SET
  contact_first_name = EXCLUDED.contact_first_name,
  contact_last_name = EXCLUDED.contact_last_name,
  contact_phone = EXCLUDED.contact_phone`, owner.ID, contactViewer.ID); err != nil {
		t.Fatalf("insert contact: %v", err)
	}

	contactsOnly, err := store.ListStoryViews(ctx, domain.StoryViewListRequest{
		ViewerUserID: owner.ID,
		Owner:        ownerPeer,
		StoryID:      1,
		Limit:        10,
		JustContacts: true,
	})
	if err != nil {
		t.Fatalf("list contacts-only: %v", err)
	}
	if contactsOnly.Count != 1 || contactsOnly.ViewsCount != 2 || len(contactsOnly.Views) != 1 || contactsOnly.Views[0].ViewerID != contactViewer.ID {
		t.Fatalf("contacts-only = %+v, want one contact viewer and total views 2", contactsOnly)
	}

	remark, err := store.ListStoryViews(ctx, domain.StoryViewListRequest{
		ViewerUserID: owner.ID,
		Owner:        ownerPeer,
		StoryID:      1,
		Limit:        10,
		Query:        "close",
	})
	if err != nil {
		t.Fatalf("list remark query: %v", err)
	}
	if remark.Count != 1 || len(remark.Views) != 1 || remark.Views[0].ViewerID != contactViewer.ID {
		t.Fatalf("remark query = %+v, want contact viewer", remark)
	}

	strangerQuery, err := store.ListStoryViews(ctx, domain.StoryViewListRequest{
		ViewerUserID: owner.ID,
		Owner:        ownerPeer,
		StoryID:      1,
		Limit:        10,
		Query:        "bob",
	})
	if err != nil {
		t.Fatalf("list stranger query: %v", err)
	}
	if strangerQuery.Count != 1 || len(strangerQuery.Views) != 1 || strangerQuery.Views[0].ViewerID != stranger.ID {
		t.Fatalf("stranger query = %+v, want stranger viewer", strangerQuery)
	}

	intersection, err := store.ListStoryViews(ctx, domain.StoryViewListRequest{
		ViewerUserID: owner.ID,
		Owner:        ownerPeer,
		StoryID:      1,
		Limit:        10,
		Query:        "bob",
		JustContacts: true,
	})
	if err != nil {
		t.Fatalf("list query contacts intersection: %v", err)
	}
	if intersection.Count != 0 || len(intersection.Views) != 0 {
		t.Fatalf("intersection = %+v, want empty contact-filtered bob result", intersection)
	}
	if _, err := store.DeleteStories(ctx, ownerPeer, []int{1}, 1700000403); err != nil {
		t.Fatalf("delete story: %v", err)
	}
	deletedViewerIDs, err := store.ListStoryViewerIDs(ctx, ownerPeer, 1, 10)
	if err != nil {
		t.Fatalf("list deleted story viewer ids: %v", err)
	}
	if len(deletedViewerIDs) != 2 || deletedViewerIDs[0] != contactViewer.ID || deletedViewerIDs[1] != stranger.ID {
		t.Fatalf("deleted story viewer ids = %v, want contact and stranger ascending", deletedViewerIDs)
	}
}

func TestStoryStoreViewerIDsIncludeCachedStoryExposurePostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	owner, _ := createStoryTestUsers(t, ctx, pool)
	store := NewStoryStore(pool)
	ownerPeer := domain.Peer{Type: domain.PeerTypeUser, ID: owner.ID}
	if _, err := store.UpsertStory(ctx, domain.UpsertStoryRequest{Story: domain.Story{
		Owner:      ownerPeer,
		ID:         1,
		Date:       1700000900,
		ExpireDate: 1700001900,
		Public:     true,
	}}); err != nil {
		t.Fatalf("upsert story: %v", err)
	}

	viewerOne := owner.ID + 100001
	viewerTwo := owner.ID + 100002
	viewerThree := owner.ID + 100003
	list, err := store.ListActiveStories(ctx, viewerOne, false, 1700000910, 10)
	if err != nil {
		t.Fatalf("list active stories: %v", err)
	}
	if len(list.Stories) != 1 {
		t.Fatalf("active stories = %d, want 1", len(list.Stories))
	}
	peerStories, err := store.GetPeerStories(ctx, viewerTwo, ownerPeer, 1700000911)
	if err != nil {
		t.Fatalf("get peer stories: %v", err)
	}
	if len(peerStories.Stories) != 1 {
		t.Fatalf("peer stories = %d, want 1", len(peerStories.Stories))
	}
	exact, err := store.GetStoriesByID(ctx, viewerThree, ownerPeer, []int{1}, 1700000912)
	if err != nil {
		t.Fatalf("get stories by id: %v", err)
	}
	if len(exact.Stories) != 1 {
		t.Fatalf("exact stories = %d, want 1", len(exact.Stories))
	}

	viewerIDs, err := store.ListStoryViewerIDs(ctx, ownerPeer, 1, 10)
	if err != nil {
		t.Fatalf("list story viewer ids: %v", err)
	}
	want := []int64{viewerOne, viewerTwo, viewerThree}
	if len(viewerIDs) != len(want) {
		t.Fatalf("viewer ids = %v, want %v", viewerIDs, want)
	}
	for i := range want {
		if viewerIDs[i] != want[i] {
			t.Fatalf("viewer ids = %v, want %v", viewerIDs, want)
		}
	}
	views, err := store.ListStoryViews(ctx, domain.StoryViewListRequest{
		ViewerUserID: owner.ID,
		Owner:        ownerPeer,
		StoryID:      1,
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("list story views: %v", err)
	}
	if views.Count != 0 || views.ViewsCount != 0 || len(views.Views) != 0 {
		t.Fatalf("story views = %+v, want exposure without view counters", views)
	}
}

func TestStoryStorePrivacyVisibilityPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	owner, selectedViewer := createStoryTestUsers(t, ctx, pool)
	users := NewUserStore(pool)
	suffix := randomSuffix(t)
	stranger, err := users.Create(ctx, domain.User{AccessHash: 8203, Phone: "+1772" + suffix + "03", FirstName: "Stranger"})
	if err != nil {
		t.Fatalf("create stranger: %v", err)
	}
	contactViewer, err := users.Create(ctx, domain.User{AccessHash: 8204, Phone: "+1772" + suffix + "04", FirstName: "Contact"})
	if err != nil {
		t.Fatalf("create contact viewer: %v", err)
	}
	excludedContact, err := users.Create(ctx, domain.User{AccessHash: 8205, Phone: "+1772" + suffix + "05", FirstName: "Excluded"})
	if err != nil {
		t.Fatalf("create excluded contact: %v", err)
	}
	closeFriendViewer, err := users.Create(ctx, domain.User{AccessHash: 8206, Phone: "+1772" + suffix + "06", FirstName: "Close"})
	if err != nil {
		t.Fatalf("create close friend viewer: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `
DELETE FROM contacts
WHERE user_id = $1
   OR contact_user_id = ANY($2::bigint[])`, owner.ID, []int64{stranger.ID, contactViewer.ID, excludedContact.ID, closeFriendViewer.ID}); err != nil {
			t.Fatalf("cleanup contacts: %v", err)
		}
		if _, err := pool.Exec(context.Background(), `
DELETE FROM users
WHERE id = ANY($1::bigint[])`, []int64{stranger.ID, contactViewer.ID, excludedContact.ID, closeFriendViewer.ID}); err != nil {
			t.Fatalf("cleanup extra users: %v", err)
		}
	})
	if _, err := pool.Exec(ctx, `
INSERT INTO contacts (user_id, contact_user_id, mutual, close_friend, contact_first_name, contact_last_name, contact_phone)
VALUES ($1, $2, false, false, 'Contact', 'Viewer', '7004'),
       ($1, $3, false, false, 'Excluded', 'Contact', '7005'),
       ($1, $4, false, true, 'Close', 'Friend', '7006')
ON CONFLICT (user_id, contact_user_id) DO UPDATE SET
  close_friend = EXCLUDED.close_friend,
  contact_first_name = EXCLUDED.contact_first_name,
  contact_last_name = EXCLUDED.contact_last_name,
  contact_phone = EXCLUDED.contact_phone`, owner.ID, contactViewer.ID, excludedContact.ID, closeFriendViewer.ID); err != nil {
		t.Fatalf("insert contacts: %v", err)
	}

	store := NewStoryStore(pool)
	ownerPeer := domain.Peer{Type: domain.PeerTypeUser, ID: owner.ID}
	stories := []domain.Story{
		{
			Owner:           ownerPeer,
			ID:              1,
			Date:            1700000701,
			ExpireDate:      1700009000,
			Public:          true,
			DisallowUserIDs: []int64{stranger.ID},
			PrivacyRules: []domain.PrivacyRule{
				{Kind: domain.PrivacyRuleAllowAll},
				{Kind: domain.PrivacyRuleDisallowUsers, UserIDs: []int64{stranger.ID}},
			},
		},
		{
			Owner:            ownerPeer,
			ID:               2,
			Date:             1700000702,
			ExpireDate:       1700009000,
			SelectedContacts: true,
			AllowUserIDs:     []int64{selectedViewer.ID},
			PrivacyRules: []domain.PrivacyRule{
				{Kind: domain.PrivacyRuleAllowUsers, UserIDs: []int64{selectedViewer.ID}},
			},
		},
		{
			Owner:           ownerPeer,
			ID:              3,
			Date:            1700000703,
			ExpireDate:      1700009000,
			Contacts:        true,
			DisallowUserIDs: []int64{excludedContact.ID},
			PrivacyRules: []domain.PrivacyRule{
				{Kind: domain.PrivacyRuleAllowContacts},
				{Kind: domain.PrivacyRuleDisallowUsers, UserIDs: []int64{excludedContact.ID}},
			},
		},
		{
			Owner:        ownerPeer,
			ID:           4,
			Date:         1700000704,
			ExpireDate:   1700009000,
			CloseFriends: true,
			PrivacyRules: []domain.PrivacyRule{
				{Kind: domain.PrivacyRuleAllowCloseFriends},
			},
		},
	}
	for _, story := range stories {
		if _, err := store.UpsertStory(ctx, domain.UpsertStoryRequest{Story: story}); err != nil {
			t.Fatalf("upsert story %d: %v", story.ID, err)
		}
	}

	afterRestart := NewStoryStore(pool)
	selected, err := afterRestart.GetStoriesByID(ctx, selectedViewer.ID, ownerPeer, []int{1, 2, 3, 4}, 1700000800)
	if err != nil {
		t.Fatalf("selected viewer get stories: %v", err)
	}
	if gotIDs := pgStoryIDs(selected.Stories); !samePGStoryIDs(gotIDs, []int{1, 2}) {
		t.Fatalf("selected viewer story ids = %v, want [1 2]", gotIDs)
	}
	if len(selected.Stories[1].PrivacyRules) != 1 || selected.Stories[1].AllowUserIDs[0] != selectedViewer.ID {
		t.Fatalf("selected story privacy = %+v allow=%v, want persisted allow user", selected.Stories[1].PrivacyRules, selected.Stories[1].AllowUserIDs)
	}
	hidden, err := afterRestart.GetStoriesByID(ctx, stranger.ID, ownerPeer, []int{1, 2, 3, 4}, 1700000800)
	if err != nil {
		t.Fatalf("stranger get stories: %v", err)
	}
	if len(hidden.Stories) != 0 {
		t.Fatalf("stranger stories = %+v, want none", hidden.Stories)
	}
	contacts, err := afterRestart.GetStoriesByID(ctx, contactViewer.ID, ownerPeer, []int{1, 2, 3, 4}, 1700000800)
	if err != nil {
		t.Fatalf("contact get stories: %v", err)
	}
	if gotIDs := pgStoryIDs(contacts.Stories); !samePGStoryIDs(gotIDs, []int{1, 3}) {
		t.Fatalf("contact story ids = %v, want [1 3]", gotIDs)
	}
	excluded, err := afterRestart.GetStoriesByID(ctx, excludedContact.ID, ownerPeer, []int{3}, 1700000800)
	if err != nil {
		t.Fatalf("excluded contact get stories: %v", err)
	}
	if len(excluded.Stories) != 0 {
		t.Fatalf("excluded contact stories = %+v, want none", excluded.Stories)
	}
	closeFriend, err := afterRestart.GetStoriesByID(ctx, closeFriendViewer.ID, ownerPeer, []int{1, 2, 3, 4}, 1700000800)
	if err != nil {
		t.Fatalf("close friend get stories: %v", err)
	}
	if gotIDs := pgStoryIDs(closeFriend.Stories); !samePGStoryIDs(gotIDs, []int{1, 3, 4}) {
		t.Fatalf("close friend story ids = %v, want [1 3 4]", gotIDs)
	}
	if created, err := afterRestart.IncrementViews(ctx, stranger.ID, ownerPeer, []int{2}, 1700000801); err != nil || created != 0 {
		t.Fatalf("hidden increment views = %d, %v; want 0 nil", created, err)
	}
	if _, err := afterRestart.SetReaction(ctx, stranger.ID, ownerPeer, 2, &domain.MessageReaction{Type: domain.MessageReactionEmoji, Emoticon: "like"}, 1700000802); err != domain.ErrStoryNotFound {
		t.Fatalf("hidden set reaction err = %v, want ErrStoryNotFound", err)
	}
	recent, err := afterRestart.GetPeerMaxIDs(ctx, contactViewer.ID, []domain.Peer{ownerPeer}, 1700000803)
	if err != nil {
		t.Fatalf("contact peer max ids: %v", err)
	}
	if len(recent) != 1 || recent[0].MaxID != 3 {
		t.Fatalf("contact recent = %+v, want max id 3", recent)
	}
	closeFriendRecent, err := afterRestart.GetPeerMaxIDs(ctx, closeFriendViewer.ID, []domain.Peer{ownerPeer}, 1700000803)
	if err != nil {
		t.Fatalf("close friend peer max ids: %v", err)
	}
	if len(closeFriendRecent) != 1 || closeFriendRecent[0].MaxID != 4 {
		t.Fatalf("close friend recent = %+v, want max id 4", closeFriendRecent)
	}
}

func TestStoryStoreCreateEditDeleteAndPinnedPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	owner, _ := createStoryTestUsers(t, ctx, pool)
	store := NewStoryStore(pool)
	ownerPeer := domain.Peer{Type: domain.PeerTypeUser, ID: owner.ID}
	media := &domain.MessageMedia{Kind: domain.MessageMediaKindPhoto, Photo: &domain.Photo{ID: 9701, AccessHash: 97, DCID: 2}}

	created, err := store.CreateStory(ctx, domain.StoryCreateRequest{
		Owner:    ownerPeer,
		RandomID: 99001,
		Date:     1700000600,
		Period:   86400,
		Public:   true,
		Pinned:   true,
		Caption:  "pg first",
		Media:    media,
	})
	if err != nil {
		t.Fatalf("create story: %v", err)
	}
	if created.Duplicate || created.Story.ID != 1 || created.Story.ExpireDate != 1700087000 {
		t.Fatalf("created = %+v, want new story id 1", created)
	}
	dup, err := store.CreateStory(ctx, domain.StoryCreateRequest{
		Owner:    ownerPeer,
		RandomID: 99001,
		Date:     1700000601,
		Period:   86400,
		Public:   true,
		Caption:  "duplicate",
		Media:    media,
	})
	if err != nil {
		t.Fatalf("create duplicate: %v", err)
	}
	if !dup.Duplicate || dup.Story.ID != created.Story.ID || dup.Story.Caption != "pg first" {
		t.Fatalf("duplicate = %+v, want original story", dup)
	}
	selfStories, err := store.GetPeerStories(ctx, owner.ID, ownerPeer, 1700000602)
	if err != nil {
		t.Fatalf("get self stories: %v", err)
	}
	if selfStories.MaxReadID != created.Story.ID {
		t.Fatalf("self max read = %d, want story id", selfStories.MaxReadID)
	}
	exact, err := store.GetStoriesByID(ctx, owner.ID, ownerPeer, []int{created.Story.ID, created.Story.ID}, 1700000602)
	if err != nil {
		t.Fatalf("get stories by duplicate ids: %v", err)
	}
	if len(exact.Stories) != 1 || exact.Stories[0].ID != created.Story.ID {
		t.Fatalf("exact stories = %+v, want one deduped story", exact.Stories)
	}
	if _, err := store.GetStoriesByID(ctx, owner.ID, ownerPeer, nil, 1700000602); !errors.Is(err, domain.ErrStoryIDInvalid) {
		t.Fatalf("empty get stories by id err = %v, want ErrStoryIDInvalid", err)
	}

	edited, err := store.EditStory(ctx, domain.StoryEditRequest{
		Owner:         ownerPeer,
		ID:            created.Story.ID,
		Caption:       "pg edited",
		UpdateCaption: true,
	})
	if err != nil {
		t.Fatalf("edit story: %v", err)
	}
	if edited.Previous.Caption != "pg first" || edited.Previous.Edited {
		t.Fatalf("previous = %+v, want pre-edit story", edited.Previous)
	}
	if !edited.Story.Edited || edited.Story.Caption != "pg edited" {
		t.Fatalf("edited = %+v, want edited caption", edited)
	}
	pinned, err := store.ListPinnedStories(ctx, owner.ID, ownerPeer, 0, 10, 1700000603)
	if err != nil {
		t.Fatalf("list pinned: %v", err)
	}
	if len(pinned.Stories) != 1 || pinned.Stories[0].ID != created.Story.ID {
		t.Fatalf("pinned = %+v, want created story", pinned)
	}
	toggled, err := store.TogglePinned(ctx, ownerPeer, []int{created.Story.ID, created.Story.ID}, false, 1700000604)
	if err != nil {
		t.Fatalf("toggle pinned: %v", err)
	}
	if len(toggled.IDs) != 1 || toggled.IDs[0] != created.Story.ID || len(toggled.Stories) != 1 || toggled.Stories[0].Pinned {
		t.Fatalf("toggled = %+v, want unpinned story", toggled)
	}
	if len(toggled.Previous) != 1 || toggled.Previous[0].Deleted || !toggled.Previous[0].Pinned {
		t.Fatalf("toggled previous = %+v, want pre-toggle pinned snapshot", toggled.Previous)
	}
	retryToggled, err := store.TogglePinned(ctx, ownerPeer, []int{created.Story.ID}, false, 1700000605)
	if err != nil {
		t.Fatalf("retry toggle pinned: %v", err)
	}
	if len(retryToggled.IDs) != 1 || retryToggled.IDs[0] != created.Story.ID || len(retryToggled.Stories) != 0 || len(retryToggled.Previous) != 0 {
		t.Fatalf("retry toggled = %+v, want id echo without mutation snapshot", retryToggled)
	}
	emptyToggle, err := store.TogglePinned(ctx, ownerPeer, nil, true, 1700000604)
	if err != nil {
		t.Fatalf("empty toggle pinned: %v", err)
	}
	if len(emptyToggle.IDs) != 0 || len(emptyToggle.Stories) != 0 {
		t.Fatalf("empty toggle = %+v, want no-op", emptyToggle)
	}
	deleted, err := store.DeleteStories(ctx, ownerPeer, []int{created.Story.ID, created.Story.ID}, 1700000605)
	if err != nil {
		t.Fatalf("delete story: %v", err)
	}
	if len(deleted.IDs) != 1 || deleted.IDs[0] != created.Story.ID || len(deleted.Stories) != 1 || !deleted.Stories[0].Deleted {
		t.Fatalf("deleted = %+v, want deleted story", deleted)
	}
	retryDeleted, err := store.DeleteStories(ctx, ownerPeer, []int{created.Story.ID}, 1700000606)
	if err != nil {
		t.Fatalf("retry delete story: %v", err)
	}
	if len(retryDeleted.IDs) != 1 || retryDeleted.IDs[0] != created.Story.ID || len(retryDeleted.Stories) != 0 {
		t.Fatalf("retry deleted = %+v, want id echo without mutation snapshot", retryDeleted)
	}
	if _, err := store.UpsertStory(ctx, domain.UpsertStoryRequest{Story: domain.Story{
		Owner:      ownerPeer,
		ID:         77,
		Date:       1700000000,
		ExpireDate: 1700000100,
		Pinned:     true,
		Public:     true,
		Media:      media,
	}}); err != nil {
		t.Fatalf("upsert expired pinned story: %v", err)
	}
	expiredPinnedDeleted, err := store.DeleteStories(ctx, ownerPeer, []int{77}, 1700000607)
	if err != nil {
		t.Fatalf("delete expired pinned story: %v", err)
	}
	if len(expiredPinnedDeleted.Stories) != 1 || !expiredPinnedDeleted.Stories[0].Deleted || expiredPinnedDeleted.Stories[0].Pinned {
		t.Fatalf("expired pinned deleted = %+v, want deleted unpinned snapshot", expiredPinnedDeleted)
	}
	if len(expiredPinnedDeleted.Previous) != 1 || expiredPinnedDeleted.Previous[0].Deleted || !expiredPinnedDeleted.Previous[0].Pinned {
		t.Fatalf("expired pinned previous = %+v, want pre-delete pinned snapshot", expiredPinnedDeleted.Previous)
	}
	active, err := store.GetPeerStories(ctx, owner.ID, ownerPeer, 1700000606)
	if err != nil {
		t.Fatalf("get active after delete: %v", err)
	}
	if len(active.Stories) != 0 {
		t.Fatalf("active after delete = %+v, want empty", active)
	}
}

func TestStoryStorePinnedToTopOrderPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	owner, _ := createStoryTestUsers(t, ctx, pool)
	store := NewStoryStore(pool)
	ownerPeer := domain.Peer{Type: domain.PeerTypeUser, ID: owner.ID}
	for _, id := range []int{1, 2, 3, 4} {
		if _, err := store.UpsertStory(ctx, domain.UpsertStoryRequest{Story: domain.Story{
			Owner:      ownerPeer,
			ID:         id,
			Date:       1700000700 + id,
			ExpireDate: 1700009000,
			Pinned:     true,
			Public:     true,
		}}); err != nil {
			t.Fatalf("upsert story %d: %v", id, err)
		}
	}
	if err := store.TogglePinnedToTop(ctx, ownerPeer, []int{3, 1, 3}); err != nil {
		t.Fatalf("toggle pinned to top: %v", err)
	}
	pinned, err := store.ListPinnedStories(ctx, owner.ID, ownerPeer, 0, 2, 1700000800)
	if err != nil {
		t.Fatalf("list pinned stories: %v", err)
	}
	if pinned.Count != 4 || !samePGStoryIDs(pgStoryIDs(pinned.Stories), []int{4, 3}) || !samePGStoryIDs(pinned.PinnedToTop, []int{3, 1}) {
		t.Fatalf("pinned = count %d stories %v top %v, want count 4 stories 4,3 top 3,1", pinned.Count, pgStoryIDs(pinned.Stories), pinned.PinnedToTop)
	}
	androidInitial, err := store.ListPinnedStories(ctx, owner.ID, ownerPeer, -1, 2, 1700000800)
	if err != nil {
		t.Fatalf("list pinned stories with android sentinel: %v", err)
	}
	if androidInitial.Count != pinned.Count || !samePGStoryIDs(pgStoryIDs(androidInitial.Stories), []int{4, 3}) || !samePGStoryIDs(androidInitial.PinnedToTop, []int{3, 1}) {
		t.Fatalf("android sentinel pinned = count %d stories %v top %v, want same first page 4,3 top 3,1", androidInitial.Count, pgStoryIDs(androidInitial.Stories), androidInitial.PinnedToTop)
	}
	if err := store.TogglePinnedToTop(ctx, ownerPeer, []int{2, 1, 4}); err != nil {
		t.Fatalf("replace pinned to top: %v", err)
	}
	pinned, err = store.ListPinnedStories(ctx, owner.ID, ownerPeer, 0, 10, 1700000801)
	if err != nil {
		t.Fatalf("list replaced pinned stories: %v", err)
	}
	if !samePGStoryIDs(pinned.PinnedToTop, []int{2, 1, 4}) {
		t.Fatalf("replaced top = %v, want 2,1,4", pinned.PinnedToTop)
	}
	pageAfterOffset, err := store.ListPinnedStories(ctx, owner.ID, ownerPeer, 3, 10, 1700000801)
	if err != nil {
		t.Fatalf("list pinned page after offset: %v", err)
	}
	if !samePGStoryIDs(pgStoryIDs(pageAfterOffset.Stories), []int{2, 1}) || !samePGStoryIDs(pageAfterOffset.PinnedToTop, []int{2, 1, 4}) {
		t.Fatalf("offset page stories %v top %v, want stories 2,1 and full top 2,1,4", pgStoryIDs(pageAfterOffset.Stories), pageAfterOffset.PinnedToTop)
	}
	pageAfterTail, err := store.ListPinnedStories(ctx, owner.ID, ownerPeer, 1, 10, 1700000801)
	if err != nil {
		t.Fatalf("list pinned page after tail: %v", err)
	}
	if pageAfterTail.Count != 4 || len(pageAfterTail.Stories) != 0 || !samePGStoryIDs(pageAfterTail.PinnedToTop, []int{2, 1, 4}) {
		t.Fatalf("tail page = count %d stories %v top %v, want count 4 empty stories top 2,1,4", pageAfterTail.Count, pgStoryIDs(pageAfterTail.Stories), pageAfterTail.PinnedToTop)
	}
	if _, err := store.TogglePinned(ctx, ownerPeer, []int{2}, false, 1700000802); err != nil {
		t.Fatalf("unpin story: %v", err)
	}
	pinned, err = store.ListPinnedStories(ctx, owner.ID, ownerPeer, 0, 10, 1700000803)
	if err != nil {
		t.Fatalf("list after unpin: %v", err)
	}
	if !samePGStoryIDs(pinned.PinnedToTop, []int{1, 4}) {
		t.Fatalf("top after unpin = %v, want 1,4", pinned.PinnedToTop)
	}
	if err := store.TogglePinnedToTop(ctx, ownerPeer, []int{2}); !errors.Is(err, domain.ErrStoryIDInvalid) {
		t.Fatalf("unpinned top err = %v, want ErrStoryIDInvalid", err)
	}
	if _, err := store.DeleteStories(ctx, ownerPeer, []int{1}, 1700000804); err != nil {
		t.Fatalf("delete story: %v", err)
	}
	pinned, err = store.ListPinnedStories(ctx, owner.ID, ownerPeer, 0, 10, 1700000805)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if !samePGStoryIDs(pinned.PinnedToTop, []int{4}) {
		t.Fatalf("top after delete = %v, want 4", pinned.PinnedToTop)
	}
	if err := store.TogglePinnedToTop(ctx, ownerPeer, nil); err != nil {
		t.Fatalf("clear pinned to top: %v", err)
	}
	pinned, err = store.ListPinnedStories(ctx, owner.ID, ownerPeer, 0, 10, 1700000806)
	if err != nil {
		t.Fatalf("list after clear: %v", err)
	}
	if len(pinned.PinnedToTop) != 0 {
		t.Fatalf("top after clear = %v, want empty", pinned.PinnedToTop)
	}
}

func pgStoryIDs(stories []domain.Story) []int {
	out := make([]int, 0, len(stories))
	for _, story := range stories {
		out = append(out, story.ID)
	}
	return out
}

func samePGStoryIDs(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestStoryUpdateEventPayloadPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	owner, _ := createStoryTestUsers(t, ctx, pool)
	events := NewUpdateEventStore(pool)
	ownerPeer := domain.Peer{Type: domain.PeerTypeUser, ID: owner.ID}
	story := domain.Story{
		Owner:      ownerPeer,
		ID:         7,
		Date:       1700000400,
		ExpireDate: 1700004000,
		Public:     true,
		Caption:    "durable payload",
	}
	reaction := &domain.MessageReaction{Type: domain.MessageReactionEmoji, Emoticon: "spark"}

	if err := events.Append(ctx, owner.ID, domain.UpdateEvent{
		Type:     domain.UpdateEventStory,
		Pts:      1,
		PtsCount: 1,
		Date:     story.Date,
		Peer:     ownerPeer,
		Story:    story,
	}); err != nil {
		t.Fatalf("append story event: %v", err)
	}
	if err := events.Append(ctx, owner.ID, domain.UpdateEvent{
		Type:     domain.UpdateEventSentStoryReaction,
		Pts:      2,
		PtsCount: 1,
		Date:     1700000401,
		Peer:     ownerPeer,
		MaxID:    story.ID,
		Story:    story,
		Reaction: reaction,
	}); err != nil {
		t.Fatalf("append reaction event: %v", err)
	}

	listed, err := events.ListAfter(ctx, owner.ID, 0, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(listed) != 2 || listed[0].Story.ID != story.ID || listed[0].Story.Caption != story.Caption {
		t.Fatalf("listed story events = %+v, want story payload", listed)
	}
	if listed[1].Reaction == nil || *listed[1].Reaction != *reaction || listed[1].Story.ID != story.ID {
		t.Fatalf("listed reaction event = %+v, want reaction and story payload", listed[1])
	}

	batched, err := events.BatchByCursor(ctx, []storepkg.EventCursor{{UserID: owner.ID, Pts: 2}})
	if err != nil {
		t.Fatalf("batch events: %v", err)
	}
	if len(batched) != 1 || batched[0].Reaction == nil || *batched[0].Reaction != *reaction || batched[0].Story.ID != story.ID {
		t.Fatalf("batched event = %+v, want durable reaction payload", batched)
	}
}

func createStoryTestUsers(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (domain.User, domain.User) {
	t.Helper()
	suffix := randomSuffix(t)
	users := NewUserStore(pool)
	owner, err := users.Create(ctx, domain.User{AccessHash: 8101, Phone: "+1771" + suffix + "01", FirstName: "StoryOwner"})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	viewer, err := users.Create(ctx, domain.User{AccessHash: 8102, Phone: "+1771" + suffix + "02", FirstName: "StoryViewer"})
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	t.Cleanup(func() {
		cleanupStoryTestRows(t, context.Background(), pool, owner.ID, viewer.ID)
	})
	return owner, viewer
}

func cleanupStoryTestRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ownerID, viewerID int64) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
DELETE FROM story_hidden_peers
WHERE viewer_user_id = $1
   OR owner_peer_id = $2`, viewerID, ownerID); err != nil {
		t.Fatalf("cleanup story hidden peers: %v", err)
	}
	if _, err := pool.Exec(ctx, `
DELETE FROM story_read_states
WHERE viewer_user_id = $1
   OR owner_peer_id = $2`, viewerID, ownerID); err != nil {
		t.Fatalf("cleanup story read states: %v", err)
	}
	if _, err := pool.Exec(ctx, `
DELETE FROM story_views
WHERE viewer_user_id = $1
   OR owner_peer_id = $2`, viewerID, ownerID); err != nil {
		t.Fatalf("cleanup story views: %v", err)
	}
	if _, err := pool.Exec(ctx, `
DELETE FROM story_exposures
WHERE viewer_user_id = $1
   OR owner_peer_id = $2`, viewerID, ownerID); err != nil {
		t.Fatalf("cleanup story exposures: %v", err)
	}
	if _, err := pool.Exec(ctx, `
DELETE FROM stories
WHERE owner_peer_id = ANY($1::bigint[])`, []int64{ownerID, viewerID}); err != nil {
		t.Fatalf("cleanup stories: %v", err)
	}
	if _, err := pool.Exec(ctx, `
DELETE FROM users
WHERE id = ANY($1::bigint[])`, []int64{ownerID, viewerID}); err != nil {
		t.Fatalf("cleanup story users: %v", err)
	}
}

func cleanupChannelStoryTestRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, channelID int64, userIDs []int64) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
DELETE FROM story_hidden_peers
WHERE (owner_peer_type = 'channel' AND owner_peer_id = $1)
   OR viewer_user_id = ANY($2::bigint[])`, channelID, userIDs); err != nil {
		t.Fatalf("cleanup channel story hidden peers: %v", err)
	}
	if _, err := pool.Exec(ctx, `
DELETE FROM story_read_states
WHERE (owner_peer_type = 'channel' AND owner_peer_id = $1)
   OR viewer_user_id = ANY($2::bigint[])`, channelID, userIDs); err != nil {
		t.Fatalf("cleanup channel story read states: %v", err)
	}
	if _, err := pool.Exec(ctx, `
DELETE FROM story_views
WHERE (owner_peer_type = 'channel' AND owner_peer_id = $1)
   OR viewer_user_id = ANY($2::bigint[])`, channelID, userIDs); err != nil {
		t.Fatalf("cleanup channel story views: %v", err)
	}
	if _, err := pool.Exec(ctx, `
DELETE FROM story_exposures
WHERE (owner_peer_type = 'channel' AND owner_peer_id = $1)
   OR viewer_user_id = ANY($2::bigint[])`, channelID, userIDs); err != nil {
		t.Fatalf("cleanup channel story exposures: %v", err)
	}
	if _, err := pool.Exec(ctx, `
DELETE FROM stories
WHERE owner_peer_type = 'channel'
  AND owner_peer_id = $1`, channelID); err != nil {
		t.Fatalf("cleanup channel stories: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM channels WHERE id = $1`, channelID); err != nil {
		t.Fatalf("cleanup channel: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1::bigint[])`, userIDs); err != nil {
		t.Fatalf("cleanup channel story users: %v", err)
	}
}

func storyListContains(list domain.StoryList, peer domain.Peer, storyID int) bool {
	for _, story := range list.Stories {
		if story.Owner == peer && story.ID == storyID {
			return true
		}
	}
	return false
}

func cleanupStoryPagingTestRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, viewerID int64, ownerIDs []int64) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
DELETE FROM story_hidden_peers
WHERE viewer_user_id = $1
   OR owner_peer_id = ANY($2::bigint[])`, viewerID, ownerIDs); err != nil {
		t.Fatalf("cleanup story paging hidden peers: %v", err)
	}
	if _, err := pool.Exec(ctx, `
DELETE FROM story_read_states
WHERE viewer_user_id = $1
   OR owner_peer_id = ANY($2::bigint[])`, viewerID, ownerIDs); err != nil {
		t.Fatalf("cleanup story paging read states: %v", err)
	}
	if _, err := pool.Exec(ctx, `
DELETE FROM story_views
WHERE viewer_user_id = $1
   OR owner_peer_id = ANY($2::bigint[])`, viewerID, ownerIDs); err != nil {
		t.Fatalf("cleanup story paging views: %v", err)
	}
	if _, err := pool.Exec(ctx, `
DELETE FROM story_exposures
WHERE viewer_user_id = $1
   OR owner_peer_id = ANY($2::bigint[])`, viewerID, ownerIDs); err != nil {
		t.Fatalf("cleanup story paging exposures: %v", err)
	}
	if _, err := pool.Exec(ctx, `
DELETE FROM stories
WHERE owner_peer_id = ANY($1::bigint[])`, ownerIDs); err != nil {
		t.Fatalf("cleanup story paging stories: %v", err)
	}
	userIDs := append([]int64{viewerID}, ownerIDs...)
	if _, err := pool.Exec(ctx, `
DELETE FROM users
WHERE id = ANY($1::bigint[])`, userIDs); err != nil {
		t.Fatalf("cleanup story paging users: %v", err)
	}
}
