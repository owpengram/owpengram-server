package memory

import (
	"context"
	"sync"

	"telesrv/internal/domain"
)

// StarsStore 是 store.StarsStore 的内存实现，复刻 postgres 版的原子语义
// （在单个互斥锁下完成读-检查-写，等价于 SELECT ... FOR UPDATE）。
type StarsStore struct {
	mu     sync.Mutex
	states map[int64]*starsState
	nextID int64
}

type starsState struct {
	balance int64
	granted bool
	txns    []domain.StarsTransaction // 追加序，读时倒序
}

// NewStarsStore 创建内存 StarsStore。
func NewStarsStore() *StarsStore {
	return &StarsStore{states: make(map[int64]*starsState)}
}

func (s *StarsStore) GetBalance(_ context.Context, userID int64) (domain.StarsBalance, error) {
	if userID == 0 {
		return domain.StarsBalance{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.states[userID]
	if st == nil {
		return domain.StarsBalance{UserID: userID}, nil
	}
	return domain.StarsBalance{UserID: userID, Balance: st.balance, Granted: st.granted}, nil
}

func (s *StarsStore) EnsureGrant(_ context.Context, userID, amount int64, date int) (domain.StarsBalance, bool, error) {
	if userID == 0 {
		return domain.StarsBalance{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.states[userID]
	if st == nil {
		st = &starsState{}
		s.states[userID] = st
	}
	if amount <= 0 {
		return domain.StarsBalance{UserID: userID, Balance: st.balance, Granted: st.granted}, false, nil
	}
	if st.granted {
		return domain.StarsBalance{UserID: userID, Balance: st.balance, Granted: true}, false, nil
	}
	st.balance += amount
	st.granted = true
	s.appendTxn(st, userID, amount, domain.StarsReasonGrant, domain.Peer{}, date, "", "")
	return domain.StarsBalance{UserID: userID, Balance: st.balance, Granted: true}, true, nil
}

func (s *StarsStore) Credit(_ context.Context, userID, amount int64, reason domain.StarsTransactionReason, peer domain.Peer, date int, title, desc string) (domain.StarsBalance, error) {
	if userID == 0 || amount <= 0 {
		return domain.StarsBalance{}, domain.ErrStarsInvalidAmount
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.states[userID]
	if st == nil {
		st = &starsState{}
		s.states[userID] = st
	}
	st.balance += amount
	s.appendTxn(st, userID, amount, reason, peer, date, title, desc)
	return domain.StarsBalance{UserID: userID, Balance: st.balance, Granted: st.granted}, nil
}

func (s *StarsStore) Debit(_ context.Context, userID, amount int64, reason domain.StarsTransactionReason, peer domain.Peer, date int, title, desc string) (domain.StarsBalance, error) {
	if userID == 0 || amount <= 0 {
		return domain.StarsBalance{}, domain.ErrStarsInvalidAmount
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.states[userID]
	if st == nil || st.balance < amount {
		return domain.StarsBalance{}, domain.ErrStarsInsufficient
	}
	st.balance -= amount
	s.appendTxn(st, userID, -amount, reason, peer, date, title, desc)
	return domain.StarsBalance{UserID: userID, Balance: st.balance, Granted: st.granted}, nil
}

func (s *StarsStore) ListTransactions(_ context.Context, userID int64, query domain.StarsTransactionQuery) (domain.StarsTransactionPage, error) {
	if userID == 0 {
		return domain.StarsTransactionPage{}, nil
	}
	query, err := domain.NormalizeStarsTransactionQuery(query)
	if err != nil {
		return domain.StarsTransactionPage{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.states[userID]
	if st == nil {
		return domain.StarsTransactionPage{}, nil
	}
	page := domain.StarsTransactionPage{Balance: st.balance}
	cursor, hasCursor := domain.DecodeStarsCursor(query.Offset)
	out := make([]domain.StarsTransaction, 0, query.Limit+1)
	appendMatch := func(t domain.StarsTransaction) bool {
		if hasCursor {
			if query.Ascending && t.ID <= cursor {
				return false
			}
			if !query.Ascending && t.ID >= cursor {
				return false
			}
		}
		if !query.Direction.IncludesAmount(t.Amount) {
			return false
		}
		out = append(out, t)
		return len(out) > query.Limit
	}
	if query.Ascending {
		for i := 0; i < len(st.txns) && len(out) <= query.Limit; i++ {
			appendMatch(st.txns[i])
		}
	} else {
		for i := len(st.txns) - 1; i >= 0 && len(out) <= query.Limit; i-- {
			appendMatch(st.txns[i])
		}
	}
	if len(out) > query.Limit {
		out = out[:query.Limit]
		page.NextOffset = domain.EncodeStarsCursor(out[len(out)-1].ID)
	}
	page.Transactions = out
	return page, nil
}

func (s *StarsStore) appendTxn(st *starsState, userID, amount int64, reason domain.StarsTransactionReason, peer domain.Peer, date int, title, desc string) {
	s.nextID++
	st.txns = append(st.txns, domain.StarsTransaction{
		ID:          s.nextID,
		UserID:      userID,
		Peer:        peer,
		Amount:      amount,
		Date:        date,
		Reason:      reason,
		Title:       title,
		Description: desc,
	})
}
