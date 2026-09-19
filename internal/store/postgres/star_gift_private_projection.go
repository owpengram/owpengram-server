package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"telesrv/internal/domain"
)

// projectPrivateStarGiftSourceRef exposes a user-owned gift's stable source
// message identity only in the gift owner's message-box projection. Telegram
// defines gift_msg_id as receiver-only. A non-owner counterpart box id is not
// a valid substitute: it could resolve to an unrelated gift owned by that
// viewer. User unique actions do not use channel-only peer/saved_id fields;
// their owner-scoped message ids are registered separately at write time.
func projectPrivateStarGiftSourceRef(
	_ context.Context,
	_ pgx.Tx,
	req *domain.SendPrivateTextRequest,
	sourceOwnerUserID int64,
	sourceOwnerBoxID int,
) (privateSendMediaProjection, error) {
	if req == nil || req.Media == nil || sourceOwnerUserID <= 0 || sourceOwnerBoxID <= 0 ||
		(sourceOwnerUserID != req.SenderUserID && sourceOwnerUserID != req.RecipientUserID) {
		return privateSendMediaProjection{}, fmt.Errorf("project private star gift source: invalid scope")
	}

	shared, err := cloneMessageMedia(req.Media)
	if err != nil {
		return privateSendMediaProjection{}, err
	}
	sender, err := cloneMessageMedia(req.Media)
	if err != nil {
		return privateSendMediaProjection{}, err
	}
	recipient, err := cloneMessageMedia(req.Media)
	if err != nil {
		return privateSendMediaProjection{}, err
	}

	switch {
	case privateStarGiftAction(shared) != nil:
		sharedAction := privateStarGiftAction(shared)
		senderAction := privateStarGiftAction(sender)
		recipientAction := privateStarGiftAction(recipient)
		if sharedAction.GiftMsgID != sourceOwnerBoxID {
			return privateSendMediaProjection{}, fmt.Errorf(
				"project private star gift source: gift_msg_id %d does not match owner box %d",
				sharedAction.GiftMsgID, sourceOwnerBoxID,
			)
		}
		sharedAction.GiftMsgID = 0
		senderAction.GiftMsgID = 0
		recipientAction.GiftMsgID = 0
		// messageActionStarGift.can_upgrade is receiver-only. A separate
		// prepayment notification shares one logical private message, but its
		// payer box must not advertise owner actions.
		sharedAction.CanUpgrade = false
		senderAction.CanUpgrade = req.SenderUserID == sourceOwnerUserID && senderAction.CanUpgrade
		recipientAction.CanUpgrade = req.RecipientUserID == sourceOwnerUserID && recipientAction.CanUpgrade
		if req.SenderUserID == sourceOwnerUserID {
			senderAction.GiftMsgID = sourceOwnerBoxID
		} else {
			recipientAction.GiftMsgID = sourceOwnerBoxID
		}
	default:
		return privateSendMediaProjection{}, fmt.Errorf("project private star gift source: unsupported media")
	}

	return privateSendMediaProjection{Shared: shared, Sender: sender, Recipient: recipient}, nil
}

// projectPrivateStarGiftPurchase scopes the two mutually exclusive actions of
// an ordinary user gift to the correct account-local message box:
//   - the gift owner/receiver may upgrade it and must not receive the separate
//     prepayment hash;
//   - the non-owner sender may use the hash to prepay for the owner's upgrade,
//     but must not receive the receiver-only can_upgrade capability.
//
// The shared logical envelope carries neither viewer-only field. This keeps a
// future read/replay path from accidentally treating it as either participant's
// projection while the two message_boxes remain the durable wire truth.
func projectPrivateStarGiftPurchase(
	_ context.Context,
	_ pgx.Tx,
	req *domain.SendPrivateTextRequest,
) (privateSendMediaProjection, error) {
	if req == nil || req.Media == nil || req.SenderUserID <= 0 || req.RecipientUserID <= 0 {
		return privateSendMediaProjection{}, fmt.Errorf("project private star gift purchase: invalid scope")
	}
	action := privateStarGiftAction(req.Media)
	if action == nil {
		return privateSendMediaProjection{}, fmt.Errorf("project private star gift purchase: unsupported media")
	}
	ownerUserID := action.PeerUserID
	if ownerUserID == 0 && action.To.Type == domain.PeerTypeUser {
		ownerUserID = action.To.ID
	}
	if ownerUserID <= 0 || ownerUserID != req.RecipientUserID {
		return privateSendMediaProjection{}, fmt.Errorf(
			"project private star gift purchase: owner %d does not match recipient %d",
			ownerUserID, req.RecipientUserID,
		)
	}

	shared, err := cloneMessageMedia(req.Media)
	if err != nil {
		return privateSendMediaProjection{}, err
	}
	sender, err := cloneMessageMedia(req.Media)
	if err != nil {
		return privateSendMediaProjection{}, err
	}
	recipient, err := cloneMessageMedia(req.Media)
	if err != nil {
		return privateSendMediaProjection{}, err
	}

	sharedAction := privateStarGiftAction(shared)
	senderAction := privateStarGiftAction(sender)
	recipientAction := privateStarGiftAction(recipient)
	sharedAction.PrepaidUpgradeHash = ""
	sharedAction.CanUpgrade = false
	if req.SenderUserID == ownerUserID {
		senderAction.PrepaidUpgradeHash = ""
	} else {
		senderAction.CanUpgrade = false
	}
	recipientAction.PrepaidUpgradeHash = ""
	return privateSendMediaProjection{Shared: shared, Sender: sender, Recipient: recipient}, nil
}

func cloneMessageMedia(media *domain.MessageMedia) (*domain.MessageMedia, error) {
	encoded, err := encodeMessageMedia(media)
	if err != nil {
		return nil, fmt.Errorf("clone private message media: %w", err)
	}
	cloned, err := decodeMessageMedia(string(encoded))
	if err != nil {
		return nil, fmt.Errorf("clone private message media: %w", err)
	}
	return cloned, nil
}

// encodeSharedPrivateStarGiftMedia returns the logical private-message
// envelope for an already viewpoint-projected Star Gift service message.
// Conversation message ids belong to a single owner's message_boxes
// namespace, so the shared row must never retain them. saved_id is likewise
// box-local for user gifts, while channel saved ids remain globally meaningful
// inside the channel gift namespace.
func encodeSharedPrivateStarGiftMedia(media *domain.MessageMedia) ([]byte, error) {
	shared, err := cloneMessageMedia(media)
	if err != nil {
		return nil, err
	}

	switch {
	case privateStarGiftAction(shared) != nil:
		action := privateStarGiftAction(shared)
		action.GiftMsgID = 0
		action.UpgradeMsgID = 0
		if action.PeerUserID > 0 || action.To.Type == domain.PeerTypeUser {
			action.SavedID = 0
			action.PrepaidUpgradeHash = ""
			action.CanUpgrade = false
		}
	case privateStarGiftUniqueAction(shared) != nil:
		action := privateStarGiftUniqueAction(shared)
		if action.Peer.Type == domain.PeerTypeUser {
			action.SavedID = 0
		}
	default:
		return nil, fmt.Errorf("encode shared private star gift media: unsupported media")
	}

	encoded, err := encodeMessageMedia(shared)
	if err != nil {
		return nil, fmt.Errorf("encode shared private star gift media: %w", err)
	}
	return encoded, nil
}

func privateStarGiftAction(media *domain.MessageMedia) *domain.MessageStarGiftAction {
	if media == nil || media.Kind != domain.MessageMediaKindService || media.ServiceAction == nil ||
		media.ServiceAction.Kind != domain.MessageServiceActionStarGift {
		return nil
	}
	return media.ServiceAction.StarGift
}

func privateStarGiftUniqueAction(media *domain.MessageMedia) *domain.MessageStarGiftUniqueAction {
	if media == nil || media.Kind != domain.MessageMediaKindService || media.ServiceAction == nil ||
		media.ServiceAction.Kind != domain.MessageServiceActionStarGiftUnique {
		return nil
	}
	return media.ServiceAction.StarGiftUnique
}
