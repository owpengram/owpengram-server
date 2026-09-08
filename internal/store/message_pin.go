package store

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"

	"telesrv/internal/domain"
)

// PrivatePinServiceRequest prepares the service receipt for a shared pin.
// Pin state, checked under the store's pair lock, is the command's idempotency
// boundary. A new receipt key allows unpin/repin even at the same timestamp.
func PrivatePinServiceRequest(req domain.PinPrivateMessageRequest) (domain.SendPrivateTextRequest, error) {
	var key [8]byte
	var id int64
	for id == 0 {
		if _, err := rand.Read(key[:]); err != nil {
			return domain.SendPrivateTextRequest{}, fmt.Errorf("pin service receipt key: %w", err)
		}
		id = int64(binary.LittleEndian.Uint64(key[:]))
	}
	return domain.SendPrivateTextRequest{
		SenderUserID: req.OwnerUserID, RecipientUserID: req.Peer.ID, RandomID: id,
		Media: &domain.MessageMedia{Kind: domain.MessageMediaKindService,
			ServiceAction: &domain.MessageServiceAction{Kind: domain.MessageServiceActionPinMessage}},
		ReplyTo: &domain.MessageReply{MessageID: req.MessageID, Peer: req.Peer},
		Silent:  req.Silent, Date: req.Date, RecipientBlocked: req.RecipientBlocked,
		OriginAuthKeyID: req.OriginAuthKeyID, OriginSessionID: req.OriginSessionID,
	}, nil
}
