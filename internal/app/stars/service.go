// Package stars 实现 Stars 本地账本应用服务：余额查询、贷记/借记、流水分页，
// 以及「惰性首读授予」起始余额（靠 stars_balances.granted 布尔幂等，新老账号都覆盖、
// 无需回填迁移）。原子性由 store 事务保证；本层只做校验 + 授予策略。
package stars

import (
	"context"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

// Service 是 Stars 账本应用服务。
type Service struct {
	store         store.StarsStore
	purchaseStore store.StarsPurchaseStore
	grantAmount   int64
	now           func() time.Time
}

// Option 配置 Service。
type Option func(*Service)

// WithStartingGrant 设置惰性首读授予的起始余额；amount<=0 关闭自动授予。
func WithStartingGrant(amount int64) Option {
	return func(s *Service) { s.grantAmount = amount }
}

// WithPurchaseStore enables the atomic fiat Stars checkout aggregate.
func WithPurchaseStore(st store.StarsPurchaseStore) Option {
	return func(s *Service) { s.purchaseStore = st }
}

// WithClock 注入时钟（测试用）。
func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

// NewService 创建 Stars 账本服务，默认起始授予 domain.DefaultStarsStartingGrant。
func NewService(st store.StarsStore, opts ...Option) *Service {
	s := &Service{store: st, grantAmount: domain.DefaultStarsStartingGrant, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ensureGranted 惰性应用一次起始授予（幂等），返回最新余额。
func (s *Service) ensureGranted(ctx context.Context, userID int64) (domain.StarsBalance, error) {
	if s.grantAmount > 0 {
		bal, _, err := s.store.EnsureGrant(ctx, userID, s.grantAmount, int(s.now().Unix()))
		return bal, err
	}
	return s.store.GetBalance(ctx, userID)
}

// GetBalance 返回账号余额，首读时惰性授予起始余额。
func (s *Service) GetBalance(ctx context.Context, userID int64) (domain.StarsBalance, error) {
	return s.ensureGranted(ctx, userID)
}

// Credit 为账号入账（amount>0）。
func (s *Service) Credit(ctx context.Context, userID, amount int64, reason domain.StarsTransactionReason, peer domain.Peer, title, desc string) (domain.StarsBalance, error) {
	if amount <= 0 {
		return domain.StarsBalance{}, domain.ErrStarsInvalidAmount
	}
	return s.store.Credit(ctx, userID, amount, reason, peer, int(s.now().Unix()), title, desc)
}

// Debit 从账号扣款（amount>0），余额不足返回 domain.ErrStarsInsufficient。
// 先确保起始授予已应用，避免新账号在尚未首读余额前借记被误判余额不足。
func (s *Service) Debit(ctx context.Context, userID, amount int64, reason domain.StarsTransactionReason, peer domain.Peer, title, desc string) (domain.StarsBalance, error) {
	if amount <= 0 {
		return domain.StarsBalance{}, domain.ErrStarsInvalidAmount
	}
	if _, err := s.ensureGranted(ctx, userID); err != nil {
		return domain.StarsBalance{}, err
	}
	return s.store.Debit(ctx, userID, amount, reason, peer, int(s.now().Unix()), title, desc)
}

// ListTransactions 按方向与顺序做 keyset 分页，首读时惰性授予。
func (s *Service) ListTransactions(ctx context.Context, userID int64, query domain.StarsTransactionQuery) (domain.StarsTransactionPage, error) {
	query, err := domain.NormalizeStarsTransactionQuery(query)
	if err != nil {
		return domain.StarsTransactionPage{}, err
	}
	if _, err := s.ensureGranted(ctx, userID); err != nil {
		return domain.StarsTransactionPage{}, err
	}
	return s.store.ListTransactions(ctx, userID, query)
}

// IssuePurchaseForm persists a short-lived, exact checkout intent.
func (s *Service) IssuePurchaseForm(ctx context.Context, form domain.StarsPurchaseForm) (domain.StarsPurchaseForm, error) {
	if s.purchaseStore == nil || !validPurchaseForm(form) {
		return domain.StarsPurchaseForm{}, domain.ErrStarsPurchaseFormInvalid
	}
	return s.purchaseStore.IssueStarsPurchaseForm(ctx, form)
}

// Purchase settles one exact persisted form. Package validation remains at
// the RPC boundary as well, while the store revalidates the persisted tuple
// under lock before performing any write.
func (s *Service) Purchase(ctx context.Context, req domain.StarsPurchaseRequest) (domain.StarsPurchaseResult, error) {
	if s.purchaseStore == nil || req.FormID == 0 || req.Date <= 0 || !validPurchaseCommand(req.StarsPurchaseForm) {
		return domain.StarsPurchaseResult{}, domain.ErrStarsPurchaseFormInvalid
	}
	return s.purchaseStore.PurchaseStars(ctx, req)
}

// GetGiveawayInfo resolves one launch card from the same aggregate that
// persisted it. date is supplied by the RPC clock for deterministic tests.
func (s *Service) GetGiveawayInfo(ctx context.Context, viewerUserID, channelID int64, messageID, date int) (domain.StarsGiveawayInfo, error) {
	reader, ok := s.purchaseStore.(store.StarsGiveawayStore)
	if !ok || viewerUserID <= 0 || channelID <= 0 || messageID <= 0 || date <= 0 {
		return domain.StarsGiveawayInfo{}, domain.ErrStarsPurchaseFormInvalid
	}
	return reader.GetStarsGiveawayInfo(ctx, viewerUserID, channelID, messageID, date)
}

func validPurchaseForm(form domain.StarsPurchaseForm) bool {
	return validPurchaseCommand(form) && form.IssuedAt > 0 && form.ExpiresAt == form.IssuedAt+600
}

func validPurchaseCommand(form domain.StarsPurchaseForm) bool {
	if !form.Kind.Valid() || form.BuyerUserID <= 0 || form.Stars <= 0 || form.Amount <= 0 || form.Currency == "" {
		return false
	}
	switch form.Kind {
	case domain.StarsPurchaseTopup:
		return form.Giveaway == nil && form.RecipientUserID == 0 && ((form.SpendPurposePeer == domain.Peer{}) ||
			((form.SpendPurposePeer.Type == domain.PeerTypeUser || form.SpendPurposePeer.Type == domain.PeerTypeChannel) && form.SpendPurposePeer.ID > 0))
	case domain.StarsPurchaseGift:
		return form.Giveaway == nil && form.RecipientUserID > 0 && form.BuyerUserID != form.RecipientUserID && form.SpendPurposePeer == (domain.Peer{})
	case domain.StarsPurchaseGiveaway:
		g := form.Giveaway
		return form.RecipientUserID == 0 && form.SpendPurposePeer == (domain.Peer{}) && g != nil &&
			g.BoostPeer.Type == domain.PeerTypeChannel && g.BoostPeer.ID > 0 && g.RandomID != 0 &&
			g.UntilDate > 0 && g.Users > 0 && g.PerUserStars > 0 &&
			int64(g.Users) <= form.Stars/g.PerUserStars && int64(g.Users)*g.PerUserStars == form.Stars
	default:
		return false
	}
}
