// Package broadcast implements admin-triggered system message campaigns:
// sending a message from the official system account (domain.OfficialSystemUserID,
// 777000) to every user or a hand-picked list.
//
// A "selected" campaign's recipient rows are all inserted at creation, since
// that list is bounded by domain.MaxBroadcastSelectedRecipients. An "all"
// campaign instead only snapshots the current max eligible user id at
// creation, and store.BroadcastStore.MaterializeBroadcastRecipients walks
// that range incrementally, a bounded batch per worker cycle -- so creating
// a campaign for a large user base is a single cheap insert, not one giant
// blocking transaction.
//
// Delivery is a lease-based claim cycle (store.BroadcastStore.
// ClaimBroadcastRecipients/CompleteBroadcastRecipient/ReleaseBroadcastRecipient):
// a worker leases a bounded batch of eligible rows for a fixed duration,
// delivers each one, and closes it out. A lease that is never renewed simply
// expires, so a worker crash mid-cycle cannot strand rows in 'processing'
// forever, and a future multi-instance worker can run the same cycle
// concurrently without two instances ever believing they hold the same
// row's lease at once.
//
// Delivery itself goes through messageSender.SendPrivateText -- the same
// store-layer send path internal/app/bots's sendServiceBotReplyResult calls
// directly (bypassing the auth-checked app.messages.Service wrapper, which
// requires SenderUserID == the authenticated caller) -- rather than
// duplicating message/pts/dispatch-outbox creation here. SendPrivateText's
// random_id dedup is what actually closes the small race a lease alone
// leaves open: if a lease expires and gets reclaimed while the original
// holder's send is still in flight, both attempts use the same
// (broadcastID, userID)-derived random id (see stableBroadcastRandomID), so
// the store resolves them to the very same message instead of sending
// twice, no matter which claim ends up recording it.
package broadcast

import (
	"context"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

// messageSender is the narrow port this package needs from
// store.MessageStore: sending a message with an arbitrary SenderUserID.
type messageSender interface {
	SendPrivateText(ctx context.Context, req domain.SendPrivateTextRequest) (domain.SendPrivateTextResult, error)
}

// Service creates broadcasts and drains their delivery outbox.
type Service struct {
	store    store.BroadcastStore
	messages messageSender
	log      *zap.Logger
}

// Option adjusts an optional Service dependency.
type Option func(*Service)

// NewService builds the broadcast service.
func NewService(st store.BroadcastStore, opts ...Option) *Service {
	s := &Service{store: st, log: zap.NewNop()}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// WithMessageSender injects the store used to actually deliver a message.
func WithMessageSender(m messageSender) Option {
	return func(s *Service) {
		if m != nil {
			s.messages = m
		}
	}
}

// WithLogger injects a logger (default zap.NewNop()).
func WithLogger(log *zap.Logger) Option {
	return func(s *Service) {
		if log != nil {
			s.log = log
		}
	}
}

// Ready reports whether both the store and the sender are wired.
func (s *Service) Ready() bool { return s != nil && s.store != nil && s.messages != nil }

func normalizeRequest(message string, mode domain.BroadcastTargetMode, selectedUserIDs []int64) (string, []int64, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return "", nil, domain.ErrBroadcastMessageEmpty
	}
	if !utf8.ValidString(message) || len(message) > domain.MaxBroadcastMessageBytes {
		return "", nil, domain.ErrBroadcastMessageTooLong
	}
	switch mode {
	case domain.BroadcastTargetAll:
		if len(selectedUserIDs) != 0 {
			return "", nil, domain.ErrBroadcastInvalid
		}
		return message, nil, nil
	case domain.BroadcastTargetSelected:
		if len(selectedUserIDs) == 0 {
			return "", nil, domain.ErrBroadcastNoRecipients
		}
		if len(selectedUserIDs) > domain.MaxBroadcastSelectedRecipients {
			return "", nil, domain.ErrBroadcastInvalid
		}
		seen := make(map[int64]struct{}, len(selectedUserIDs))
		ids := make([]int64, 0, len(selectedUserIDs))
		for _, userID := range selectedUserIDs {
			if userID <= 0 || domain.IsSystemUserID(userID) {
				return "", nil, domain.ErrBroadcastRecipientInvalid
			}
			if _, ok := seen[userID]; ok {
				return "", nil, domain.ErrBroadcastRecipientInvalid
			}
			seen[userID] = struct{}{}
			ids = append(ids, userID)
		}
		return message, ids, nil
	default:
		return "", nil, domain.ErrBroadcastInvalid
	}
}

// Preview validates and counts a campaign's intended recipient set without
// creating anything, so an admin UI can show "this will reach N users"
// before committing.
func (s *Service) Preview(ctx context.Context, message string, mode domain.BroadcastTargetMode, selectedUserIDs []int64) (int64, error) {
	if s == nil || s.store == nil {
		return 0, fmt.Errorf("broadcast store is not configured")
	}
	_, ids, err := normalizeRequest(message, mode, selectedUserIDs)
	if err != nil {
		return 0, err
	}
	return s.store.PreviewBroadcastRecipients(ctx, mode, ids)
}

// Create validates and snapshots a new broadcast, then returns immediately:
// delivery (and, for "all" mode, recipient enumeration itself) happens
// asynchronously via the Worker's RunCycle, so this never blocks an admin
// HTTP request on however many recipients there are.
//
// Entities are derived automatically from the plain-text message (mentions,
// hashtags, cashtags, bot commands -- see domain.DetectAutomaticMessageEntities),
// not operator-composed: there is currently no admin UI for hand-authoring
// bold/italic/link spans on a broadcast, so this only gets a broadcast the
// same clickable-entity rendering any other plain-text message with an
// @mention or #hashtag already gets.
func (s *Service) Create(ctx context.Context, message string, mode domain.BroadcastTargetMode, selectedUserIDs []int64, createdBy string) (domain.Broadcast, error) {
	if s == nil || s.store == nil {
		return domain.Broadcast{}, fmt.Errorf("broadcast store is not configured")
	}
	message, ids, err := normalizeRequest(message, mode, selectedUserIDs)
	if err != nil {
		return domain.Broadcast{}, err
	}
	entities := domain.DetectAutomaticMessageEntities(message, nil)
	return s.store.CreateBroadcast(ctx, message, entities, mode, ids, strings.TrimSpace(createdBy))
}

// List pages broadcasts newest-first.
func (s *Service) List(ctx context.Context, beforeID int64, limit int) ([]domain.Broadcast, bool, error) {
	if s == nil || s.store == nil {
		return nil, false, nil
	}
	return s.store.ListBroadcasts(ctx, beforeID, limit)
}

// Get returns one broadcast.
func (s *Service) Get(ctx context.Context, id int64) (domain.Broadcast, bool, error) {
	if s == nil || s.store == nil {
		return domain.Broadcast{}, false, nil
	}
	return s.store.BroadcastByID(ctx, id)
}

// CycleResult reports one worker cycle's outcome.
type CycleResult struct {
	Materialized int
	Claimed      int
	Sent         int
	Failed       int
}

// RunCycle advances "all"-mode enumeration by up to materializeBatch rows,
// then claims up to deliveryBatch eligible recipient rows under leaseToken
// for lease, delivering each one via SendPrivateText. One recipient's
// failure (blocked account, deleted account, transient error) never blocks
// the rest of the batch.
func (s *Service) RunCycle(ctx context.Context, leaseToken string, materializeBatch, deliveryBatch int, lease time.Duration) (CycleResult, error) {
	var result CycleResult
	if !s.Ready() {
		return result, nil
	}
	materialized, err := s.store.MaterializeBroadcastRecipients(ctx, materializeBatch)
	if err != nil {
		return result, err
	}
	result.Materialized = materialized
	claims, err := s.store.ClaimBroadcastRecipients(ctx, leaseToken, deliveryBatch, lease)
	if err != nil {
		return result, err
	}
	result.Claimed = len(claims)
	for _, claim := range claims {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := s.deliverClaim(ctx, claim); err != nil {
			result.Failed++
			if releaseErr := s.store.ReleaseBroadcastRecipient(ctx, claim, err.Error()); releaseErr != nil {
				s.log.Warn("release broadcast recipient failed",
					zap.Int64("recipient_id", claim.RecipientID),
					zap.Int64("broadcast_id", claim.BroadcastID),
					zap.Error(releaseErr))
			}
			continue
		}
		result.Sent++
	}
	return result, nil
}

func (s *Service) deliverClaim(ctx context.Context, claim store.BroadcastRecipientClaim) error {
	send, err := s.messages.SendPrivateText(ctx, domain.SendPrivateTextRequest{
		SenderUserID:    domain.OfficialSystemUserID,
		RecipientUserID: claim.UserID,
		// A stable id derived from (broadcast, recipient) makes redelivering
		// this exact row idempotent at the store layer's random_id dedup: if
		// this claim's lease expires and gets reclaimed while a prior send is
		// still in flight, both resolve to the same message instead of
		// sending twice.
		RandomID: stableBroadcastRandomID(claim.BroadcastID, claim.UserID),
		Message:  claim.Message,
		Entities: claim.Entities,
	})
	if err != nil {
		return err
	}
	msg := send.RecipientMessage
	if err := s.store.CompleteBroadcastRecipient(ctx, claim, msg.UID, msg.ID, msg.Pts); err != nil {
		return err
	}
	return nil
}

// stableBroadcastRandomID derives a random_id from (broadcastID, userID) so
// re-processing the same recipient row (after a lease is reclaimed, before
// it was recorded delivered) resolves to the same send instead of a
// duplicate message.
func stableBroadcastRandomID(broadcastID, userID int64) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strconv.FormatInt(broadcastID, 10) + ":" + strconv.FormatInt(userID, 10)))
	v := int64(h.Sum64())
	if v == 0 {
		v = 1
	}
	return v
}
