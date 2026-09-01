package rpc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/iamxvbaba/td/clock"
	"github.com/iamxvbaba/td/tg"
	"go.uber.org/zap/zaptest"

	appusers "telesrv/internal/app/users"
	"telesrv/internal/domain"
	"telesrv/internal/store"
)

type capacitySplittingMainOutboxUsers struct {
	*countingOutboxUsersService
	maxEdges       int
	capacityErr    error
	sparseRequests []map[int64][]int64
}

func (s *capacitySplittingMainOutboxUsers) ByIDsForViewerUserIDs(ctx context.Context, requested map[int64][]int64) (map[int64][]domain.User, error) {
	s.sparseRequests = append(s.sparseRequests, cloneOutboxSparseRequest(requested))
	if s.maxEdges > 0 && sparseOutboxRequestedEdgeCount(requested, s.maxEdges+1) > s.maxEdges {
		err := s.capacityErr
		if err == nil {
			err = store.ErrActiveChannelMemberPairsLimit
		}
		return nil, fmt.Errorf("%w: test capacity", err)
	}
	return s.countingOutboxUsersService.ByIDsForViewerUserIDs(ctx, requested)
}

type failingSparseMainOutboxResolver struct {
	err   error
	calls int
}

func (s *failingSparseMainOutboxResolver) ByIDsForViewerUserIDs(context.Context, map[int64][]int64) (map[int64][]domain.User, error) {
	s.calls++
	return nil, s.err
}

func TestResolveSparseOutboxUsersSplitsEveryProjectionCapacityError(t *testing.T) {
	capacityErrors := map[string]error{
		"privacy_memberships": store.ErrActiveChannelMemberPairsLimit,
		"owner_union":         appusers.ErrBatchUsersLimit,
		"sparse_cells":        appusers.ErrBatchViewerCells,
	}
	for name, capacityErr := range capacityErrors {
		t.Run(name, func(t *testing.T) {
			base := &countingOutboxUsersService{users: map[int64]domain.User{
				2001: {ID: 2001, FirstName: "one"},
				2002: {ID: 2002, FirstName: "two"},
			}}
			resolver := &capacitySplittingMainOutboxUsers{
				countingOutboxUsersService: base,
				maxEdges:                   1,
				capacityErr:                capacityErr,
			}
			projected, err := resolveSparseOutboxUsers(context.Background(), resolver, map[int64][]int64{
				1001: {2001},
				1002: {2002},
			})
			if err != nil || len(projected[1001]) != 1 || len(projected[1002]) != 1 {
				t.Fatalf("projected=%+v err=%v, want both split results", projected, err)
			}
			if len(resolver.sparseRequests) != 3 || len(base.calls) != 0 {
				t.Fatalf("sparse calls=%d scalar=%+v, want one failed batch plus two sparse halves", len(resolver.sparseRequests), base.calls)
			}
		})
	}
}

func TestResolveSparseOutboxUsersDoesNotSplitOtherErrors(t *testing.T) {
	boom := errors.New("projection unavailable")
	resolver := &failingSparseMainOutboxResolver{err: boom}
	projected, err := resolveSparseOutboxUsers(context.Background(), resolver, map[int64][]int64{
		1001: {2001},
		1002: {2002},
	})
	if !errors.Is(err, boom) || projected != nil {
		t.Fatalf("resolveSparseOutboxUsers = %+v, %v; want nil, boom", projected, err)
	}
	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want no split for non-capacity error", resolver.calls)
	}
}

func TestRouterBuildOutboxUpdatesFailsClosedAtSparseRecoveryCallBudget(t *testing.T) {
	const viewerID = int64(1000000050)
	base := &countingOutboxUsersService{users: make(map[int64]domain.User)}
	peers := make([]domain.Peer, 100)
	for i := range peers {
		id := int64(1000001000 + i)
		peers[i] = domain.Peer{Type: domain.PeerTypeUser, ID: id}
		base.users[id] = domain.User{ID: id, FirstName: "projected"}
	}
	users := &capacitySplittingMainOutboxUsers{
		countingOutboxUsersService: base,
		maxEdges:                   1,
		capacityErr:                store.ErrActiveChannelMemberPairsLimit,
	}
	router := New(Config{}, Deps{Users: users}, zaptest.NewLogger(t), clock.System)

	updates, err := router.BuildOutboxUpdates(context.Background(), []OutboxUpdateRequest{{
		TargetUserID: viewerID,
		Event: domain.UpdateEvent{
			UserID: viewerID,
			Type:   domain.UpdateEventPinnedDialogs,
			Peers:  peers,
		},
	}})
	if updates != nil || !errors.Is(err, ErrUserProjectionCapacityRecoveryLimit) {
		t.Fatalf("updates=%+v err=%v, want nil recovery-limit error", updates, err)
	}
	if errors.Is(err, store.ErrActiveChannelMemberPairsLimit) {
		t.Fatalf("recovery-limit error must not retain capacity identity: %v", err)
	}
	if len(users.sparseRequests) != maxSparseOutboxRecoveryCalls {
		t.Fatalf("sparse resolver calls=%d, want hard limit %d", len(users.sparseRequests), maxSparseOutboxRecoveryCalls)
	}
	if len(base.calls) != 0 {
		t.Fatalf("scalar ByIDs calls=%+v, want zero fallback", base.calls)
	}
}

func TestResolveSparseOutboxUsersRejectsAttemptedEdgeOverflowBeforeResolver(t *testing.T) {
	makeIDs := func(count int) []int64 {
		ids := make([]int64, count)
		for i := range ids {
			ids[i] = int64(i + 1)
		}
		return ids
	}

	t.Run("initial request", func(t *testing.T) {
		resolver := &failingSparseMainOutboxResolver{err: errors.New("must not be called")}
		projected, err := resolveSparseOutboxUsers(context.Background(), resolver, map[int64][]int64{
			1001: makeIDs(maxSparseOutboxAttemptedUserEdges + 1),
		})
		if projected != nil || !errors.Is(err, ErrUserProjectionCapacityRecoveryLimit) {
			t.Fatalf("projected=%+v err=%v, want nil recovery-limit error", projected, err)
		}
		if resolver.calls != 0 {
			t.Fatalf("resolver calls=%d, want rejection before first call", resolver.calls)
		}
		if isSparseProjectionCapacityError(err) {
			t.Fatalf("recovery-limit error must be terminal, got capacity identity: %v", err)
		}
	})

	t.Run("cumulative recursive edges", func(t *testing.T) {
		resolver := &failingSparseMainOutboxResolver{err: store.ErrActiveChannelMemberPairsLimit}
		projected, err := resolveSparseOutboxUsers(context.Background(), resolver, map[int64][]int64{
			1001: makeIDs(300000),
		})
		if projected != nil || !errors.Is(err, ErrUserProjectionCapacityRecoveryLimit) {
			t.Fatalf("projected=%+v err=%v, want nil recovery-limit error", projected, err)
		}
		if resolver.calls != 2 {
			t.Fatalf("resolver calls=%d, want root and first half before shared edge limit", resolver.calls)
		}
		if errors.Is(err, store.ErrActiveChannelMemberPairsLimit) {
			t.Fatalf("recovery-limit error must not retain capacity identity: %v", err)
		}
	})
}

func TestRouterBuildOutboxUpdatesRejectsIncompleteSparseProjection(t *testing.T) {
	const (
		viewerUserID  = int64(1000000010)
		missingUserID = int64(1000000011)
	)
	users := &countingOutboxUsersService{users: map[int64]domain.User{}}
	router := New(Config{}, Deps{Users: users}, zaptest.NewLogger(t), clock.System)
	updates, err := router.BuildOutboxUpdates(context.Background(), []OutboxUpdateRequest{{
		TargetUserID: viewerUserID,
		Event: domain.UpdateEvent{UserID: viewerUserID, Type: domain.UpdateEventNewMessage, Pts: 3, PtsCount: 1,
			Message: domain.Message{ID: 3, OwnerUserID: viewerUserID,
				Peer: domain.Peer{Type: domain.PeerTypeUser, ID: missingUserID},
				From: domain.Peer{Type: domain.PeerTypeUser, ID: missingUserID}}},
	}})
	if !errors.Is(err, ErrSparseOutboxUserProjectionIncomplete) || updates != nil {
		t.Fatalf("BuildOutboxUpdates=%+v err=%v, want incomplete fail-closed", updates, err)
	}
	if users.sparseCalls != 1 || len(users.calls) != 0 {
		t.Fatalf("projection calls = sparse %d scalar %+v, want one sparse call and no fallback", users.sparseCalls, users.calls)
	}
}

func TestRouterBuildOutboxUpdatesAllowsOnlyExplicitNoopToStayEmpty(t *testing.T) {
	router := New(Config{}, Deps{}, zaptest.NewLogger(t), clock.System)
	updates, err := router.BuildOutboxUpdates(context.Background(), []OutboxUpdateRequest{{
		TargetUserID: 1000000030,
		Event:        domain.UpdateEvent{UserID: 1000000030, Type: domain.UpdateEventNoop, Pts: 9, PtsCount: 1},
	}})
	if err != nil || len(updates) != 1 || updates[0] != nil {
		t.Fatalf("explicit noop updates=%+v err=%v, want one nil entry without error", updates, err)
	}

	updates, err = router.BuildOutboxUpdates(context.Background(), []OutboxUpdateRequest{{
		TargetUserID: 1000000030,
		Event:        domain.UpdateEvent{UserID: 1000000030, Type: domain.UpdateEventReadHistoryInbox, Pts: 10, PtsCount: 1},
	}})
	if !errors.Is(err, ErrOutboxUpdateProjectionEmpty) || updates != nil {
		t.Fatalf("invalid non-noop updates=%+v err=%v, want ErrOutboxUpdateProjectionEmpty", updates, err)
	}
}

func TestOutboxDispatcherFailsClosedOnBuilderErrorAndNilNonNoop(t *testing.T) {
	const userID = int64(1000000040)
	baseEvent := domain.UpdateEvent{
		UserID: userID, Type: domain.UpdateEventReadHistoryInbox, Pts: 10, PtsCount: 1,
		Peer: domain.Peer{Type: domain.PeerTypeUser, ID: 1000000041}, MaxID: 9,
	}
	tests := []struct {
		name    string
		builder OutboxUpdateBuilder
		wantErr error
	}{
		{
			name: "builder error",
			builder: func(context.Context, []OutboxUpdateRequest) ([]*tg.Updates, error) {
				return nil, errors.New("projection unavailable")
			},
		},
		{
			name: "nil non-noop",
			builder: func(_ context.Context, requests []OutboxUpdateRequest) ([]*tg.Updates, error) {
				return make([]*tg.Updates, len(requests)), nil
			},
			wantErr: errOutboxUpdateBuilderEmpty,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outbox := &captureDispatchOutbox{items: []store.DispatchOutboxItem{{
				ID: 71, TargetUserID: userID, Pts: baseEvent.Pts, EventType: baseEvent.Type,
			}}}
			sessions := &captureSessions{}
			dispatcher := newTestOutboxDispatcher(
				&captureUpdateEventStore{events: []domain.UpdateEvent{baseEvent}},
				outbox,
				sessions,
				zaptest.NewLogger(t),
				WithOutboxUpdateBuilder(tt.builder),
			)
			dispatcher.DispatchOnce(context.Background())
			if !outbox.failed || outbox.delivered || sessions.message != nil {
				t.Fatalf("failed=%v delivered=%v pushed=%T, want terminal failure without delivery", outbox.failed, outbox.delivered, sessions.message)
			}
			if tt.wantErr != nil && !strings.Contains(outbox.failedError, tt.wantErr.Error()) {
				t.Fatalf("failed error=%q, want %v", outbox.failedError, tt.wantErr)
			}
		})
	}
}
