package rpc

import (
	"context"

	"telesrv/internal/domain"
)

// RevokeAuthorizationAuthKey is the domain-only hook used by the internal Admin API
// after the auth service has removed the durable authorization/auth_key rows.
func (r *Router) RevokeAuthorizationAuthKey(ctx context.Context, authKeyID [8]byte, userID int64) error {
	if r == nil || authKeyID == ([8]byte{}) {
		return nil
	}
	r.revokeAuthKeySessions(authKeyID)
	if err := r.clearAuthKeyState(ctx, authKeyID); err != nil {
		return err
	}
	if userID != 0 {
		r.discardSecretChatsForAuthKey(ctx, businessAuthKeyInt64(authKeyID), userID)
	}
	return nil
}

// NotifyChannelChanged is the domain-only hook used by the internal Admin API
// after a channel/supergroup base fact changed.
func (r *Router) NotifyChannelChanged(ctx context.Context, ch domain.Channel) error {
	if r == nil || ch.ID == 0 {
		return nil
	}
	r.channelStateMutationUpdates(ctx, ch.CreatorUserID, ch)
	return nil
}

// NotifyAccountFreezeChanged invalidates target-scoped projections immediately
// and wakes the durable audience nudge worker. Cross-instance cache invalidation
// is also carried by the committed user_visibility read-model notification.
func (r *Router) NotifyAccountFreezeChanged(_ context.Context, freeze domain.AccountFreeze) error {
	if r == nil || freeze.UserID == 0 {
		return nil
	}
	if r.deps.UserProjectionFacts != nil {
		r.deps.UserProjectionFacts.InvalidateAccountFreezeFact(freeze.UserID)
	}
	r.invalidateRPCProjectionForUser(freeze.UserID)
	if r.accountFreezeWake != nil {
		select {
		case r.accountFreezeWake <- struct{}{}:
		default:
		}
	}
	return nil
}
