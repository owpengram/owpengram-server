package users

import (
	"context"
	"fmt"

	"telesrv/internal/domain"
)

// ByIDsForViewerUserIDs projects an actual sparse viewer->owner graph. Base
// users are loaded once for the union; unlike ByIDsForViewers, owners belonging
// to one viewer are never implicitly projected for every other viewer.
func (s *Service) ByIDsForViewerUserIDs(ctx context.Context, userIDsByViewer map[int64][]int64) (map[int64][]domain.User, error) {
	requested := make(map[int64][]int64, len(userIDsByViewer))
	union := make([]int64, 0)
	seenUnion := make(map[int64]struct{})
	pairs := 0
	for viewerID, userIDs := range userIDsByViewer {
		if viewerID == 0 {
			continue
		}
		ids := uniqueUserIDs(userIDs, 0)
		if !sparseViewerProjectionPairsAllowed(pairs, len(ids)) {
			return nil, fmt.Errorf("%w: got more than %d sparse pairs", ErrBatchViewerCells, maxBatchViewerProjectionCells)
		}
		pairs += len(ids)
		requested[viewerID] = ids
		for _, id := range ids {
			if _, ok := seenUnion[id]; ok {
				continue
			}
			seenUnion[id] = struct{}{}
			union = append(union, id)
			if len(union) > maxBatchUsers {
				return nil, ErrBatchUsersLimit
			}
		}
	}
	if len(requested) == 0 || len(union) == 0 {
		return map[int64][]domain.User{}, nil
	}
	base, err := s.loadBaseUsersByIDs(ctx, union)
	if err != nil {
		return nil, err
	}
	base, err = requireBatchBaseUsers(union, base)
	if err != nil {
		return nil, err
	}
	return s.projector.ForViewerUserIDs(ctx, requested, base)
}

func sparseViewerProjectionPairsAllowed(current, additional int) bool {
	if current < 0 || additional < 0 || current > maxBatchViewerProjectionCells {
		return false
	}
	return additional <= maxBatchViewerProjectionCells-current
}
