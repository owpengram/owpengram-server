package rpc

import (
	"context"

	"github.com/iamxvbaba/td/tg"

	"telesrv/internal/domain"
)

func (r *Router) suggestedPostApprovalUpdatesStrict(ctx context.Context, viewerUserID int64, result domain.ToggleSuggestedPostApprovalResult) (*tg.Updates, error) {
	return r.suggestedPostApprovalUpdatesWithPeerCacheAndOverlaysStrict(ctx, viewerUserID, result, nil, nil)
}

func (r *Router) suggestedPostApprovalUpdatesWithPeerCacheAndOverlays(ctx context.Context, viewerUserID int64, result domain.ToggleSuggestedPostApprovalResult, cache *viewerPeerCache, overlays *monoforumPeerOverlays) *tg.Updates {
	updates, _ := r.suggestedPostApprovalUpdatesWithPeerCacheAndOverlaysStrict(ctx, viewerUserID, result, cache, overlays)
	return updates
}

func (r *Router) suggestedPostApprovalUpdatesWithPeerCacheAndOverlaysStrict(ctx context.Context, viewerUserID int64, result domain.ToggleSuggestedPostApprovalResult, cache *viewerPeerCache, overlays *monoforumPeerOverlays) (*tg.Updates, error) {
	updates := make([]tg.UpdateClass, 0, 4)
	if result.OriginalEvent.Pts > 0 {
		if update := tgChannelUpdate(viewerUserID, result.OriginalEvent); update != nil {
			updates = append(updates, update)
		}
	}
	if result.ServiceEvent.Pts > 0 {
		if update := tgChannelUpdate(viewerUserID, result.ServiceEvent); update != nil {
			updates = append(updates, update)
		}
	}
	if result.Published != nil && result.Published.Event.Pts > 0 {
		if update := tgChannelUpdate(viewerUserID, result.Published.Event); update != nil {
			updates = append(updates, update)
		}
	}
	chats := r.monoforumChats(ctx, viewerUserID, result.Monoforum)
	if result.Parent.ID != 0 {
		chats = appendUniqueTGChats(chats, tgChannelChatMin(viewerUserID, result.Parent))
	}
	messages := make([]domain.ChannelMessage, 0, 3)
	if result.OriginalMessage.ID != 0 {
		messages = append(messages, result.OriginalMessage)
	}
	if result.ServiceMessage.ID != 0 {
		messages = append(messages, result.ServiceMessage)
	}
	if result.Published != nil {
		messages = append(messages, result.Published.Message)
	}
	if cache == nil {
		cache = newViewerPeerCache(r)
	}
	if overlays == nil {
		ids := monoforumSubscriberUserIDs([]domain.MonoforumDialog{{SavedPeer: result.SavedPeer}}, messages)
		overlays = r.loadMonoforumPeerOverlays(ctx, monoforumProjectionPeers(result.Monoforum.ID, result.Parent.ID, ids))
	}
	users, err := r.monoforumSubscriberUsersWithPeerCacheAndOverlaysStrict(ctx, viewerUserID, []domain.MonoforumDialog{{SavedPeer: result.SavedPeer}}, messages, cache, overlays)
	if err != nil {
		return nil, err
	}
	applyMonoforumPeerOverlays(nil, chats, overlays)
	return &tg.Updates{
		Updates: updates,
		Chats:   chats,
		Users:   users,
		Date:    int(r.clock.Now().Unix()),
	}, nil
}

func (r *Router) enqueueSuggestedPostApprovalFanout(ctx context.Context, originUserID int64, result domain.ToggleSuggestedPostApprovalResult) {
	monoOnly := result
	monoOnly.Published = nil
	nudge := max(result.OriginalEvent.Pts, result.ServiceEvent.Pts)
	if nudge > 0 {
		messages := make([]domain.ChannelMessage, 0, 2)
		if monoOnly.OriginalMessage.ID != 0 {
			messages = append(messages, monoOnly.OriginalMessage)
		}
		if monoOnly.ServiceMessage.ID != 0 {
			messages = append(messages, monoOnly.ServiceMessage)
		}
		ownerIDs := monoforumSubscriberUserIDs([]domain.MonoforumDialog{{SavedPeer: monoOnly.SavedPeer}}, messages)
		fanoutCache := newViewerPeerCache(r)
		projectionPeers := monoforumProjectionPeers(monoOnly.Monoforum.ID, monoOnly.Parent.ID, ownerIDs)
		var overlays *monoforumPeerOverlays
		r.enqueueChannelFanoutWithPrefetch(ctx, channelFanoutExplicit, originUserID, result.Monoforum.ID, nudge, result.Recipients,
			0,
			func(bgCtx context.Context, viewers []int64) bool {
				if !r.prefetchChannelFanoutUsers(bgCtx, fanoutCache, viewers, ownerIDs) {
					return false
				}
				overlays = r.loadMonoforumPeerOverlays(bgCtx, projectionPeers)
				return true
			},
			func(bgCtx context.Context, viewerUserID int64) *tg.Updates {
				return r.suggestedPostApprovalUpdatesWithPeerCacheAndOverlays(bgCtx, viewerUserID, monoOnly, fanoutCache, overlays)
			})
	}
	if result.Published != nil && result.Published.Event.Pts > 0 {
		r.enqueueChannelMessageFanout(ctx, originUserID, *result.Published, nil)
	}
}
