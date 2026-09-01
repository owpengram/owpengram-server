package rpc

import (
	"context"
	"time"

	"github.com/iamxvbaba/td/proto"
	"github.com/iamxvbaba/td/tg"
	"github.com/iamxvbaba/td/tlprofile"
	"go.uber.org/zap"

	"telesrv/internal/store"
)

const ephemeralPushSubscribeRetry = time.Second

func (r *Router) RunEphemeralPushSubscriber(ctx context.Context) {
	if r == nil || r.deps.EphemeralPush == nil {
		return
	}
	for {
		err := r.deps.EphemeralPush.SubscribeEphemeralPushes(ctx, func(ctx context.Context, event store.EphemeralPush) {
			if event.SourceID == "" || event.SourceID == r.instanceID {
				return
			}
			r.deliverEphemeralPushLocal(ctx, event)
		})
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			r.log.Warn("ephemeral push subscriber stopped", zap.Error(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(ephemeralPushSubscribeRetry):
		}
	}
}

func (r *Router) publishEphemeralPush(ctx context.Context, event store.EphemeralPush) {
	if r == nil || event.TargetUserID <= 0 {
		return
	}
	event.SourceID = r.instanceID
	if event.Date <= 0 {
		event.Date = int(r.clock.Now().Unix())
	}
	r.deliverEphemeralPushLocal(ctx, event)
	if r.deps.EphemeralPush != nil {
		if err := r.deps.EphemeralPush.PublishEphemeralPush(ctx, event); err != nil {
			r.log.Debug("publish ephemeral push", zap.String("kind", string(event.Kind)), zap.Int64("target_user_id", event.TargetUserID), zap.Error(err))
		}
	}
}

func (r *Router) deliverEphemeralPushLocal(ctx context.Context, event store.EphemeralPush) {
	if r == nil || r.deps.Sessions == nil || event.TargetUserID <= 0 || event.Message.ID <= 0 {
		return
	}
	if online, ok := r.deps.Sessions.(OnlineUserProvider); ok && !online.IsUserOnline(event.TargetUserID) {
		return
	}
	binder, ok := r.deps.Sessions.(SemanticTransientSessionBinder)
	if !ok {
		return
	}
	var updates tg.UpdatesClass
	var semantic tlprofile.SemanticID
	switch event.Kind {
	case store.EphemeralPushNew, store.EphemeralPushEdit:
		if event.TargetUserID != event.Message.ReceiverUserID || event.Message.Deleted {
			return
		}
		built, err := r.ephemeralMessageUpdates(ctx, event.TargetUserID, event.Message, event.Kind == store.EphemeralPushEdit)
		if err != nil {
			return
		}
		updates = built
		semantic = tlprofile.SemanticTypeUpdateNewEphemeralMessage
		if event.Kind == store.EphemeralPushEdit {
			semantic = tlprofile.SemanticTypeUpdateEditEphemeralMessage
		}
	case store.EphemeralPushDelete:
		if !event.Message.Deleted || (event.TargetUserID != event.Message.SenderUserID && event.TargetUserID != event.Message.ReceiverUserID) {
			return
		}
		updates = ephemeralDeleteUpdates(event.Message, event.Date)
		semantic = tlprofile.SemanticTypeUpdateDeleteEphemeralMessages
	case store.EphemeralPushCallback:
		callback := event.Callback
		if callback == nil || callback.BotUserID != event.TargetUserID || callback.MessageID != event.Message.ID || callback.Peer != event.Message.Peer {
			return
		}
		update := &tg.UpdateBotCallbackQuery{
			QueryID: callback.ID, UserID: callback.UserID, Peer: tgPeer(callback.Peer),
			MsgID: callback.MessageID, ChatInstance: callback.ChatInstance,
		}
		update.SetData(callback.Data)
		updates = &tg.Updates{Updates: []tg.UpdateClass{update}, Date: event.Date}
		semantic = tlprofile.SemanticTypeUpdateBotCallbackQuery
	default:
		return
	}
	if event.TargetBusinessAuthKey != ([8]byte{}) {
		_, _ = binder.PushToUserAuthKeyTransientCompatible(ctx, event.TargetUserID, event.TargetBusinessAuthKey, semantic, proto.MessageFromServer, updates, r.cfg.OutboundPushTimeout)
		return
	}
	_, _ = binder.PushToUserTransientCompatible(ctx, event.TargetUserID, semantic, proto.MessageFromServer, updates, r.cfg.OutboundPushTimeout)
}
