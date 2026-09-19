package rpc

import (
	"context"
	"errors"
	"strconv"

	"github.com/iamxvbaba/td/tg"
	"github.com/iamxvbaba/td/tgerr"

	"github.com/iamxvbaba/td/tlprofile"
	"telesrv/internal/compat/tdesktop"
	"telesrv/internal/domain"
)

// registerPayments registers payments.* RPCs. telesrv does not implement the
// channel/TON Star Gift economy: getStarsStatus/Subscriptions/Transactions
// serve the real personal Stars ledger (r.deps.Stars) when the target peer is
// the caller themselves, and fall back to a fixed zero-balance response for a
// channel-owned or TON peer (no channel/TON ledger exists yet) -- some client
// UIs load these unconditionally, and an error would hang or crash the
// screen where zero values render as "no Stars".
func (r *Router) registerPayments(d *tlprofile.Dispatcher) {
	registerRPC[*tg.PaymentsCanPurchaseStoreRequest](d, tlprofile.SemanticMethodPaymentsCanPurchaseStore, func(ctx context.Context, req *tg.PaymentsCanPurchaseStoreRequest) (any, error) {
		return r.onPaymentsCanPurchaseStore(ctx, req)
	})
	registerRPC[*tg.PaymentsAssignPlayMarketTransactionRequest](d, tlprofile.SemanticMethodPaymentsAssignPlayMarketTransaction, func(ctx context.Context, req *tg.PaymentsAssignPlayMarketTransactionRequest) (any, error) {
		return r.onPaymentsAssignPlayMarketTransaction(ctx, req)
	})
	registerRPC[*tg.PaymentsGetPremiumGiftCodeOptionsRequest](d, tlprofile.SemanticMethodPaymentsGetPremiumGiftCodeOptions, func(ctx context.Context, req *tg.PaymentsGetPremiumGiftCodeOptionsRequest) (any, error) {
		return r.onPaymentsGetPremiumGiftCodeOptions(ctx, req)
	})
	registerRPC[*tg.PaymentsGetPaymentFormRequest](d, tlprofile.SemanticMethodPaymentsGetPaymentForm, func(ctx context.Context, req *tg.PaymentsGetPaymentFormRequest) (any, error) {
		return r.onPaymentsGetPaymentForm(ctx, req)
	})
	registerRPC[*tg.PaymentsSendStarsFormRequest](d, tlprofile.SemanticMethodPaymentsSendStarsForm, func(ctx context.Context, req *tg.PaymentsSendStarsFormRequest) (any, error) {
		return r.onPaymentsSendStarsForm(ctx, req)
	})
	registerRPC[*tg.PaymentsGetStarsStatusRequest](d, tlprofile.SemanticMethodPaymentsGetStarsStatus, func(ctx context.Context, layerRequest *tg.PaymentsGetStarsStatusRequest) (any, error) {
		return r.onPaymentsGetStarsStatus(ctx, layerRequest)
	})
	registerRPC[*tg.PaymentsGetStarsSubscriptionsRequest](d, tlprofile.SemanticMethodPaymentsGetStarsSubscriptions, func(ctx context.Context, req *tg.PaymentsGetStarsSubscriptionsRequest) (any, error) {
		return r.onPaymentsGetStarsSubscriptions(ctx, req)
	})
	registerRPC[*tg.PaymentsGetStarsTransactionsRequest](d, tlprofile.SemanticMethodPaymentsGetStarsTransactions, func(ctx context.Context, layerRequest *tg.PaymentsGetStarsTransactionsRequest) (any, error) {
		return r.onPaymentsGetStarsTransactions(ctx, layerRequest)
	})
	registerRPC[*tg.PaymentsGetStarsRevenueAdsAccountURLRequest](d, tlprofile.SemanticMethodPaymentsGetStarsRevenueAdsAccountURL, func(ctx context.Context, layerRequest *tg.PaymentsGetStarsRevenueAdsAccountURLRequest) (any, error) {
		peer := layerRequest.
			Peer
		_ = peer

		userID, _, err := r.currentUserID(ctx)
		if err != nil {
			return nil, internalErr()
		}
		if _, err := r.checkedDomainPeerFromInputPeer(ctx, userID, peer); err != nil {
			return nil, err
		}
		return &tg.PaymentsStarsRevenueAdsAccountURL{URL: r.publicLink("")}, nil
	})
	registerRPC[*tg.PaymentsGetStarsRevenueStatsRequest](d, tlprofile.SemanticMethodPaymentsGetStarsRevenueStats, func(ctx context.Context, req *tg.PaymentsGetStarsRevenueStatsRequest) (any, error) {
		return r.onPaymentsGetStarsRevenueStats(ctx, req)
	})
	registerRPC[*tg.PaymentsGetStarGiftsRequest](d, tlprofile.SemanticMethodPaymentsGetStarGifts, func(ctx context.Context, layerRequest *tg.PaymentsGetStarGiftsRequest) (any, error) {
		return r.onPaymentsGetStarGifts(ctx, layerRequest.Hash)
	})
	registerRPC[*tg.PaymentsGetSavedStarGiftsRequest](d, tlprofile.SemanticMethodPaymentsGetSavedStarGifts, func(ctx context.Context, layerRequest *tg.PaymentsGetSavedStarGiftsRequest) (any, error) {
		return r.onPaymentsGetSavedStarGifts(ctx, layerRequest)
	})
	registerRPC[*tg.PaymentsSaveStarGiftRequest](d, tlprofile.SemanticMethodPaymentsSaveStarGift, func(ctx context.Context, layerRequest *tg.PaymentsSaveStarGiftRequest) (any, error) {
		return r.onPaymentsSaveStarGift(ctx, layerRequest)
	})
	registerRPC[*tg.PaymentsConvertStarGiftRequest](d, tlprofile.SemanticMethodPaymentsConvertStarGift, func(ctx context.Context, layerRequest *tg.PaymentsConvertStarGiftRequest) (any, error) {
		return r.onPaymentsConvertStarGift(ctx, layerRequest.Stargift)
	})

}

func (r *Router) onPaymentsCanPurchaseStore(ctx context.Context, _ *tg.PaymentsCanPurchaseStoreRequest) (bool, error) {
	if _, _, err := r.currentUserID(ctx); err != nil {
		return false, internalErr()
	}
	// telesrv deliberately exposes no Google Play products or receipt verifier.
	// DrKLO is steered to the invoice flow by appConfig; if a stale client still
	// reaches this preflight, fail closed instead of authorizing an unverifiable
	// external charge.
	return false, nil
}

func (r *Router) onPaymentsAssignPlayMarketTransaction(ctx context.Context, _ *tg.PaymentsAssignPlayMarketTransactionRequest) (tg.UpdatesClass, error) {
	if _, _, err := r.currentUserID(ctx); err != nil {
		return nil, internalErr()
	}
	return nil, tgerr.New(400, "STORE_PAYMENT_UNAVAILABLE")
}

// onPaymentsGetStarsRevenueStats: telesrv has no channel Star Gift economy, so
// every peer gets the same zero-balance compatibility response (no channel
// ledger branch left to read from).
func (r *Router) onPaymentsGetStarsRevenueStats(ctx context.Context, req *tg.PaymentsGetStarsRevenueStatsRequest) (*tg.PaymentsStarsRevenueStats, error) {
	userID, _, err := r.currentUserID(ctx)
	if err != nil {
		return nil, internalErr()
	}
	if req == nil {
		return nil, peerIDInvalidErr()
	}
	if _, err := r.checkedDomainPeerFromInputPeer(ctx, userID, req.Peer); err != nil {
		return nil, err
	}
	return tdesktop.StarsRevenueStats(req.GetTon()), nil
}

// onPaymentsGetStarsStatus resolves the caller's own Stars balance from the
// real ledger. A channel-owned or TON peer falls back to the fixed
// zero-balance response -- neither ledger exists yet.
func (r *Router) onPaymentsGetStarsStatus(ctx context.Context, req *tg.PaymentsGetStarsStatusRequest) (*tg.PaymentsStarsStatus, error) {
	userID, owner, err := r.starsLedgerOwnerForPeer(ctx, req.GetPeer())
	if err != nil {
		return nil, err
	}
	ton := req != nil && req.GetTon()
	if ton || owner.Type != domain.PeerTypeUser || r.deps.Stars == nil {
		if ton {
			return emptyStarsStatus(&tg.StarsTonAmount{}), nil
		}
		return emptyStarsStatus(&tg.StarsAmount{}), nil
	}
	bal, err := r.deps.Stars.GetBalance(ctx, userID)
	if err != nil {
		return nil, starsErr(err)
	}
	return emptyStarsStatus(&tg.StarsAmount{Amount: bal.Balance}), nil
}

// onPaymentsGetStarsSubscriptions returns the authoritative current balance
// with an empty subscription page. telesrv does not create recurring Stars
// subscriptions yet; returning a well-shaped terminal page lets both official
// clients finish loading the Stars screen without inventing subscription state.
func (r *Router) onPaymentsGetStarsSubscriptions(ctx context.Context, req *tg.PaymentsGetStarsSubscriptionsRequest) (*tg.PaymentsStarsStatus, error) {
	if req == nil || len(req.Offset) > domain.MaxStarsTransactionsOffsetBytes {
		return nil, inputRequestInvalidErr()
	}
	userID, owner, err := r.starsLedgerOwnerForPeer(ctx, req.Peer)
	if err != nil {
		return nil, err
	}
	if owner.Type != domain.PeerTypeUser || owner.ID != userID {
		return nil, peerIDInvalidErr()
	}
	if r.deps.Stars == nil {
		return emptyStarsStatus(&tg.StarsAmount{}), nil
	}
	balance, err := r.deps.Stars.GetBalance(ctx, userID)
	if err != nil {
		return nil, starsErr(err)
	}
	return emptyStarsStatus(&tg.StarsAmount{Amount: balance.Balance}), nil
}

// onPaymentsGetStarsTransactions returns keyset-paginated Stars history for
// the caller's own balance (same envelope as starsStatus). The last page must
// omit next_offset (flag unset), or DrKLO paginates forever. A channel-owned
// or TON peer falls back to the fixed zero-balance, empty-history response.
func (r *Router) onPaymentsGetStarsTransactions(ctx context.Context, req *tg.PaymentsGetStarsTransactionsRequest) (*tg.PaymentsStarsStatus, error) {
	var peer tg.InputPeerClass
	if req != nil {
		peer = req.Peer
	}
	userID, owner, err := r.starsLedgerOwnerForPeer(ctx, peer)
	if err != nil {
		return nil, err
	}
	query, err := starsTransactionQuery(req)
	if err != nil {
		return nil, err
	}
	ton := req != nil && req.GetTon()
	if ton || owner.Type != domain.PeerTypeUser || r.deps.Stars == nil {
		if ton {
			return emptyStarsStatus(&tg.StarsTonAmount{}), nil
		}
		return emptyStarsStatus(&tg.StarsAmount{}), nil
	}
	page, err := r.deps.Stars.ListTransactions(ctx, userID, query)
	if err != nil {
		return nil, starsErr(err)
	}
	out := emptyStarsStatus(&tg.StarsAmount{Amount: page.Balance})
	if txns := tgStarsTransactions(page.Transactions); len(txns) > 0 {
		out.SetHistory(txns)
	}
	if page.NextOffset != "" {
		out.SetNextOffset(page.NextOffset)
	}
	// Enrich the user counterparties mentioned in history (channel
	// counterparties go into Chats once the channel ledger exists).
	if ids := starsTransactionUserIDs(page.Transactions); len(ids) > 0 {
		out.Users = tgUsersForViewer(userID, r.domainUsersForIDs(ctx, userID, ids))
	}
	return out, nil
}

func starsTransactionQuery(req *tg.PaymentsGetStarsTransactionsRequest) (domain.StarsTransactionQuery, error) {
	if req == nil {
		return domain.StarsTransactionQuery{}, inputRequestInvalidErr()
	}
	inbound, outbound := req.GetInbound(), req.GetOutbound()
	if inbound && outbound {
		return domain.StarsTransactionQuery{}, inputRequestInvalidErr()
	}
	if _, ok := req.GetSubscriptionID(); ok {
		// Stars subscriptions are not part of the current business model. Do not
		// silently return the unfiltered ledger for a requested subscription.
		return domain.StarsTransactionQuery{}, subscriptionIDInvalidErr()
	}
	direction := domain.StarsTransactionDirectionAll
	if inbound {
		direction = domain.StarsTransactionDirectionIncoming
	} else if outbound {
		direction = domain.StarsTransactionDirectionOutgoing
	}
	limit := req.Limit
	if limit <= 0 || limit > domain.MaxStarsTransactionsLimit {
		limit = domain.MaxStarsTransactionsLimit
	}
	return domain.StarsTransactionQuery{
		Offset:    req.Offset,
		Limit:     limit,
		Direction: direction,
		Ascending: req.GetAscending(),
	}, nil
}

// starsLedgerOwnerForPeer resolves the caller and the requested ledger owner.
// A self peer is always allowed; a channel peer is resolved (so a bad input
// still fails loudly) but its ledger is not backed yet -- callers fall back to
// the zero-balance response rather than erroring, matching every other
// unimplemented-ledger path in this file.
func (r *Router) starsLedgerOwnerForPeer(ctx context.Context, input tg.InputPeerClass) (int64, domain.Peer, error) {
	userID, _, err := r.currentUserID(ctx)
	if err != nil {
		return 0, domain.Peer{}, internalErr()
	}
	owner, err := r.checkedDomainPeerFromInputPeer(ctx, userID, input)
	if err != nil {
		return 0, domain.Peer{}, err
	}
	if owner.Type == domain.PeerTypeUser && owner.ID != userID {
		return 0, domain.Peer{}, peerIDInvalidErr()
	}
	return userID, owner, nil
}

// emptyStarsStatus builds a minimal valid payments.starsStatus (chats/users
// are non-nullable vectors, but may be empty).
func emptyStarsStatus(balance tg.StarsAmountClass) *tg.PaymentsStarsStatus {
	return &tg.PaymentsStarsStatus{
		Balance: balance,
		Chats:   []tg.ChatClass{},
		Users:   []tg.UserClass{},
	}
}

func tgStarsTransactions(in []domain.StarsTransaction) []tg.StarsTransaction {
	out := make([]tg.StarsTransaction, 0, len(in))
	for _, t := range in {
		item := tg.StarsTransaction{
			ID:     strconv.FormatInt(t.ID, 10),
			Amount: &tg.StarsAmount{Amount: t.Amount},
			Date:   t.Date,
			Peer:   tgStarsTransactionPeer(t),
		}
		if t.Title != "" {
			item.SetTitle(t.Title)
		}
		if t.Description != "" {
			item.SetDescription(t.Description)
		}
		switch t.Reason {
		case domain.StarsReasonReaction:
			item.Reaction = true
		case domain.StarsReasonPaidMessage:
			item.SetPaidMessages(1)
		case domain.StarsReasonGift:
			item.Gift = true
		case domain.StarsReasonGiftUpgrade:
			// Telegram Desktop treats stargift_upgrade as a promise that the
			// optional stargift field contains a unique gift and immediately
			// dereferences its model document while building the history row.
			// The compact ledger projection currently has no StarGift payload,
			// so advertising the flag produces a client-side access violation.
			// Keep the transaction visible through its title/description and only
			// restore this flag together with a complete unique-gift projection.
		case domain.StarsReasonGiftResale:
			item.StargiftResale = true
		case domain.StarsReasonGiftPrepaid:
			item.StargiftPrepaidUpgrade = true
		case domain.StarsReasonGiftDrop:
			item.StargiftDropOriginalDetails = true
		case domain.StarsReasonGiftAuction:
			item.StargiftAuctionBid = true
		case domain.StarsReasonGiftOffer:
			item.Offer = true
		case domain.StarsReasonPremium:
			if t.PremiumMonths > 0 {
				item.SetPremiumGiftMonths(t.PremiumMonths)
			}
		}
		out = append(out, item)
	}
	return out
}

// tgStarsTransactionPeer picks the counterparty constructor: grant/topup go
// through Fragment (the off-platform top-up rail), Premium through the
// Premium bot, a real peer through tgPeer, and anything else falls back to
// Unsupported (the Peer field is required, never nil).
func tgStarsTransactionPeer(t domain.StarsTransaction) tg.StarsTransactionPeerClass {
	switch t.Reason {
	case domain.StarsReasonGrant, domain.StarsReasonTopup:
		return &tg.StarsTransactionPeerFragment{}
	case domain.StarsReasonPremium:
		return &tg.StarsTransactionPeerPremiumBot{}
	}
	if t.Peer.Type != "" && t.Peer.ID != 0 {
		if p := tgPeer(t.Peer); p != nil {
			return &tg.StarsTransactionPeer{Peer: p}
		}
	}
	return &tg.StarsTransactionPeerUnsupported{}
}

// starsTransactionUserIDs collects the deduplicated user counterparty ids
// mentioned in a page of transactions.
func starsTransactionUserIDs(in []domain.StarsTransaction) []int64 {
	seen := make(map[int64]struct{}, len(in))
	ids := make([]int64, 0, len(in))
	for _, t := range in {
		if t.Peer.Type != domain.PeerTypeUser || t.Peer.ID == 0 {
			continue
		}
		if _, ok := seen[t.Peer.ID]; ok {
			continue
		}
		seen[t.Peer.ID] = struct{}{}
		ids = append(ids, t.Peer.ID)
	}
	return ids
}

// starsErr maps a Stars ledger domain error to a client-recognizable tgerr.
func starsErr(err error) error {
	switch {
	case errors.Is(err, domain.ErrStarsInsufficient):
		return balanceTooLowErr()
	case errors.Is(err, domain.ErrStarsInvalidAmount):
		return starsAmountInvalidErr()
	default:
		return internalErr()
	}
}
