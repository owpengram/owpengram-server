package rpc

import (
	"context"

	"github.com/owpengram/owpengram-td/tg"

	"telesrv/internal/domain"
)

// tgSelfUserWithUsernames is the narrow single-object projection used by
// authorization results and self-profile updates. Those constructors already
// carry a complete User, so it must include the username registry without
// querying unrelated story or bot-verification read models.
//
// When a collectible registry vector exists, applyUsernamesToPeerObjects clears
// the legacy scalar username. Official clients treat the scalar and vector as
// alternative representations; emitting both makes TDLib reject the username
// set as malformed.
func (r *Router) tgSelfUserWithUsernames(ctx context.Context, u domain.User) *tg.User {
	self := r.tgSelfUser(u)
	users := []tg.UserClass{self}
	r.applyUsernamesToPeerObjects(ctx, users, nil)
	return self
}
