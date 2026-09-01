package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

// BroadcastStore is the in-memory implementation of store.BroadcastStore,
// used by admin/app unit tests. It has no concept of a "users table" to
// snapshot against for "all" mode, so callers seed eligible user ids via
// SeedEligibleUsers; MaterializeBroadcastRecipients walks that fixed set the
// same way the postgres backend walks a keyset range.
type BroadcastStore struct {
	mu            sync.Mutex
	broadcasts    map[int64]domain.Broadcast
	recipients    map[int64]*domain.BroadcastRecipient
	eligibleUsers []int64 // sorted ascending, mirrors "all non-bot, non-system users"
	nextBID       int64
	nextRID       int64
}

func NewBroadcastStore() *BroadcastStore {
	return &BroadcastStore{
		broadcasts: make(map[int64]domain.Broadcast),
		recipients: make(map[int64]*domain.BroadcastRecipient),
	}
}

var _ store.BroadcastStore = (*BroadcastStore)(nil)

// SeedEligibleUsers sets the fixed set of user ids "all"-mode targets and
// PreviewBroadcastRecipients/CreateBroadcast/MaterializeBroadcastRecipients
// enumerate over, mirroring the postgres store's live users-table query.
func (s *BroadcastStore) SeedEligibleUsers(userIDs []int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.eligibleUsers = append([]int64(nil), userIDs...)
	sort.Slice(s.eligibleUsers, func(i, j int) bool { return s.eligibleUsers[i] < s.eligibleUsers[j] })
}

func isEligibleSelected(userID int64) bool {
	return userID > 0 && !domain.IsSystemUserID(userID)
}

func (s *BroadcastStore) PreviewBroadcastRecipients(_ context.Context, mode domain.BroadcastTargetMode, selectedUserIDs []int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch mode {
	case domain.BroadcastTargetAll:
		if len(s.eligibleUsers) == 0 {
			return 0, domain.ErrBroadcastNoRecipients
		}
		return int64(len(s.eligibleUsers)), nil
	case domain.BroadcastTargetSelected:
		if len(selectedUserIDs) == 0 {
			return 0, domain.ErrBroadcastNoRecipients
		}
		for _, id := range selectedUserIDs {
			if !isEligibleSelected(id) {
				return 0, domain.ErrBroadcastRecipientInvalid
			}
		}
		return int64(len(selectedUserIDs)), nil
	default:
		return 0, domain.ErrBroadcastInvalid
	}
}

func (s *BroadcastStore) CreateBroadcast(_ context.Context, message string, entities []domain.MessageEntity, mode domain.BroadcastTargetMode, selectedUserIDs []int64, createdBy string) (domain.Broadcast, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch mode {
	case domain.BroadcastTargetAll:
		if len(s.eligibleUsers) == 0 {
			return domain.Broadcast{}, domain.ErrBroadcastNoRecipients
		}
		s.nextBID++
		b := domain.Broadcast{
			ID: s.nextBID, Message: message, Entities: entities, TargetMode: mode,
			TargetCount: int64(len(s.eligibleUsers)), CreatedBy: createdBy, CreatedAt: time.Now().UTC(),
		}
		s.broadcasts[b.ID] = b
		return b, nil
	case domain.BroadcastTargetSelected:
		if len(selectedUserIDs) == 0 {
			return domain.Broadcast{}, domain.ErrBroadcastNoRecipients
		}
		for _, id := range selectedUserIDs {
			if !isEligibleSelected(id) {
				return domain.Broadcast{}, domain.ErrBroadcastRecipientInvalid
			}
		}
		s.nextBID++
		b := domain.Broadcast{
			ID: s.nextBID, Message: message, Entities: entities, TargetMode: mode,
			EnumerationDone: true, CreatedBy: createdBy, CreatedAt: time.Now().UTC(),
		}
		seen := make(map[int64]bool, len(selectedUserIDs))
		for _, userID := range selectedUserIDs {
			if seen[userID] {
				continue
			}
			seen[userID] = true
			s.nextRID++
			s.recipients[s.nextRID] = &domain.BroadcastRecipient{
				ID: s.nextRID, BroadcastID: b.ID, UserID: userID,
				Status: domain.BroadcastRecipientPending, NextAttemptAt: time.Now().UTC(),
			}
			b.TargetCount++
			b.MaterializedCount++
		}
		s.broadcasts[b.ID] = b
		return b, nil
	default:
		return domain.Broadcast{}, domain.ErrBroadcastInvalid
	}
}

func (s *BroadcastStore) MaterializeBroadcastRecipients(_ context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []int64
	for id, b := range s.broadcasts {
		if b.TargetMode == domain.BroadcastTargetAll && !b.EnumerationDone {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	sortInt64s(ids)
	bid := ids[0]
	b := s.broadcasts[bid]
	inserted := 0
	for _, userID := range s.eligibleUsers {
		if int64(inserted) >= int64(limit) {
			break
		}
		if s.hasRecipient(bid, userID) {
			continue
		}
		s.nextRID++
		s.recipients[s.nextRID] = &domain.BroadcastRecipient{
			ID: s.nextRID, BroadcastID: bid, UserID: userID,
			Status: domain.BroadcastRecipientPending, NextAttemptAt: time.Now().UTC(),
		}
		b.MaterializedCount++
		inserted++
	}
	if inserted < limit {
		b.EnumerationDone = true
		b.TargetCount = b.MaterializedCount
	}
	s.broadcasts[bid] = b
	return inserted, nil
}

func (s *BroadcastStore) hasRecipient(broadcastID, userID int64) bool {
	for _, r := range s.recipients {
		if r.BroadcastID == broadcastID && r.UserID == userID {
			return true
		}
	}
	return false
}

func (s *BroadcastStore) ClaimBroadcastRecipients(_ context.Context, leaseToken string, limit int, lease time.Duration) ([]store.BroadcastRecipientClaim, error) {
	if leaseToken == "" {
		return nil, domain.ErrBroadcastInvalid
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []int64
	now := time.Now().UTC()
	for id, r := range s.recipients {
		eligible := (r.Status == domain.BroadcastRecipientPending && !r.NextAttemptAt.After(now)) ||
			(r.Status == domain.BroadcastRecipientProcessing && r.LeaseUntil != nil && !r.LeaseUntil.After(now))
		if eligible {
			ids = append(ids, id)
		}
	}
	sortInt64s(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	out := make([]store.BroadcastRecipientClaim, 0, len(ids))
	until := now.Add(lease)
	for _, id := range ids {
		r := s.recipients[id]
		r.Status = domain.BroadcastRecipientProcessing
		r.Attempts++
		r.LeaseToken = leaseToken
		r.LeaseUntil = &until
		r.UpdatedAt = now
		b := s.broadcasts[r.BroadcastID]
		out = append(out, store.BroadcastRecipientClaim{
			RecipientID: r.ID, BroadcastID: r.BroadcastID, UserID: r.UserID,
			Attempts: r.Attempts, LeaseToken: leaseToken, Message: b.Message, Entities: b.Entities,
		})
	}
	return out, nil
}

func (s *BroadcastStore) CompleteBroadcastRecipient(_ context.Context, claim store.BroadcastRecipientClaim, privateMessageID int64, messageBoxID int, pts int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.recipients[claim.RecipientID]
	if !ok || r.Status != domain.BroadcastRecipientProcessing || r.LeaseToken != claim.LeaseToken {
		return domain.ErrBroadcastLeaseLost
	}
	now := time.Now().UTC()
	r.Status = domain.BroadcastRecipientSent
	r.LeaseToken = ""
	r.LeaseUntil = nil
	r.LastError = ""
	r.PrivateMessageID = privateMessageID
	r.MessageBoxID = messageBoxID
	r.Pts = pts
	r.SentAt = &now
	r.UpdatedAt = now
	b := s.broadcasts[claim.BroadcastID]
	b.SentCount++
	s.broadcasts[claim.BroadcastID] = b
	return nil
}

func (s *BroadcastStore) ReleaseBroadcastRecipient(_ context.Context, claim store.BroadcastRecipientClaim, cause string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.recipients[claim.RecipientID]
	if !ok || r.Status != domain.BroadcastRecipientProcessing || r.LeaseToken != claim.LeaseToken {
		return nil
	}
	now := time.Now().UTC()
	r.LeaseToken = ""
	r.LeaseUntil = nil
	r.LastError = cause
	r.UpdatedAt = now
	if r.Attempts >= domain.MaxBroadcastRecipientAttempts {
		r.Status = domain.BroadcastRecipientFailed
		b := s.broadcasts[claim.BroadcastID]
		b.FailedCount++
		s.broadcasts[claim.BroadcastID] = b
	} else {
		r.Status = domain.BroadcastRecipientPending
		r.NextAttemptAt = now
	}
	return nil
}

func (s *BroadcastStore) ListBroadcasts(_ context.Context, beforeID int64, limit int) ([]domain.Broadcast, bool, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]int64, 0, len(s.broadcasts))
	for id := range s.broadcasts {
		if beforeID == 0 || id < beforeID {
			ids = append(ids, id)
		}
	}
	sortInt64sDesc(ids)
	hasMore := len(ids) > limit
	if hasMore {
		ids = ids[:limit]
	}
	out := make([]domain.Broadcast, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.broadcasts[id])
	}
	return out, hasMore, nil
}

func (s *BroadcastStore) BroadcastByID(_ context.Context, id int64) (domain.Broadcast, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.broadcasts[id]
	if !ok {
		return domain.Broadcast{}, false, nil
	}
	return b, true, nil
}

func sortInt64s(v []int64) {
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
}

func sortInt64sDesc(v []int64) {
	sort.Slice(v, func(i, j int) bool { return v[i] > v[j] })
}
