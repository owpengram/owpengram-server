package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/iamxvbaba/td/clock"
	"github.com/iamxvbaba/td/tg"
	"go.uber.org/zap/zaptest"

	appusers "telesrv/internal/app/users"
	"telesrv/internal/domain"
	"telesrv/internal/store/memory"
)

type fakeAccountRatingProjection struct {
	byUser      map[int64]domain.AccountRating
	err         error
	ratingCalls int
	ensureCalls int
}

func (f *fakeAccountRatingProjection) Rating(_ context.Context, userID int64) (domain.AccountRating, error) {
	f.ratingCalls++
	if f.err != nil {
		return domain.AccountRating{}, f.err
	}
	rating, ok := f.byUser[userID]
	if !ok {
		return domain.AccountRating{}, domain.ErrAccountRatingNotFound
	}
	return rating, nil
}

// EnsureRating deliberately exists on the fake even though it is not part of
// AccountRatingService. The assertion below pins that profile reads stay
// read-only if a future implementation happens to expose a materializer.
func (f *fakeAccountRatingProjection) EnsureRating(_ context.Context, userID int64) (domain.AccountRating, error) {
	f.ensureCalls++
	return f.byUser[userID], nil
}

var _ AccountRatingService = (*fakeAccountRatingProjection)(nil)

func newAccountRatingProjectionFixture(t *testing.T, ratings AccountRatingService) (*Router, domain.User, domain.User) {
	t.Helper()
	ctx := context.Background()
	userStore := memory.NewUserStore()
	owner, err := userStore.Create(ctx, domain.User{
		AccessHash: 11,
		Phone:      "15550004001",
		FirstName:  "Owner",
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	other, err := userStore.Create(ctx, domain.User{
		AccessHash: 22,
		Phone:      "15550004002",
		FirstName:  "Other",
	})
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	router := New(Config{}, Deps{
		Users:          appusers.NewService(userStore),
		AccountRatings: ratings,
	}, zaptest.NewLogger(t), clock.System)
	return router, owner, other
}

func TestUserFullProjectsCompositeRatingReadOnlyAndCachesIt(t *testing.T) {
	pendingDate := time.Unix(1800000000, 0).UTC()
	ratings := &fakeAccountRatingProjection{byUser: make(map[int64]domain.AccountRating)}
	router, owner, _ := newAccountRatingProjectionFixture(t, ratings)
	ratings.byUser[owner.ID] = domain.AccountRating{
		UserID:            owner.ID,
		Level:             3,
		Stars:             1200,
		CurrentLevelStars: domain.AccountRatingLevelThreshold(3),
		NextLevelStars:    domain.AccountRatingLevelThreshold(4),
		HasNextLevel:      true,
		PendingStars:      500,
		PendingDate:       pendingDate,
	}
	ctx := WithUserID(context.Background(), owner.ID)

	full, err := router.onUsersGetFullUser(ctx, &tg.InputUserSelf{})
	if err != nil {
		t.Fatalf("get self full user: %v", err)
	}
	rating, ok := full.FullUser.GetStarsRating()
	if !ok || rating.Level != 3 || rating.Stars != 1200 ||
		rating.CurrentLevelStars != domain.AccountRatingLevelThreshold(3) {
		t.Fatalf("self rating = %+v (present=%v)", rating, ok)
	}
	if next, ok := rating.GetNextLevelStars(); !ok || next != domain.AccountRatingLevelThreshold(4) {
		t.Fatalf("self next_level_stars = %d (present=%v)", next, ok)
	}
	pending, ok := full.FullUser.GetStarsMyPendingRating()
	if !ok || pending.Stars != 1700 {
		t.Fatalf("self pending rating = %+v (present=%v)", pending, ok)
	}
	if date, ok := full.FullUser.GetStarsMyPendingRatingDate(); !ok || date != int(pendingDate.Unix()) {
		t.Fatalf("self pending date = %d (present=%v)", date, ok)
	}
	if ratings.ratingCalls != 1 || ratings.ensureCalls != 0 {
		t.Fatalf("rating calls=%d ensure calls=%d, want 1/0", ratings.ratingCalls, ratings.ensureCalls)
	}

	// The second response is served from the existing UserFull projection cache:
	// the rating read cannot become a per-request database query.
	if _, err := router.onUsersGetFullUser(ctx, &tg.InputUserSelf{}); err != nil {
		t.Fatalf("get cached self full user: %v", err)
	}
	if ratings.ratingCalls != 1 || ratings.ensureCalls != 0 {
		t.Fatalf("cached rating calls=%d ensure calls=%d, want 1/0", ratings.ratingCalls, ratings.ensureCalls)
	}
}

func TestUserFullRatingPendingIsSelfOnly(t *testing.T) {
	pendingDate := time.Unix(1800000000, 0).UTC()
	ratings := &fakeAccountRatingProjection{byUser: make(map[int64]domain.AccountRating)}
	router, owner, other := newAccountRatingProjectionFixture(t, ratings)
	ratings.byUser[other.ID] = domain.AccountRating{
		UserID:            other.ID,
		Level:             1,
		Stars:             150,
		CurrentLevelStars: domain.AccountRatingLevelThreshold(1),
		NextLevelStars:    domain.AccountRatingLevelThreshold(2),
		HasNextLevel:      true,
		PendingStars:      500,
		PendingDate:       pendingDate,
	}

	full, err := router.onUsersGetFullUser(
		WithUserID(context.Background(), owner.ID),
		&tg.InputUser{UserID: other.ID, AccessHash: other.AccessHash},
	)
	if err != nil {
		t.Fatalf("get other full user: %v", err)
	}
	if rating, ok := full.FullUser.GetStarsRating(); !ok || rating.Level != 1 || rating.Stars != 150 {
		t.Fatalf("other rating = %+v (present=%v)", rating, ok)
	}
	if _, ok := full.FullUser.GetStarsMyPendingRating(); ok {
		t.Fatal("other pending rating is visible")
	}
	if _, ok := full.FullUser.GetStarsMyPendingRatingDate(); ok {
		t.Fatal("other pending rating date is visible")
	}
}

func TestUserFullRatingDegradesWithoutStoredProjection(t *testing.T) {
	tests := []struct {
		name    string
		ratings AccountRatingService
	}{
		{name: "service absent"},
		{name: "row missing", ratings: &fakeAccountRatingProjection{byUser: map[int64]domain.AccountRating{}}},
		{name: "read failure", ratings: &fakeAccountRatingProjection{
			byUser: map[int64]domain.AccountRating{},
			err:    errors.New("rating unavailable"),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, owner, _ := newAccountRatingProjectionFixture(t, tt.ratings)
			full, err := router.onUsersGetFullUser(
				WithUserID(context.Background(), owner.ID),
				&tg.InputUserSelf{},
			)
			if err != nil {
				t.Fatalf("get full user: %v", err)
			}
			if _, ok := full.FullUser.GetStarsRating(); ok {
				t.Fatal("rating set without a stored projection")
			}
			if _, ok := full.FullUser.GetStarsMyPendingRating(); ok {
				t.Fatal("pending rating set without a stored projection")
			}
		})
	}
}

func TestUserFullRatingOmitsBotsAndTopLevelThreshold(t *testing.T) {
	ctx := context.Background()
	userStore := memory.NewUserStore()
	viewer, err := userStore.Create(ctx, domain.User{
		AccessHash: 31,
		Phone:      "15550004101",
		FirstName:  "Viewer",
	})
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	bot, err := userStore.Create(ctx, domain.User{
		AccessHash: 32,
		Phone:      "15550004102",
		FirstName:  "Helper",
		Bot:        true,
	})
	if err != nil {
		t.Fatalf("create bot: %v", err)
	}
	ratings := &fakeAccountRatingProjection{byUser: map[int64]domain.AccountRating{
		viewer.ID: {
			UserID:            viewer.ID,
			Level:             domain.MaxAccountRatingLevel,
			Stars:             domain.AccountRatingLevelThreshold(domain.MaxAccountRatingLevel),
			CurrentLevelStars: domain.AccountRatingLevelThreshold(domain.MaxAccountRatingLevel),
		},
		bot.ID: {
			UserID: bot.ID,
			Level:  4,
			Stars:  2000,
		},
		domain.OfficialSystemUserID: {
			UserID: domain.OfficialSystemUserID,
			Level:  5,
			Stars:  3000,
		},
	}}
	router := New(Config{}, Deps{
		Users:          appusers.NewService(userStore),
		AccountRatings: ratings,
	}, zaptest.NewLogger(t), clock.System)
	viewerCtx := WithUserID(ctx, viewer.ID)

	own, err := router.onUsersGetFullUser(viewerCtx, &tg.InputUserSelf{})
	if err != nil {
		t.Fatalf("get own full user: %v", err)
	}
	rating, ok := own.FullUser.GetStarsRating()
	if !ok || rating.Level != domain.MaxAccountRatingLevel {
		t.Fatalf("top-level rating = %+v (present=%v)", rating, ok)
	}
	if _, ok := rating.GetNextLevelStars(); ok {
		t.Fatal("next_level_stars set at the maximum level")
	}

	botFull, err := router.onUsersGetFullUser(
		viewerCtx,
		&tg.InputUser{UserID: bot.ID, AccessHash: bot.AccessHash},
	)
	if err != nil {
		t.Fatalf("get bot full user: %v", err)
	}
	if _, ok := botFull.FullUser.GetStarsRating(); ok {
		t.Fatal("bot rating is visible")
	}

	official, err := router.onUsersGetFullUser(
		viewerCtx,
		&tg.InputUser{
			UserID:     domain.OfficialSystemUserID,
			AccessHash: domain.OfficialSystemUser().AccessHash,
		},
	)
	if err != nil {
		t.Fatalf("get official system user: %v", err)
	}
	if _, ok := official.FullUser.GetStarsRating(); ok {
		t.Fatal("system account rating is visible")
	}
	// One read for the ratable viewer and none for the bot/system guards.
	if ratings.ratingCalls != 1 {
		t.Fatalf("rating calls = %d, want 1", ratings.ratingCalls)
	}
}
