package donations

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

// chainKeyRe matches the same slug shape every seeded chain_key already
// uses (ethereum, bsc, sepolia, ...): lowercase ASCII letters, digits and
// underscores, so it's safe as both a DB primary key and a URL-free token
// in admin API paths.
var chainKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)

// CreditNotifier receives one notice per deposit the watcher just credited,
// so whatever owns the actual chat channel (app/bots.Service and its
// built-in @premiumbot, which satisfies this as-is via
// NotifyDonationCredited) can tell the user their Stars landed. A narrow
// port rather than a concrete dependency for the usual reason: donations
// must not need to import bots to send a chat message, and bots already
// depends on donations (donationsSource) the other way for /deposit, so a
// direct import back would cycle.
type CreditNotifier interface {
	NotifyDonationCredited(ctx context.Context, notice domain.DonationCreditNotice)
}

// Service is the application boundary for crypto donations: wallet
// lifecycle, per-user address assignment, and (in watcher.go) the
// on-chain watcher that detects and credits deposits. It holds the
// decrypted wallet in memory only -- never persists it anywhere itself.
type Service struct {
	store    store.DonationStore
	key      EncryptionKey
	wallet   *Wallet
	notifier CreditNotifier

	watcherMu           sync.Mutex
	watcherCtx          context.Context
	watcherPollInterval time.Duration
	watcherLog          *zap.Logger
	watcherCancel       map[string]context.CancelFunc
}

// SetNotifier wires the chat notification sent after each deposit is
// credited. Without a call to this (a nil notifier, the zero value),
// crediting still happens exactly the same -- the user just isn't told
// about it until they check /deposit or their Stars balance themselves.
func (s *Service) SetNotifier(n CreditNotifier) {
	if s == nil {
		return
	}
	s.notifier = n
}

// NewService constructs the service with an already-resolved encryption
// key (see LoadOrGenerateEncryptionKey -- the normal path -- or
// ParseEncryptionKey for an operator-supplied override) and, if a wallet
// already exists in the store, loads and decrypts it immediately. If none
// exists yet, the service comes back not Ready(); call EnsureWallet to
// provision one -- cmd/telesrv does this automatically at startup so
// donations work with zero manual setup, see docs/donations.md.
func NewService(ctx context.Context, st store.DonationStore, key EncryptionKey) (*Service, error) {
	s := &Service{store: st, key: key}
	seed, nonce, found, err := st.WalletSeed(ctx)
	if err != nil {
		return nil, err
	}
	if !found {
		return s, nil
	}
	mnemonic, err := Open(key, seed, nonce)
	if err != nil {
		return nil, err
	}
	wallet, err := NewWallet(mnemonic)
	if err != nil {
		return nil, err
	}
	s.wallet = wallet
	return s, nil
}

// EnsureWallet generates and persists a new encrypted wallet the first time
// donations run against a store that doesn't have one yet. cmd/telesrv
// calls this automatically at startup whenever !Ready() -- see
// LoadOrGenerateEncryptionKey's doc comment for the matching "no manual
// step" reasoning on the key file. It's still idempotent/safe to call by
// hand: calling it again once a wallet exists returns
// domain.ErrDonationWalletAlreadyExists rather than silently doing nothing,
// so nothing mistakes "already set up" for "just set up, please save this
// phrase".
func (s *Service) EnsureWallet(ctx context.Context) (mnemonic string, err error) {
	if s == nil || s.store == nil {
		return "", domain.ErrDonationWalletNotConfigured
	}
	if s.wallet != nil {
		return "", domain.ErrDonationWalletAlreadyExists
	}
	mnemonic, err = GenerateMnemonic()
	if err != nil {
		return "", err
	}
	ciphertext, nonce, err := Seal(s.key, mnemonic)
	if err != nil {
		return "", err
	}
	if err := s.store.CreateWalletSeed(ctx, ciphertext, nonce); err != nil {
		return "", err
	}
	wallet, err := NewWallet(mnemonic)
	if err != nil {
		return "", err
	}
	s.wallet = wallet
	return mnemonic, nil
}

// Ready reports whether the service has a usable, decrypted wallet.
func (s *Service) Ready() bool {
	return s != nil && s.wallet != nil
}

// AddressForUser returns the user's permanent donation deposit address,
// assigning one on first call. The address is identical across every
// enabled EVM chain -- there is exactly one per user, full stop.
func (s *Service) AddressForUser(ctx context.Context, userID int64) (string, error) {
	if s == nil || s.store == nil {
		return "", domain.ErrDonationWalletNotConfigured
	}
	if userID <= 0 {
		return "", fmt.Errorf("donations: invalid user id %d", userID)
	}
	if existing, found, err := s.store.DonationAddressForUser(ctx, userID); err != nil {
		return "", err
	} else if found {
		return existing.Address, nil
	}
	if !s.Ready() {
		return "", domain.ErrDonationWalletNotConfigured
	}
	index, err := s.store.NextDonationAddressIndex(ctx)
	if err != nil {
		return "", err
	}
	address, err := s.wallet.DeriveAddress(index)
	if err != nil {
		return "", err
	}
	row, err := s.store.InsertDonationAddress(ctx, userID, index, address)
	if err != nil {
		return "", err
	}
	return row.Address, nil
}

// UpdateChainConfig is the admin panel's write path for a chain's
// operator-editable settings (RPC/WS endpoint, enabled flag, confirmation
// depth, price feed/manual rate) -- see internal/admin.Service.UpdateDonationChain,
// the only caller. It never touches the chain's identity or native currency.
func (s *Service) UpdateChainConfig(ctx context.Context, upd domain.DonationChainConfigUpdate) (domain.DonationChain, error) {
	if s == nil || s.store == nil {
		return domain.DonationChain{}, domain.ErrDonationWalletNotConfigured
	}
	if strings.TrimSpace(upd.ChainKey) == "" {
		return domain.DonationChain{}, domain.ErrDonationChainNotFound
	}
	if upd.ConfirmationsRequired <= 0 {
		return domain.DonationChain{}, fmt.Errorf("donations: confirmations_required must be positive")
	}
	// A chain enabled with no USD rate prices every deposit to zero Stars,
	// which the watcher silently refuses to credit (see
	// refreshConfirmationsAndCredit) -- the deposit sits at "confirmed"
	// forever. Catch this before it ships instead of after a donor's
	// deposit goes uncredited.
	if upd.Enabled && upd.ManualUSDRateMicros <= 0 {
		return domain.DonationChain{}, fmt.Errorf("donations: manual_usd_rate_micros must be positive to enable a chain -- deposits would price to zero Stars and never get credited")
	}
	chain, err := s.store.UpdateDonationChainConfig(ctx, upd)
	if err != nil {
		return domain.DonationChain{}, err
	}
	if chain.Watchable() {
		s.ensureWatcher(chain.Key)
	} else {
		s.stopWatcher(chain.Key)
	}
	return chain, nil
}

// CreateChain adds a brand new chain (an operator picking a preset or
// filling in a custom form in the admin panel's "Add chain" menu). Unlike
// UpdateChainConfig this also sets the chain's identity/native-currency
// fields, since they don't exist yet for a chain the operator is creating.
func (s *Service) CreateChain(ctx context.Context, chain domain.DonationChain) (domain.DonationChain, error) {
	if s == nil || s.store == nil {
		return domain.DonationChain{}, domain.ErrDonationWalletNotConfigured
	}
	chain.Key = strings.ToLower(strings.TrimSpace(chain.Key))
	chain.Name = strings.TrimSpace(chain.Name)
	if !chainKeyRe.MatchString(chain.Key) {
		return domain.DonationChain{}, fmt.Errorf("donations: chain key must be 2-32 lowercase letters/digits/underscores, starting with a letter")
	}
	if chain.Name == "" {
		return domain.DonationChain{}, fmt.Errorf("donations: chain name is required")
	}
	if !chain.Valid() {
		return domain.DonationChain{}, fmt.Errorf("donations: chain_id, native_decimals and confirmations_required must all be positive")
	}
	// See UpdateChainConfig's identical check for why.
	if chain.Enabled && chain.ManualUSDRateMicros <= 0 {
		return domain.DonationChain{}, fmt.Errorf("donations: manual_usd_rate_micros must be positive to enable a chain -- deposits would price to zero Stars and never get credited")
	}
	created, err := s.store.CreateDonationChain(ctx, chain)
	if err != nil {
		return domain.DonationChain{}, err
	}
	if created.Watchable() {
		s.ensureWatcher(created.Key)
	}
	return created, nil
}

// DeleteChain removes a chain the operator added by mistake or no longer
// wants listed. See store.DonationStore.DeleteDonationChain: refuses with
// domain.ErrDonationChainHasDeposits once real donation history exists --
// disable it instead at that point.
func (s *Service) DeleteChain(ctx context.Context, chainKey string) error {
	if s == nil || s.store == nil {
		return domain.ErrDonationWalletNotConfigured
	}
	chainKey = strings.ToLower(strings.TrimSpace(chainKey))
	if chainKey == "" {
		return domain.ErrDonationChainNotFound
	}
	if err := s.store.DeleteDonationChain(ctx, chainKey); err != nil {
		return err
	}
	s.stopWatcher(chainKey)
	return nil
}

// StartWatchers starts the on-chain watcher for every currently enabled and
// watchable chain, and remembers ctx/pollInterval/log so CreateChain and
// UpdateChainConfig can start (or stop) an individual chain's watcher
// dynamically from then on -- without this, a chain added or re-enabled
// through the admin panel's "Add chain" menu or its Enabled toggle would
// silently never be watched until the next full server restart, since
// nothing else in this process ever re-scans the chain table. Call this
// once, at server startup (see cmd/telesrv/main.go); ctx's cancellation
// stops every watcher, current and future.
func (s *Service) StartWatchers(ctx context.Context, pollInterval time.Duration, log *zap.Logger) error {
	if s == nil || s.store == nil {
		return domain.ErrDonationWalletNotConfigured
	}
	s.watcherMu.Lock()
	s.watcherCtx = ctx
	s.watcherPollInterval = pollInterval
	s.watcherLog = log
	if s.watcherCancel == nil {
		s.watcherCancel = make(map[string]context.CancelFunc)
	}
	s.watcherMu.Unlock()
	chains, err := s.store.EnabledDonationChains(ctx)
	if err != nil {
		return err
	}
	for _, chain := range chains {
		if chain.Watchable() {
			s.ensureWatcher(chain.Key)
		}
	}
	return nil
}

// ensureWatcher starts chainKey's watcher goroutine if one isn't already
// running. A no-op before StartWatchers has ever been called (donations
// disabled entirely, or the process hasn't finished booting yet) -- the
// chain will simply be picked up by StartWatchers' own initial sweep once
// it does run.
func (s *Service) ensureWatcher(chainKey string) {
	s.watcherMu.Lock()
	defer s.watcherMu.Unlock()
	if s.watcherCtx == nil || s.watcherCancel == nil {
		return
	}
	if _, running := s.watcherCancel[chainKey]; running {
		return
	}
	childCtx, cancel := context.WithCancel(s.watcherCtx)
	s.watcherCancel[chainKey] = cancel
	log := s.watcherLog
	if log == nil {
		log = zap.NewNop()
	}
	chainLog := log.Named(chainKey)
	pollInterval := s.watcherPollInterval
	go func() {
		if err := s.WatchChain(childCtx, chainKey, pollInterval, chainLog); err != nil && !errors.Is(err, context.Canceled) {
			chainLog.Error("donation watcher stopped", zap.Error(err))
		}
		s.watcherMu.Lock()
		if s.watcherCancel[chainKey] != nil {
			delete(s.watcherCancel, chainKey)
		}
		s.watcherMu.Unlock()
	}()
}

// stopWatcher cancels chainKey's watcher goroutine, if one is running --
// called when a chain is disabled, deleted, or loses the RPC URL that made
// it watchable.
func (s *Service) stopWatcher(chainKey string) {
	s.watcherMu.Lock()
	cancel, running := s.watcherCancel[chainKey]
	if running {
		delete(s.watcherCancel, chainKey)
	}
	s.watcherMu.Unlock()
	if running {
		cancel()
	}
}

// EnabledChains lists every chain configured as enabled, whether or not
// it's actually watchable yet (see domain.DonationChain.Watchable).
func (s *Service) EnabledChains(ctx context.Context) ([]domain.DonationChain, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	return s.store.EnabledDonationChains(ctx)
}

// UserDeposits lists a user's donation history, newest first.
func (s *Service) UserDeposits(ctx context.Context, userID int64, limit int) ([]domain.DonationDeposit, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	return s.store.UserDonationDeposits(ctx, userID, limit)
}

// FormatAssetAmount renders a raw smallest-unit amount (wei, or an ERC-20's
// base units) as a human string with the asset's actual decimal point, for
// chat replies and logs -- never for anything that feeds back into pricing
// math, which stays in raw big.Int form throughout (see pricing.go).
func FormatAssetAmount(amountRaw string, decimals int) string {
	if decimals <= 0 || len(amountRaw) == 0 {
		return amountRaw
	}
	neg := strings.HasPrefix(amountRaw, "-")
	digits := strings.TrimPrefix(amountRaw, "-")
	for len(digits) <= decimals {
		digits = "0" + digits
	}
	intPart, fracPart := digits[:len(digits)-decimals], strings.TrimRight(digits[len(digits)-decimals:], "0")
	out := intPart
	if fracPart != "" {
		out += "." + fracPart
	}
	if neg {
		out = "-" + out
	}
	return out
}
