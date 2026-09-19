package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"telesrv/internal/domain"
)

// IssueChannelRevenueWithdrawal/ResolveChannelRevenueWithdrawal/
// CompleteChannelRevenueWithdrawal settle a channel's TON ad-revenue
// withdrawal requests. Channel ad-revenue withdrawal is a separate, broader
// monetization feature this port does not implement; these stubs exist only
// to satisfy store.StarGiftLifecycleStore, whose interface groups them in
// alongside the Star Gift channel-TON-ledger methods upstream.
func (s *StarGiftLifecycleStore) IssueChannelRevenueWithdrawal(ctx context.Context, req domain.ChannelRevenueWithdrawalRequest) (domain.ChannelRevenueWithdrawal, error) {
	return domain.ChannelRevenueWithdrawal{}, fmt.Errorf("channel revenue withdrawal is not implemented")
}

func (s *StarGiftLifecycleStore) ResolveChannelRevenueWithdrawal(ctx context.Context, tokenDigest []byte) (domain.ChannelRevenueWithdrawal, bool, error) {
	return domain.ChannelRevenueWithdrawal{}, false, nil
}

func (s *StarGiftLifecycleStore) CompleteChannelRevenueWithdrawal(ctx context.Context, tokenDigest []byte, date int) (domain.ChannelRevenueWithdrawal, error) {
	return domain.ChannelRevenueWithdrawal{}, fmt.Errorf("channel revenue withdrawal is not implemented")
}

// AppendStarGiftAdminLog is the store.ChannelsService entry point used by
// the base payments_star_gifts.go RPC handler for a gift sent to a channel:
// it posts the messageActionStarGift service message as a real, visible
// channel post (self-managed transaction, same as AppendCallServiceMessage)
// and discards the delivery result the caller does not need.
func (s *ChannelStore) AppendStarGiftAdminLog(ctx context.Context, channelID, senderUserID, savedID int64, date int, action domain.ChannelMessageAction) error {
	_, err := s.appendServiceMessage(ctx, "star_gift", channelID, senderUserID, date, action)
	return err
}

// appendStarGiftAdminLogTx is the transaction-scoped counterpart used by the
// copied lifecycle/craft-auction files, which need the admin-log write to
// commit atomically with other steps of the same channel-gift operation
// (already inside an external tx). Channel-owned Star Gifts (and the TON
// ledger they'd settle against) are not implemented yet, so nothing reaches
// either of these on a live deployment today.
func (s *ChannelStore) appendStarGiftAdminLogTx(ctx context.Context, tx pgx.Tx, channelID, senderUserID, savedID int64, date int, action domain.ChannelMessageAction) error {
	if channelID == 0 || senderUserID == 0 || savedID <= 0 {
		return domain.ErrChannelInvalid
	}
	channel, err := getChannelByID(ctx, tx, channelID)
	if err != nil {
		return err
	}
	messageID := int(savedID)
	if savedID > int64(domain.MaxMessageBoxID) {
		messageID = domain.MaxMessageBoxID
	}
	action = channelServiceActionForMessage(channelID, messageID, action)
	msg := domain.ChannelMessage{
		ChannelID: channelID, ID: messageID, SenderUserID: senderUserID,
		From: domain.Peer{Type: domain.PeerTypeUser, ID: senderUserID}, Date: date,
		Post: channel.Broadcast, Action: &action, Pts: channel.Pts,
	}
	return s.insertChannelAdminLogTx(ctx, tx, domain.ChannelAdminLogEvent{
		ChannelID: channelID, UserID: senderUserID, Date: date,
		Type: domain.ChannelAdminLogSendMessage, Message: &msg,
	})
}

// channelServiceActionForMessage fills in the message-id-dependent fields a
// channel gift's service action needs (PeerChannelID/SavedID) once the
// message id that will carry it is known.
func channelServiceActionForMessage(channelID int64, msgID int, action domain.ChannelMessageAction) domain.ChannelMessageAction {
	if action.Type == domain.ChannelActionStarGift && action.StarGift != nil {
		g := *action.StarGift
		if g.PeerChannelID == 0 {
			g.PeerChannelID = channelID
		}
		if g.SavedID == 0 {
			g.SavedID = int64(msgID)
		}
		action.StarGift = &g
	}
	return action
}
