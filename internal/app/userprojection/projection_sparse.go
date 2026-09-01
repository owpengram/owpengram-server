package userprojection

import (
	"context"
	"errors"

	"golang.org/x/sync/errgroup"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

var (
	ErrSparseContactProjectionUnsupported = errors.New("sparse contact projection is not supported")
	ErrSparsePrivacyProjectionUnsupported = errors.New("sparse privacy projection is not supported")
)

// SparsePrivacyEvaluator evaluates only the supplied viewer->owner pairs. The
// inverse contact rows are supplied from the projector's shared sparse contact
// read so privacy does not issue another contact query.
type SparsePrivacyEvaluator interface {
	CanSeeForViewerUserIDs(
		ctx context.Context,
		ownerUserIDsByViewer map[int64][]int64,
		keys []domain.PrivacyKey,
		contactsByOwner map[int64]map[int64]domain.Contact,
	) (map[int64]map[int64]map[domain.PrivacyKey]bool, error)
}

// ForViewerUserIDs projects a sparse viewer->owner graph. Viewer-independent
// facts are loaded for the union once; viewer-specific facts are read only for
// graph edges that occur in the request (plus their inverse contact edge needed
// by privacy evaluation).
func (p *Projector) ForViewerUserIDs(ctx context.Context, userIDsByViewer map[int64][]int64, baseUsers []domain.User) (map[int64][]domain.User, error) {
	requested := normalizeSparseUserIDs(userIDsByViewer)
	out := make(map[int64][]domain.User, len(requested))
	if len(requested) == 0 {
		return out, nil
	}
	baseUsers = sanitizeDeletedUsers(baseUsers)
	baseByID := make(map[int64]domain.User, len(baseUsers))
	for _, user := range baseUsers {
		if user.ID != 0 {
			baseByID[user.ID] = user
		}
	}
	if p == nil {
		for viewerID, ids := range requested {
			out[viewerID] = sparseBaseUsers(ids, baseByID)
		}
		return out, nil
	}
	unionIDs := make([]int64, 0, len(baseByID))
	seenUnion := make(map[int64]struct{}, len(baseByID))
	contactPairs := make(map[int64][]int64)
	privacyPairs := make(map[int64][]int64)
	for viewerID, ids := range requested {
		for _, ownerID := range ids {
			user, found := baseByID[ownerID]
			if !found {
				continue
			}
			if _, ok := seenUnion[ownerID]; !ok && !user.Deleted {
				seenUnion[ownerID] = struct{}{}
				unionIDs = append(unionIDs, ownerID)
			}
			if user.Deleted || ownerID == viewerID {
				continue
			}
			// Personal-photo overlay applies independently of contact/privacy
			// exemptions, so retain every real viewer->owner edge here.
			contactPairs[viewerID] = append(contactPairs[viewerID], ownerID)
			if ownerID == domain.OfficialSystemUserID || user.Bot {
				continue
			}
			privacyPairs[viewerID] = append(privacyPairs[viewerID], ownerID)
			// Privacy's ViewerIsContact is the inverse owner->viewer row. Merge it
			// into the same exact-pair store call.
			contactPairs[ownerID] = append(contactPairs[ownerID], viewerID)
		}
	}

	var (
		profileRefs  map[int64]domain.ProfilePhotoRef
		fallbackRefs map[int64]domain.ProfilePhotoRef
		contactBatch domain.ContactProjectionBatch
		visibility   map[int64]map[int64]map[domain.PrivacyKey]bool
		freezes      map[int64]domain.AccountFreeze
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		profileRefs, fallbackRefs, err = p.batchProfileFallbackPhotos(gctx, unionIDs)
		return err
	})
	if p.contacts != nil && len(contactPairs) > 0 {
		g.Go(func() error {
			loader, ok := p.contacts.(store.SparseContactProjectionStore)
			if !ok {
				return ErrSparseContactProjectionUnsupported
			}
			var err error
			contactBatch, err = loader.ContactProjectionForViewerUserIDs(gctx, contactPairs)
			return err
		})
	}
	if p.freezes != nil && len(unionIDs) > 0 {
		g.Go(func() error {
			var err error
			freezes, err = p.freezes.AccountFreezes(gctx, unionIDs)
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	if p.privacy != nil && len(privacyPairs) > 0 {
		evaluator, ok := p.privacy.(SparsePrivacyEvaluator)
		if !ok {
			return nil, ErrSparsePrivacyProjectionUnsupported
		}
		var err error
		visibility, err = evaluator.CanSeeForViewerUserIDs(ctx, privacyPairs, privacyProjectionKeys, contactBatch.Contacts)
		if err != nil {
			return nil, err
		}
	}

	for viewerID, ids := range requested {
		projected := sparseBaseUsers(ids, baseByID)
		personalRefs := contactBatch.PersonalPhotos[viewerID]
		for i := range projected {
			user := projected[i]
			if user.Deleted {
				projected[i] = user.DeletedTombstone()
				continue
			}
			user = applyBasePhotos(user, profileRefs, fallbackRefs, personalRefs, viewerID)
			if viewerID != 0 && user.ID != viewerID && user.ID != domain.OfficialSystemUserID && !user.Bot {
				contact, found := contactBatch.Contacts[viewerID][user.ID]
				user = applyContactProjection(user, contact, found)
				var vis map[domain.PrivacyKey]bool
				if visibility != nil {
					vis = visibility[user.ID][viewerID]
				}
				var err error
				user, err = applyPrivacy(ctx, p.privacy, viewerID, user, found && contact.Phone != "", vis, profileRefs, fallbackRefs, personalRefs)
				if err != nil {
					return nil, err
				}
			}
			user = applyAccountFreezeProjection(user, viewerID, freezes[user.ID])
			projected[i] = user
		}
		out[viewerID] = projected
	}
	return out, nil
}

func normalizeSparseUserIDs(in map[int64][]int64) map[int64][]int64 {
	out := make(map[int64][]int64, len(in))
	for viewerID, ids := range in {
		if viewerID == 0 {
			continue
		}
		out[viewerID] = dedupNonZeroInt64(ids)
	}
	return out
}

func sparseBaseUsers(ids []int64, baseByID map[int64]domain.User) []domain.User {
	users := make([]domain.User, 0, len(ids))
	for _, id := range ids {
		if user, ok := baseByID[id]; ok {
			users = append(users, user)
		}
	}
	return cloneUsers(users)
}
