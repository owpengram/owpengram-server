package rpc

import (
	"context"
	"telesrv/internal/domain"
)

type staticUsersService struct {
	user domain.User
}

type mapUsersService struct {
	users map[int64]domain.User
}

type countingMapUsersService struct {
	mapUsersService
	selfCalls    int
	byIDCalls    int
	byIDsCalls   int
	lastByIDs    []int64
	byIDsBatches [][]int64
}

func (s staticUsersService) Self(context.Context, int64) (domain.User, error) {
	return s.user, nil
}

func (s staticUsersService) ByID(_ context.Context, _, userID int64) (domain.User, bool, error) {
	if userID == s.user.ID {
		return s.user, true, nil
	}
	if userID == domain.OfficialSystemUserID {
		return domain.OfficialSystemUser(), true, nil
	}
	return domain.User{}, false, nil
}

func (s staticUsersService) BotStatus(_ context.Context, userID int64) (bool, bool, error) {
	u, found, err := s.ByID(context.Background(), userID, userID)
	return u.Bot, found, err
}

func (s staticUsersService) ByIDs(_ context.Context, _ int64, userIDs []int64) ([]domain.User, error) {
	out := make([]domain.User, 0, len(userIDs))
	seen := map[int64]struct{}{}
	for _, userID := range userIDs {
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		if userID == s.user.ID {
			out = append(out, s.user)
		} else if userID == domain.OfficialSystemUserID {
			out = append(out, domain.OfficialSystemUser())
		}
	}
	return out, nil
}

func (s mapUsersService) Self(_ context.Context, userID int64) (domain.User, error) {
	return s.users[userID], nil
}

func (s mapUsersService) ByID(_ context.Context, _, userID int64) (domain.User, bool, error) {
	u, ok := s.users[userID]
	return u, ok, nil
}

func (s mapUsersService) BotStatus(_ context.Context, userID int64) (bool, bool, error) {
	u, found := s.users[userID]
	return u.Bot, found, nil
}

func (s mapUsersService) ByIDs(_ context.Context, _ int64, userIDs []int64) ([]domain.User, error) {
	out := make([]domain.User, 0, len(userIDs))
	seen := map[int64]struct{}{}
	for _, userID := range userIDs {
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		if u, ok := s.users[userID]; ok {
			out = append(out, u)
		}
	}
	return out, nil
}

func (s *countingMapUsersService) ByID(ctx context.Context, currentUserID, userID int64) (domain.User, bool, error) {
	s.byIDCalls++
	return s.mapUsersService.ByID(ctx, currentUserID, userID)
}

func (s *countingMapUsersService) Self(ctx context.Context, userID int64) (domain.User, error) {
	s.selfCalls++
	return s.mapUsersService.Self(ctx, userID)
}

func (s *countingMapUsersService) ByIDs(ctx context.Context, currentUserID int64, userIDs []int64) ([]domain.User, error) {
	s.byIDsCalls++
	s.lastByIDs = append([]int64(nil), userIDs...)
	s.byIDsBatches = append(s.byIDsBatches, append([]int64(nil), userIDs...))
	return s.mapUsersService.ByIDs(ctx, currentUserID, userIDs)
}

type captureUsersService struct {
	user   domain.User
	userID int64
}

func (s *captureUsersService) Self(_ context.Context, userID int64) (domain.User, error) {
	s.userID = userID
	return s.user, nil
}

func (s *captureUsersService) ByID(_ context.Context, currentUserID, userID int64) (domain.User, bool, error) {
	s.userID = currentUserID
	if userID == s.user.ID {
		return s.user, true, nil
	}
	return domain.User{}, false, nil
}

func (s *captureUsersService) ByIDs(_ context.Context, currentUserID int64, userIDs []int64) ([]domain.User, error) {
	s.userID = currentUserID
	out := make([]domain.User, 0, len(userIDs))
	for _, userID := range userIDs {
		if userID == s.user.ID {
			out = append(out, s.user)
		}
	}
	return out, nil
}
