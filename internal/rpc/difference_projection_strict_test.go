package rpc

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/iamxvbaba/td/clock"
	"github.com/iamxvbaba/td/tg"
	"go.uber.org/zap/zaptest"

	appchannels "telesrv/internal/app/channels"
	appusers "telesrv/internal/app/users"
	"telesrv/internal/domain"
	"telesrv/internal/store"
	"telesrv/internal/store/memory"
)

type strictDifferenceUsers struct {
	mapUsersService
	maxBatch        int
	capacityErr     error
	failErr         error
	calls           [][]int64
	capacityFailure int
}

func (s *strictDifferenceUsers) ByIDs(ctx context.Context, viewerUserID int64, ids []int64) ([]domain.User, error) {
	s.calls = append(s.calls, append([]int64(nil), ids...))
	if s.failErr != nil {
		return nil, s.failErr
	}
	if s.maxBatch > 0 && len(ids) > s.maxBatch {
		s.capacityFailure++
		return nil, fmt.Errorf("%w: test batch %d", s.capacityErr, len(ids))
	}
	return s.mapUsersService.ByIDs(ctx, viewerUserID, ids)
}

type strictDifferenceChannels struct {
	*appchannels.Service
	difference domain.ChannelDifference
}

func (s *strictDifferenceChannels) GetDifference(context.Context, int64, domain.ChannelDifferenceRequest) (domain.ChannelDifference, error) {
	return s.difference, nil
}

func strictDifferenceUsersAndRefs(count int) ([]int64, []domain.Peer, []domain.User, map[int64]domain.User) {
	ids := make([]int64, count)
	peers := make([]domain.Peer, count)
	raw := make([]domain.User, count)
	projected := make(map[int64]domain.User, count)
	for i := range ids {
		id := int64(2_000_000_000 + i)
		ids[i] = id
		peers[i] = domain.Peer{Type: domain.PeerTypeUser, ID: id}
		raw[i] = domain.User{ID: id, AccessHash: id + 10, Phone: "raw-secret-phone", FirstName: "raw"}
		projected[id] = domain.User{ID: id, FirstName: "projected"}
	}
	return ids, peers, raw, projected
}

func assertStrictDifferenceUsers(t *testing.T, users []tg.UserClass, want int) {
	t.Helper()
	if len(users) != want {
		t.Fatalf("projected users = %d, want %d", len(users), want)
	}
	for _, item := range users {
		user, ok := item.(*tg.User)
		if !ok {
			t.Fatalf("projected user = %T, want *tg.User", item)
		}
		if user.Phone != "" {
			t.Fatalf("raw phone leaked for user %d: %q", user.ID, user.Phone)
		}
	}
}

func TestViewerPeerCacheStrictProjectionSplitsAllCapacityErrorsAndRejectsMissing(t *testing.T) {
	capacityErrors := map[string]error{
		"privacy_memberships": store.ErrActiveChannelMemberPairsLimit,
		"owner_union":         appusers.ErrBatchUsersLimit,
		"sparse_cells":        appusers.ErrBatchViewerCells,
	}
	for name, capacityErr := range capacityErrors {
		t.Run(name, func(t *testing.T) {
			ids, _, _, projected := strictDifferenceUsersAndRefs(9)
			users := &strictDifferenceUsers{
				mapUsersService: mapUsersService{users: projected},
				maxBatch:        2,
				capacityErr:     capacityErr,
			}
			r := New(Config{}, Deps{Users: users}, zaptest.NewLogger(t), clock.System)
			got, err := newViewerPeerCache(r).usersForIDsStrict(context.Background(), 1_900_000_001, ids)
			if err != nil || len(got) != len(ids) || users.capacityFailure == 0 {
				t.Fatalf("strict projection users=%d failures=%d err=%v, want complete split result", len(got), users.capacityFailure, err)
			}
		})
	}

	missingID := int64(2_100_000_001)
	r := New(Config{}, Deps{Users: &strictDifferenceUsers{
		mapUsersService: mapUsersService{users: map[int64]domain.User{}},
	}}, zaptest.NewLogger(t), clock.System)
	got, err := newViewerPeerCache(r).usersForIDsStrict(context.Background(), 1_900_000_001, []int64{missingID, domain.OfficialSystemUserID})
	if !errors.Is(err, ErrDurableUserProjectionIncomplete) || got != nil {
		t.Fatalf("strict incomplete projection = %+v, %v; want nil ErrDurableUserProjectionIncomplete", got, err)
	}
}

func TestUpdatesGetDifferenceStrictProjectionChunksAndSplitsCapacity(t *testing.T) {
	const viewerID = int64(1_900_000_001)
	ids, peers, raw, projected := strictDifferenceUsersAndRefs(maxPeerProjectionUsersPerBatch + 1)
	users := &strictDifferenceUsers{
		mapUsersService: mapUsersService{users: projected},
		maxBatch:        250,
		capacityErr:     appusers.ErrBatchUsersLimit,
	}
	updates := &captureUpdates{state: domain.UpdateState{Pts: 42, Date: 1700000000}}
	updates.difference = &domain.UpdateDifference{
		State: updates.state,
		Events: []domain.UpdateEvent{{
			Type: domain.UpdateEventPinnedDialogs, Pts: 42, PtsCount: 1, Peers: peers, Users: raw,
		}},
	}
	r := New(Config{}, Deps{Users: users, Updates: updates}, zaptest.NewLogger(t), clock.System)

	got, err := r.onUpdatesGetDifference(WithUserID(context.Background(), viewerID), &tg.UpdatesGetDifferenceRequest{})
	if err != nil {
		t.Fatalf("updates.getDifference: %v", err)
	}
	full, ok := got.(*tg.UpdatesDifference)
	if !ok || full.State.Pts != 42 {
		t.Fatalf("difference = %T %+v, want full pts 42", got, got)
	}
	assertStrictDifferenceUsers(t, full.Users, len(ids))
	if users.capacityFailure == 0 || len(users.calls) < 3 {
		t.Fatalf("resolver calls=%d capacity failures=%d, want bounded recursive split", len(users.calls), users.capacityFailure)
	}
	for i, call := range users.calls {
		if len(call) > maxPeerProjectionUsersPerBatch {
			t.Fatalf("resolver call %d size=%d exceeds outer chunk %d", i, len(call), maxPeerProjectionUsersPerBatch)
		}
	}
}

func TestUpdatesGetChannelDifferenceStrictProjectionChunksAndSplitsCapacity(t *testing.T) {
	ctx := context.Background()
	const viewerID = int64(1_900_000_001)
	ids, _, raw, projected := strictDifferenceUsersAndRefs(maxPeerProjectionUsersPerBatch + 1)
	users := &strictDifferenceUsers{
		mapUsersService: mapUsersService{users: projected},
		maxBatch:        250,
		capacityErr:     appusers.ErrBatchViewerCells,
	}
	base := appchannels.NewService(memory.NewChannelStore())
	created, err := base.CreateChannel(ctx, viewerID, domain.CreateChannelRequest{Title: "strict diff", Broadcast: true, Date: 1700000000})
	if err != nil {
		t.Fatal(err)
	}
	channels := &strictDifferenceChannels{Service: base, difference: domain.ChannelDifference{
		Channel: created.Channel,
		Self:    domain.ChannelMember{ChannelID: created.Channel.ID, UserID: viewerID, Status: domain.ChannelMemberActive},
		OtherUpdates: []domain.ChannelUpdateEvent{{
			Type: domain.ChannelUpdateDeleteMessages, Pts: 77, PtsCount: 1, MessageIDs: []int{1}, UserIDs: ids,
		}},
		Users: raw, Pts: 77, Final: true,
	}}
	r := New(Config{}, Deps{Users: users, Channels: channels}, zaptest.NewLogger(t), clock.System)

	got, err := r.onUpdatesGetChannelDifference(WithUserID(ctx, viewerID), &tg.UpdatesGetChannelDifferenceRequest{
		Channel: &tg.InputChannel{ChannelID: created.Channel.ID, AccessHash: created.Channel.AccessHash},
		Filter:  &tg.ChannelMessagesFilterEmpty{}, Limit: 100,
	})
	if err != nil {
		t.Fatalf("updates.getChannelDifference: %v", err)
	}
	full, ok := got.(*tg.UpdatesChannelDifference)
	if !ok || full.Pts != 77 {
		t.Fatalf("channel difference = %T %+v, want full pts 77", got, got)
	}
	assertStrictDifferenceUsers(t, full.Users, len(ids))
	if users.capacityFailure == 0 || len(users.calls) < 3 {
		t.Fatalf("resolver calls=%d capacity failures=%d, want bounded recursive split", len(users.calls), users.capacityFailure)
	}
}

func TestDurableDifferencesFailClosedOnOrdinaryUserResolverError(t *testing.T) {
	boom := errors.New("projection unavailable")
	const viewerID = int64(1_900_000_001)
	ids, peers, raw, projected := strictDifferenceUsersAndRefs(1)

	t.Run("account", func(t *testing.T) {
		users := &strictDifferenceUsers{mapUsersService: mapUsersService{users: projected}, failErr: boom}
		updates := &captureUpdates{state: domain.UpdateState{Pts: 9, Date: 1700000000}}
		updates.difference = &domain.UpdateDifference{State: updates.state, Events: []domain.UpdateEvent{{
			Type: domain.UpdateEventPinnedDialogs, Pts: 9, PtsCount: 1, Peers: peers, Users: raw,
		}}}
		r := New(Config{}, Deps{Users: users, Updates: updates}, zaptest.NewLogger(t), clock.System)
		got, err := r.onUpdatesGetDifference(WithUserID(context.Background(), viewerID), &tg.UpdatesGetDifferenceRequest{})
		if err == nil || got != nil || len(users.calls) != 1 || updates.commitCalls != 0 {
			t.Fatalf("account difference=%T err=%v calls=%d commits=%d, want fail-closed nil without raw phone/PTS advance", got, err, len(users.calls), updates.commitCalls)
		}
	})

	t.Run("channel", func(t *testing.T) {
		ctx := context.Background()
		users := &strictDifferenceUsers{mapUsersService: mapUsersService{users: projected}, failErr: boom}
		base := appchannels.NewService(memory.NewChannelStore())
		created, err := base.CreateChannel(ctx, viewerID, domain.CreateChannelRequest{Title: "strict error", Broadcast: true, Date: 1700000000})
		if err != nil {
			t.Fatal(err)
		}
		channels := &strictDifferenceChannels{Service: base, difference: domain.ChannelDifference{
			Channel: created.Channel,
			Self:    domain.ChannelMember{ChannelID: created.Channel.ID, UserID: viewerID, Status: domain.ChannelMemberActive},
			OtherUpdates: []domain.ChannelUpdateEvent{{
				Type: domain.ChannelUpdateDeleteMessages, Pts: 10, PtsCount: 1, MessageIDs: []int{1}, UserIDs: ids,
			}},
			Users: raw, Pts: 10, Final: true,
		}}
		r := New(Config{}, Deps{Users: users, Channels: channels}, zaptest.NewLogger(t), clock.System)
		got, err := r.onUpdatesGetChannelDifference(WithUserID(ctx, viewerID), &tg.UpdatesGetChannelDifferenceRequest{
			Channel: &tg.InputChannel{ChannelID: created.Channel.ID, AccessHash: created.Channel.AccessHash},
			Filter:  &tg.ChannelMessagesFilterEmpty{}, Limit: 100,
		})
		if err == nil || got != nil || len(users.calls) != 1 {
			t.Fatalf("channel difference=%T err=%v calls=%d, want fail-closed nil without raw phone/PTS response", got, err, len(users.calls))
		}
	})
}
