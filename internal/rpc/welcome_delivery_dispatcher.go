package rpc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/iamxvbaba/td/proto"
	"github.com/iamxvbaba/td/tg"
	"github.com/iamxvbaba/td/tlprofile"
	"go.uber.org/zap"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

const (
	defaultWelcomeDeliveryBatch    = 100
	defaultWelcomeDeliveryLease    = 15 * time.Second
	defaultWelcomeDeliveryInterval = 250 * time.Millisecond
	defaultWelcomeDeliveryMaxRetry = time.Minute
	defaultWelcomeDeliverySweep    = time.Minute
)

var errWelcomeDeliveryMembershipSuperseded = errors.New("welcome delivery membership epoch is no longer active")

type WelcomeDeliveryDispatcher struct {
	router    *Router
	store     store.WelcomeMessageDeliveryStore
	log       *zap.Logger
	owner     string
	batch     int
	lease     time.Duration
	nextSweep time.Time
}

func NewWelcomeDeliveryDispatcher(router *Router, deliveries store.WelcomeMessageDeliveryStore, log *zap.Logger) *WelcomeDeliveryDispatcher {
	if log == nil {
		log = zap.NewNop()
	}
	return &WelcomeDeliveryDispatcher{
		router: router, store: deliveries, log: log,
		owner: welcomeDeliveryOwner(), batch: defaultWelcomeDeliveryBatch,
		lease: defaultWelcomeDeliveryLease,
	}
}

func (d *WelcomeDeliveryDispatcher) Run(ctx context.Context) {
	if d == nil || d.router == nil || d.store == nil {
		return
	}
	runIdleBackoffLoop(ctx, defaultWelcomeDeliveryInterval, defaultIdleDispatchMaxInterval, d.DispatchOnce)
}

func (d *WelcomeDeliveryDispatcher) DispatchOnce(ctx context.Context) bool {
	if d == nil || d.router == nil || d.store == nil {
		return false
	}
	now := d.router.clock.Now()
	deleted := 0
	if d.nextSweep.IsZero() || !now.Before(d.nextSweep) {
		var err error
		deleted, err = d.store.DeleteExpiredWelcomeMessageDeliveries(ctx, now, d.batch*10)
		switch {
		case err != nil:
			d.nextSweep = now.Add(defaultWelcomeDeliveryInterval)
			d.log.Warn("delete expired welcome deliveries", zap.Error(err))
		case deleted == d.batch*10:
			d.nextSweep = now
		default:
			d.nextSweep = now.Add(defaultWelcomeDeliverySweep)
		}
	}
	deliveries, err := d.store.ClaimWelcomeMessageDeliveries(ctx, d.owner, now, d.batch, d.lease)
	if err != nil {
		d.log.Warn("claim welcome deliveries", zap.Error(err))
		return deleted > 0
	}
	for _, group := range groupWelcomeMessageDeliveries(deliveries) {
		d.dispatchGroup(ctx, group)
	}
	return deleted > 0 || len(deliveries) > 0
}

func (d *WelcomeDeliveryDispatcher) dispatchGroup(ctx context.Context, deliveries []domain.WelcomeMessageDelivery) {
	if len(deliveries) == 0 {
		return
	}
	delivery := deliveries[0]
	ids := welcomeDeliveryIDs(deliveries)
	now := d.router.clock.Now()
	if !delivery.ExpiresAt.After(now) {
		return
	}
	if online, ok := d.router.deps.Sessions.(OnlineUserProvider); ok && !online.IsUserOnline(delivery.TargetUserID) {
		d.retry(ctx, deliveries, now, "target has no online session")
		return
	}
	binder, ok := d.router.deps.Sessions.(SemanticTransientSessionBinder)
	if !ok {
		d.retry(ctx, deliveries, now, "semantic transient session binder is unavailable")
		return
	}
	updates, err := d.router.welcomeDeliveryUpdates(ctx, deliveries)
	if errors.Is(err, errWelcomeDeliveryMembershipSuperseded) {
		if _, ackErr := d.store.AckWelcomeMessageDeliveries(ctx, d.owner, ids, now); ackErr != nil {
			d.log.Warn("discard superseded welcome delivery", zap.Int64("join_event_id", delivery.JoinEventID), zap.Error(ackErr))
		}
		return
	}
	if err != nil {
		d.retry(ctx, deliveries, now, err.Error())
		return
	}
	sent, sendErr := binder.PushToUserTransientCompatible(
		ctx, delivery.TargetUserID, tlprofile.SemanticTypeUpdateNewEphemeralMessage,
		proto.MessageFromServer, updates, d.router.cfg.OutboundPushTimeout,
	)
	if sent <= 0 {
		reason := "no ready exact profile can represent welcome delivery"
		if sendErr != nil {
			reason = sendErr.Error()
		}
		d.retry(ctx, deliveries, now, reason)
		return
	}
	acked, err := d.store.AckWelcomeMessageDeliveries(ctx, d.owner, ids, now)
	if err != nil || acked != len(ids) {
		d.log.Warn("ack welcome delivery",
			zap.Int64("join_event_id", delivery.JoinEventID), zap.Int64("target_user_id", delivery.TargetUserID),
			zap.Int("templates", len(ids)), zap.Int("sent_sessions", sent), zap.Int("acked", acked), zap.Error(err))
	}
}

func (d *WelcomeDeliveryDispatcher) retry(ctx context.Context, deliveries []domain.WelcomeMessageDelivery, now time.Time, reason string) {
	if len(deliveries) == 0 {
		return
	}
	delivery := deliveries[0]
	attempt := delivery.AttemptCount
	expiresAt := delivery.ExpiresAt
	for _, item := range deliveries[1:] {
		if item.AttemptCount > attempt {
			attempt = item.AttemptCount
		}
		if item.ExpiresAt.Before(expiresAt) {
			expiresAt = item.ExpiresAt
		}
	}
	delay := welcomeDeliveryRetryDelay(attempt)
	next := now.Add(delay)
	if next.After(expiresAt) {
		next = expiresAt
	}
	ids := welcomeDeliveryIDs(deliveries)
	updated, err := d.store.RetryWelcomeMessageDeliveries(ctx, d.owner, ids, next, reason)
	if err != nil || updated != len(ids) {
		d.log.Warn("retry welcome delivery",
			zap.Int64("join_event_id", delivery.JoinEventID), zap.Int64("target_user_id", delivery.TargetUserID),
			zap.Int("templates", len(ids)), zap.Int("updated", updated), zap.Error(err))
	}
}

func (r *Router) welcomeDeliveryUpdates(ctx context.Context, deliveries []domain.WelcomeMessageDelivery) (*tg.Updates, error) {
	if r == nil || r.deps.Channels == nil {
		return nil, errors.New("channel projection is unavailable")
	}
	if err := validateWelcomeDeliveryGroup(deliveries); err != nil {
		return nil, err
	}
	delivery := deliveries[0]
	view, err := r.deps.Channels.ResolveChannel(ctx, delivery.TargetUserID, delivery.ChannelID)
	if err != nil {
		return nil, err
	}
	if view.Self.Status != domain.ChannelMemberActive || view.Self.JoinedAt != delivery.JoinedAt {
		return nil, errWelcomeDeliveryMembershipSuperseded
	}
	updates := make([]tg.UpdateClass, 0, len(deliveries))
	for _, item := range deliveries {
		wire := tg.EphemeralMessage{
			Out: false, WelcomeTemplate: false,
			InvertMedia: item.Content.InvertMedia, Noforwards: item.Content.NoForwards,
			ID:         item.EphemeralID,
			FromID:     tgPeer(domain.Peer{Type: domain.PeerTypeChannel, ID: item.ChannelID}),
			PeerID:     tgPeer(domain.Peer{Type: domain.PeerTypeChannel, ID: item.ChannelID}),
			ReceiverID: 0,
			Date:       item.JoinedAt,
			Message:    item.Content.Message,
		}
		if len(item.Content.Entities) != 0 {
			wire.SetEntities(tgMessageEntities(item.Content.Entities))
		}
		if item.Content.Media != nil && !item.Content.Media.IsZero() {
			wire.SetMedia(tgMessageMedia(item.Content.Media))
		}
		if item.Content.ReplyMarkup != nil && !item.Content.ReplyMarkup.IsZero() {
			wire.SetReplyMarkup(tgReplyMarkup(item.Content.ReplyMarkup))
		}
		rich, err := tgRichMessage(item.Content.RichMessage)
		if err != nil {
			return nil, fmt.Errorf("project welcome rich message: %w", err)
		}
		if rich != nil {
			wire.SetRichMessage(*rich)
		}
		updates = append(updates, &tg.UpdateNewEphemeralMessage{Message: wire})
	}
	return &tg.Updates{
		Updates: updates,
		Chats:   []tg.ChatClass{tgChannelChatForView(delivery.TargetUserID, view)},
		Date:    delivery.JoinedAt,
		Seq:     0,
	}, nil
}

func groupWelcomeMessageDeliveries(deliveries []domain.WelcomeMessageDelivery) [][]domain.WelcomeMessageDelivery {
	groups := make([][]domain.WelcomeMessageDelivery, 0, len(deliveries))
	positions := make(map[int64]int, len(deliveries))
	for _, delivery := range deliveries {
		position, ok := positions[delivery.JoinEventID]
		if !ok {
			position = len(groups)
			positions[delivery.JoinEventID] = position
			groups = append(groups, nil)
		}
		groups[position] = append(groups[position], delivery)
	}
	return groups
}

func validateWelcomeDeliveryGroup(deliveries []domain.WelcomeMessageDelivery) error {
	if len(deliveries) == 0 || len(deliveries) > domain.MaxWelcomeMessagesPerPeer {
		return errors.New("invalid welcome delivery group size")
	}
	first := deliveries[0]
	seenTemplates := make(map[int]struct{}, len(deliveries))
	seenEphemeral := make(map[int]struct{}, len(deliveries))
	for _, delivery := range deliveries {
		if delivery.JoinEventID != first.JoinEventID || delivery.ChannelID != first.ChannelID ||
			delivery.TargetUserID != first.TargetUserID || delivery.JoinedAt != first.JoinedAt {
			return errors.New("inconsistent welcome delivery group")
		}
		if _, duplicate := seenTemplates[delivery.TemplateID]; duplicate {
			return errors.New("duplicate welcome delivery template")
		}
		if _, duplicate := seenEphemeral[delivery.EphemeralID]; duplicate {
			return errors.New("duplicate welcome delivery ephemeral id")
		}
		seenTemplates[delivery.TemplateID] = struct{}{}
		seenEphemeral[delivery.EphemeralID] = struct{}{}
	}
	return nil
}

func welcomeDeliveryIDs(deliveries []domain.WelcomeMessageDelivery) []int64 {
	ids := make([]int64, len(deliveries))
	for i, delivery := range deliveries {
		ids[i] = delivery.ID
	}
	return ids
}

func welcomeDeliveryRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	shift := attempt - 1
	if shift > 6 {
		shift = 6
	}
	delay := time.Second * time.Duration(1<<shift)
	if delay > defaultWelcomeDeliveryMaxRetry {
		return defaultWelcomeDeliveryMaxRetry
	}
	return delay
}

func welcomeDeliveryOwner() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err == nil {
		return "welcome-" + hex.EncodeToString(value[:])
	}
	return fmt.Sprintf("welcome-%d", time.Now().UnixNano())
}
