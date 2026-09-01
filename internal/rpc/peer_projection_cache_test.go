package rpc

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/iamxvbaba/td/clock"
	"go.uber.org/zap/zaptest"

	appchannels "telesrv/internal/app/channels"
	appdialogs "telesrv/internal/app/dialogs"
	appmessages "telesrv/internal/app/messages"
	appusers "telesrv/internal/app/users"
	"telesrv/internal/domain"
	"telesrv/internal/store/memory"
)

func TestEnrichMessageListReusesApplicationViewerProjection(t *testing.T) {
	ctx := context.Background()
	const (
		viewerID = int64(1001)
		peerID   = int64(1002)
		viaBotID = int64(1003)
	)
	users := &countingMapUsersService{mapUsersService: mapUsersService{users: map[int64]domain.User{
		viewerID: {ID: viewerID, FirstName: "Viewer"},
		peerID:   {ID: peerID, FirstName: "Peer"},
		viaBotID: {ID: viaBotID, FirstName: "Bot", Bot: true},
	}}}
	r := New(Config{}, Deps{
		Messages: newCompletePeerCacheMessageService(),
		Users:    users,
	}, zaptest.NewLogger(t), clock.System)

	list := domain.MessageList{
		Messages: []domain.Message{{
			OwnerUserID: viewerID,
			Peer:        domain.Peer{Type: domain.PeerTypeUser, ID: peerID},
			From:        domain.Peer{Type: domain.PeerTypeUser, ID: viewerID},
			ViaBotID:    viaBotID,
		}},
		Users: []domain.User{
			{ID: viewerID, FirstName: "Viewer"},
			{ID: peerID, FirstName: "Peer"},
		},
	}
	got := r.enrichMessageList(ctx, viewerID, list)
	if users.byIDsCalls != 1 || len(users.lastByIDs) != 1 || users.lastByIDs[0] != viaBotID {
		t.Fatalf("ByIDs calls=%d ids=%v, want only missing nested bot %d", users.byIDsCalls, users.lastByIDs, viaBotID)
	}
	if len(got.Users) != 3 {
		t.Fatalf("users=%+v, want projected envelope plus nested bot", got.Users)
	}

	users.byIDsCalls = 0
	users.lastByIDs = nil
	list.Users = append(list.Users, domain.User{ID: viaBotID, FirstName: "Bot", Bot: true})
	got = r.enrichMessageList(ctx, viewerID, list)
	if users.byIDsCalls != 0 {
		t.Fatalf("ByIDs calls with complete projected envelope=%d, want 0", users.byIDsCalls)
	}
	if len(got.Users) != 3 {
		t.Fatalf("complete users=%+v, want 3", got.Users)
	}
}

func TestEnrichMessageListDoesNotTrustPartialApplicationProjection(t *testing.T) {
	ctx := context.Background()
	const (
		viewerID = int64(1101)
		peerID   = int64(1102)
	)
	users := &countingMapUsersService{mapUsersService: mapUsersService{users: map[int64]domain.User{
		viewerID: {ID: viewerID, FirstName: "Projected viewer"},
		peerID:   {ID: peerID, FirstName: "Projected peer"},
	}}}
	r := New(Config{}, Deps{
		Messages: appmessages.NewService(memory.NewMessageStore(), nil),
		Users:    users,
	}, zaptest.NewLogger(t), clock.System)

	got := r.enrichMessageList(ctx, viewerID, domain.MessageList{
		Messages: []domain.Message{{
			OwnerUserID: viewerID,
			Peer:        domain.Peer{Type: domain.PeerTypeUser, ID: peerID},
			From:        domain.Peer{Type: domain.PeerTypeUser, ID: viewerID},
		}},
		Users: []domain.User{{ID: viewerID, FirstName: "Raw viewer"}, {ID: peerID, FirstName: "Raw peer"}},
	})
	if users.byIDsCalls != 1 || len(users.lastByIDs) != 2 {
		t.Fatalf("ByIDs calls=%d ids=%v, want one authoritative reload of both ordinary refs", users.byIDsCalls, users.lastByIDs)
	}
	for _, user := range got.Users {
		if user.ID == peerID && user.FirstName != "Projected peer" {
			t.Fatalf("peer=%+v, want RPC projection to replace the untrusted raw envelope", user)
		}
	}
}

func newCompletePeerCacheMessageService() *appmessages.Service {
	return appmessages.NewService(memory.NewMessageStore(), nil,
		appmessages.WithContactStore(memory.NewContactStore()),
		appmessages.WithPhotoProvider(peerCacheTestPhotos{}),
		appmessages.WithPrivacyEvaluator(peerCacheTestPrivacy{}),
		appmessages.WithAccountFreezeProvider(peerCacheTestFreezes{}),
	)
}

type peerCacheTestPhotos struct{}

func (peerCacheTestPhotos) CurrentProfilePhotos(context.Context, domain.PeerType, []int64) (map[int64]domain.ProfilePhotoRef, error) {
	return map[int64]domain.ProfilePhotoRef{}, nil
}

type peerCacheTestPrivacy struct{}

func (peerCacheTestPrivacy) CanSee(context.Context, int64, int64, domain.PrivacyKey) (bool, error) {
	return true, nil
}

type peerCacheTestFreezes struct{}

func (peerCacheTestFreezes) AccountFreezes(context.Context, []int64) (map[int64]domain.AccountFreeze, error) {
	return map[int64]domain.AccountFreeze{}, nil
}

func TestViewerPeerCacheChannelsForIDsUsesBatchAndCachesMissing(t *testing.T) {
	ctx := context.Background()
	userStore := memory.NewUserStore()
	owner, err := userStore.Create(ctx, domain.User{AccessHash: 91, Phone: "15550009101", FirstName: "Owner"})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	channelStore := memory.NewChannelStore()
	channelService := appchannels.NewService(channelStore)
	first, err := channelService.CreateChannel(ctx, owner.ID, domain.CreateChannelRequest{
		Title:     "Cache One",
		Broadcast: true,
		Date:      1700001900,
	})
	if err != nil {
		t.Fatalf("create first channel: %v", err)
	}
	second, err := channelService.CreateChannel(ctx, owner.ID, domain.CreateChannelRequest{
		Title:     "Cache Two",
		Megagroup: true,
		Date:      1700001910,
	})
	if err != nil {
		t.Fatalf("create second channel: %v", err)
	}

	counting := &countingChannelsService{Service: channelService}
	r := New(Config{}, Deps{
		Users:    appusers.NewService(userStore),
		Channels: counting,
		Dialogs:  appdialogs.NewService(memory.NewDialogStore(), channelStore),
	}, zaptest.NewLogger(t), clock.System)
	cache := newViewerPeerCache(r)

	got := cache.channelsForIDs(ctx, owner.ID, []int64{first.Channel.ID, second.Channel.ID, first.Channel.ID, 0})
	if len(got) != 2 {
		t.Fatalf("first channelsForIDs returned %d channels, want 2", len(got))
	}
	if counting.getChannelsCalls != 1 || counting.getChannelCalls != 0 {
		t.Fatalf("first load calls: GetChannels=%d GetChannel=%d, want one batch only", counting.getChannelsCalls, counting.getChannelCalls)
	}

	again := cache.channelsForIDs(ctx, owner.ID, []int64{second.Channel.ID, first.Channel.ID})
	if len(again) != 2 {
		t.Fatalf("cached channelsForIDs returned %d channels, want 2", len(again))
	}
	if counting.getChannelsCalls != 1 || counting.getChannelCalls != 0 {
		t.Fatalf("cached load calls: GetChannels=%d GetChannel=%d, want no extra calls", counting.getChannelsCalls, counting.getChannelCalls)
	}

	missingID := second.Channel.ID + 9999
	missing := cache.channelsForIDs(ctx, owner.ID, []int64{missingID})
	if len(missing) != 0 {
		t.Fatalf("missing channelsForIDs returned %d channels, want 0", len(missing))
	}
	if counting.getChannelsCalls != 2 || counting.getChannelCalls != 0 {
		t.Fatalf("missing load calls: GetChannels=%d GetChannel=%d, want second batch only", counting.getChannelsCalls, counting.getChannelCalls)
	}

	missingAgain := cache.channelsForIDs(ctx, owner.ID, []int64{missingID})
	if len(missingAgain) != 0 {
		t.Fatalf("cached missing channelsForIDs returned %d channels, want 0", len(missingAgain))
	}
	if counting.getChannelsCalls != 2 || counting.getChannelCalls != 0 {
		t.Fatalf("cached missing calls: GetChannels=%d GetChannel=%d, want no extra calls", counting.getChannelsCalls, counting.getChannelCalls)
	}
}

func TestViewerPeerCacheChunksLargeUserUnionsWithoutTruncation(t *testing.T) {
	const viewerID = int64(9001)
	ids := make([]int64, maxPeerProjectionUsersPerBatch+37)
	base := make(map[int64]domain.User, len(ids))
	for i := range ids {
		ids[i] = int64(10000 + i)
		base[ids[i]] = domain.User{ID: ids[i], FirstName: "projected"}
	}
	users := &countingMapUsersService{mapUsersService: mapUsersService{users: base}}
	router := New(Config{}, Deps{Users: users}, zaptest.NewLogger(t), clock.System)

	got := newViewerPeerCache(router).usersForIDs(context.Background(), viewerID, ids)
	if len(got) != len(ids) {
		t.Fatalf("projected users=%d, want all %d", len(got), len(ids))
	}
	if users.byIDsCalls != 2 || len(users.byIDsBatches) != 2 {
		t.Fatalf("ByIDs calls=%d batches=%d, want two bounded batches", users.byIDsCalls, len(users.byIDsBatches))
	}
	for i, batch := range users.byIDsBatches {
		if len(batch) == 0 || len(batch) > maxPeerProjectionUsersPerBatch {
			t.Fatalf("batch %d size=%d, want 1..%d", i, len(batch), maxPeerProjectionUsersPerBatch)
		}
	}
}

type capacityBudgetPeerUsers struct {
	mapUsersService
	maxBatch int
	calls    [][]int64
}

func (s *capacityBudgetPeerUsers) ByIDs(_ context.Context, _ int64, ids []int64) ([]domain.User, error) {
	s.calls = append(s.calls, append([]int64(nil), ids...))
	if s.maxBatch > 0 && len(ids) > s.maxBatch {
		return nil, fmt.Errorf("%w: test batch size %d", appusers.ErrBatchViewerCells, len(ids))
	}
	out := make([]domain.User, len(ids))
	for i, id := range ids {
		out[i] = domain.User{ID: id, FirstName: "projected"}
	}
	return out, nil
}

func TestViewerPeerCacheCapacityRecoveryBudgetIsSharedAndFailClosed(t *testing.T) {
	ids := func(count int) []int64 {
		out := make([]int64, count)
		for i := range out {
			out[i] = int64(20_000 + i)
		}
		return out
	}
	newCache := func(users *capacityBudgetPeerUsers) *viewerPeerCache {
		router := New(Config{}, Deps{Users: users}, zaptest.NewLogger(t), clock.System)
		return newViewerPeerCache(router)
	}

	t.Run("bounded split completes below call limit", func(t *testing.T) {
		users := &capacityBudgetPeerUsers{maxBatch: 32}
		got, err := newCache(users).usersForIDsStrict(context.Background(), 9001, ids(maxPeerProjectionUsersPerBatch))
		if err != nil || len(got) != maxPeerProjectionUsersPerBatch {
			t.Fatalf("strict users=%d err=%v, want complete projection", len(got), err)
		}
		if len(users.calls) != 63 {
			t.Fatalf("resolver calls=%d, want balanced 63-call recovery below limit %d", len(users.calls), maxPeerProjectionRecoveryCalls)
		}
	})

	t.Run("call budget rejects whole projection", func(t *testing.T) {
		users := &capacityBudgetPeerUsers{maxBatch: 16}
		got, err := newCache(users).usersForIDsStrict(context.Background(), 9001, ids(maxPeerProjectionUsersPerBatch))
		if got != nil || !errors.Is(err, ErrUserProjectionCapacityRecoveryLimit) {
			t.Fatalf("strict users=%+v err=%v, want nil recovery-limit error", got, err)
		}
		if errors.Is(err, appusers.ErrBatchViewerCells) {
			t.Fatalf("recovery-limit error must not retain capacity identity: %v", err)
		}
		if len(users.calls) != maxPeerProjectionRecoveryCalls {
			t.Fatalf("resolver calls=%d, want hard limit %d", len(users.calls), maxPeerProjectionRecoveryCalls)
		}
	})

	t.Run("attempted owner budget spans outer chunks", func(t *testing.T) {
		users := &capacityBudgetPeerUsers{}
		got, err := newCache(users).usersForIDsStrict(context.Background(), 9001, ids(maxPeerProjectionAttemptedOwnerIDs+1))
		if got != nil || !errors.Is(err, ErrUserProjectionCapacityRecoveryLimit) {
			t.Fatalf("strict users=%+v err=%v, want nil recovery-limit error", got, err)
		}
		wantCalls := maxPeerProjectionAttemptedOwnerIDs / maxPeerProjectionUsersPerBatch
		if len(users.calls) != wantCalls {
			t.Fatalf("resolver calls=%d, want %d successful chunks before shared owner limit", len(users.calls), wantCalls)
		}
	})
}

func TestWithDialogListPresenceOnlyLoadsMissingMessagePeers(t *testing.T) {
	ctx := context.Background()
	const (
		viewerID = int64(1001)
		peerID   = int64(1002)
		viaBotID = int64(1003)
	)
	users := &countingMapUsersService{mapUsersService: mapUsersService{users: map[int64]domain.User{
		viewerID: {ID: viewerID, FirstName: "Viewer"},
		peerID:   {ID: peerID, FirstName: "Peer"},
		viaBotID: {ID: viaBotID, FirstName: "Bot", Bot: true},
	}}}
	r := New(Config{}, Deps{Users: users}, zaptest.NewLogger(t), clock.System)

	list := domain.DialogList{
		Messages: []domain.Message{{
			Peer:     domain.Peer{Type: domain.PeerTypeUser, ID: peerID},
			From:     domain.Peer{Type: domain.PeerTypeUser, ID: viewerID},
			ViaBotID: viaBotID,
		}},
		Users: []domain.User{
			{ID: viewerID, FirstName: "Viewer"},
			{ID: peerID, FirstName: "Peer"},
		},
	}

	got := r.withDialogListPresence(ctx, viewerID, list)
	if users.byIDsCalls != 1 {
		t.Fatalf("ByIDs calls = %d, want one missing-peer batch", users.byIDsCalls)
	}
	if len(users.lastByIDs) != 1 || users.lastByIDs[0] != viaBotID {
		t.Fatalf("ByIDs ids = %v, want only missing via bot %d", users.lastByIDs, viaBotID)
	}
	if len(got.Users) != 3 {
		t.Fatalf("projected users = %d, want existing two plus missing bot", len(got.Users))
	}

	users.byIDsCalls = 0
	users.lastByIDs = nil
	list.Users = append(list.Users, domain.User{ID: viaBotID, FirstName: "Bot", Bot: true})
	got = r.withDialogListPresence(ctx, viewerID, list)
	if users.byIDsCalls != 0 {
		t.Fatalf("ByIDs calls with complete envelope = %d, want zero", users.byIDsCalls)
	}
	if len(got.Users) != 3 {
		t.Fatalf("complete projected users = %d, want unchanged three", len(got.Users))
	}
}
