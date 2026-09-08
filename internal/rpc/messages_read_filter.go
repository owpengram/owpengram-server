package rpc

import (
	"context"

	"github.com/iamxvbaba/td/tg"

	"telesrv/internal/domain"
)

type privateSendBaseUserResolver interface {
	BaseUsersByIDs(ctx context.Context, userIDs []int64) ([]domain.User, error)
}

// Read endpoints retain their existing positive page caps and signed
// add_offset semantics. Invalid IDs must not become an unbounded query.
func validateMessageReadBounds(limit, offsetID, maxID, minID int) error {
	if limit < 0 {
		return limitInvalidErr()
	}
	for _, id := range [...]int{offsetID, maxID, minID} {
		if id < 0 || id > domain.MaxMessageBoxID {
			return msgIDInvalidErr()
		}
	}
	return nil
}

// checkedMessageReadPeer never treats an invalid reference as global scope.
// Only the search parser admits InputPeerEmpty, explicitly. Base identity
// lookup avoids loading viewer-specific user projections for count requests.
func (r *Router) checkedMessageReadPeer(ctx context.Context, userID int64, input tg.InputPeerClass, sender bool) (domain.Peer, error) {
	invalid := peerIDInvalidErr
	if sender {
		invalid = fromPeerInvalidErr
	}
	if inputPeerClassNil(input) {
		return domain.Peer{}, invalid()
	}
	switch peer := input.(type) {
	case *tg.InputPeerSelf:
		if userID <= 0 {
			return domain.Peer{}, invalid()
		}
		return domain.Peer{Type: domain.PeerTypeUser, ID: userID}, nil
	case *tg.InputPeerUser:
		if peer.UserID <= 0 {
			return domain.Peer{}, invalid()
		}
		if r.deps.Users == nil {
			return domain.Peer{}, internalErr()
		}
		if resolver, ok := r.deps.Users.(privateSendBaseUserResolver); ok {
			users, err := resolver.BaseUsersByIDs(ctx, []int64{peer.UserID})
			if err != nil {
				return domain.Peer{}, internalErr()
			}
			for _, user := range users {
				if user.ID == peer.UserID && user.AccessHash == peer.AccessHash {
					return domain.Peer{Type: domain.PeerTypeUser, ID: peer.UserID}, nil
				}
			}
			return domain.Peer{}, invalid()
		}
		user, found, err := r.deps.Users.ByID(ctx, userID, peer.UserID)
		if err != nil {
			return domain.Peer{}, internalErr()
		}
		if found && user.AccessHash == peer.AccessHash {
			return domain.Peer{Type: domain.PeerTypeUser, ID: peer.UserID}, nil
		}
		return domain.Peer{}, invalid()
	default:
		if sender {
			return domain.Peer{}, invalid()
		}
		resolved, ok := r.domainPeerFromInputPeer(userID, input)
		if !ok || resolved.ID <= 0 {
			return domain.Peer{}, invalid()
		}
		return r.checkedDomainPeerFromInputPeer(ctx, userID, input)
	}
}
