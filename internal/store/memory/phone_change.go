package memory

import (
	"context"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

// PhoneChangeStore 是测试用内存实现。用户唯一性在 UserStore 锁内维护；
// updateUserPhone 无 PTS，所以不向 UpdateEventStore 写 durable event。
type PhoneChangeStore struct {
	users  *UserStore
	events store.UpdateEventStore
}

func NewPhoneChangeStore(users *UserStore, events store.UpdateEventStore) *PhoneChangeStore {
	return &PhoneChangeStore{users: users, events: events}
}

func (s *PhoneChangeStore) ChangePhone(_ context.Context, req domain.PhoneChangeRequest) (domain.PhoneChangeResult, error) {
	if s == nil || s.users == nil || req.UserID == 0 || !domain.ValidPhone(req.Phone) {
		return domain.PhoneChangeResult{}, domain.ErrPhoneNumberInvalid
	}
	s.users.mu.Lock()
	u, ok := s.users.byID[req.UserID]
	if !ok {
		s.users.mu.Unlock()
		return domain.PhoneChangeResult{}, domain.ErrUserNotFound
	}
	if u.Phone == req.Phone {
		s.users.mu.Unlock()
		return domain.PhoneChangeResult{User: u}, nil
	}
	for id, existing := range s.users.byID {
		if id != req.UserID && existing.Phone == req.Phone {
			s.users.mu.Unlock()
			return domain.PhoneChangeResult{}, domain.ErrPhoneNumberOccupied
		}
	}
	u.Phone = req.Phone
	s.users.byID[req.UserID] = u
	s.users.mu.Unlock()
	return domain.PhoneChangeResult{User: u, Changed: true}, nil
}
