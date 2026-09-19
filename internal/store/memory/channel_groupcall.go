package memory

import (
	"context"

	"telesrv/internal/domain"
)

// SetActiveCall 写入/清除 channel 行上的活跃群通话关联。
func (s *ChannelStore) SetActiveCall(_ context.Context, channelID, callID, callAccessHash int64, notEmpty bool) (domain.Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.channels[channelID]
	if !ok || ch.Deleted {
		return domain.Channel{}, domain.ErrChannelInvalid
	}
	ch.ActiveCallID = callID
	ch.ActiveCallAccessHash = callAccessHash
	ch.ActiveCallNotEmpty = notEmpty && callID != 0
	s.channels[channelID] = ch
	return ch, nil
}

// AppendCallServiceMessage 生成群通话服务消息（带频道 pts）。
func (s *ChannelStore) AppendCallServiceMessage(_ context.Context, channelID, senderUserID int64, date int, action domain.ChannelMessageAction) (domain.SendChannelMessageResult, error) {
	return s.appendServiceMessageLocked(channelID, senderUserID, date, action)
}

// AppendStarGiftAdminLog posts the messageActionStarGift service message
// for a gift a channel received.
func (s *ChannelStore) AppendStarGiftAdminLog(_ context.Context, channelID, senderUserID, _ int64, date int, action domain.ChannelMessageAction) error {
	_, err := s.appendServiceMessageLocked(channelID, senderUserID, date, action)
	return err
}

func (s *ChannelStore) appendServiceMessageLocked(channelID, senderUserID int64, date int, action domain.ChannelMessageAction) (domain.SendChannelMessageResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.channels[channelID]
	if !ok || ch.Deleted {
		return domain.SendChannelMessageResult{}, domain.ErrChannelInvalid
	}
	msg, event := s.appendChannelServiceMessageLocked(channelID, senderUserID, date, action)
	ch = s.channels[channelID]
	ch.TopMessageID = msg.ID
	ch.Pts = event.Pts
	s.channels[channelID] = ch
	return domain.SendChannelMessageResult{
		Channel:    ch,
		Message:    msg,
		Event:      event,
		Recipients: s.activeMemberIDsLocked(channelID, 0, 0),
	}, nil
}
