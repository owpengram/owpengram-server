package memory

import (
	"context"
	"telesrv/internal/domain"
	"time"
)

func (s *ChannelStore) GetChannelMessageViews(_ context.Context, req domain.ChannelMessageViewsRequest) (domain.ChannelMessageViewsResult, error) {
	if req.UserID == 0 || req.ChannelID == 0 {
		return domain.ChannelMessageViewsResult{}, domain.ErrChannelInvalid
	}
	if len(req.IDs) == 0 {
		return domain.ChannelMessageViewsResult{Views: map[int]int{}, Replies: map[int]*domain.ChannelMessageReplies{}}, nil
	}
	if len(req.IDs) > domain.MaxGetMessageIDs {
		return domain.ChannelMessageViewsResult{}, domain.ErrChannelInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	channel, member, _, err := s.channelForViewerLocked(req.UserID, req.ChannelID)
	if err != nil {
		return domain.ChannelMessageViewsResult{}, err
	}
	wanted := make(map[int]struct{}, len(req.IDs))
	for _, id := range req.IDs {
		if id <= 0 || id > domain.MaxMessageBoxID {
			return domain.ChannelMessageViewsResult{}, domain.ErrMessageIDInvalid
		}
		wanted[id] = struct{}{}
	}
	visible := make(map[int]struct{}, len(wanted))
	for _, msg := range s.messages[req.ChannelID] {
		if _, ok := wanted[msg.ID]; !ok {
			continue
		}
		if msg.Deleted || msg.ID <= member.AvailableMinID ||
			!channelMessageVisibleToViewerLocked(channel, member, req.UserID, msg) {
			continue
		}
		visible[msg.ID] = struct{}{}
	}
	if s.msgViews[req.ChannelID] == nil {
		s.msgViews[req.ChannelID] = make(map[int]int)
	}
	if s.msgViewers[req.ChannelID] == nil {
		s.msgViewers[req.ChannelID] = make(map[int]map[int64]int)
	}
	viewedAt := req.Date
	if viewedAt <= 0 {
		viewedAt = int(time.Now().Unix())
	}
	for id := range visible {
		if req.Increment {
			if s.msgViewers[req.ChannelID][id] == nil {
				s.msgViewers[req.ChannelID][id] = make(map[int64]int)
			}
			if _, seen := s.msgViewers[req.ChannelID][id][req.UserID]; !seen {
				s.msgViewers[req.ChannelID][id][req.UserID] = viewedAt
				s.msgViews[req.ChannelID][id]++
				if idx, ok := s.findMessageIndexLocked(req.ChannelID, id); ok {
					msg := s.messages[req.ChannelID][idx]
					msg.ViewsCount = s.msgViews[req.ChannelID][id]
					s.messages[req.ChannelID][idx] = msg
				}
			}
		}
	}
	out := make(map[int]int, len(visible))
	replies := make(map[int]*domain.ChannelMessageReplies, len(visible))
	peerSeen := make(map[domain.Peer]struct{}, len(visible))
	peers := make([]domain.Peer, 0, len(visible))
	for id := range visible {
		out[id] = s.msgViews[req.ChannelID][id]
	}
	for _, msg := range s.messages[req.ChannelID] {
		if _, ok := visible[msg.ID]; !ok {
			continue
		}
		peer := msg.From
		if peer.ID == 0 && msg.SenderUserID != 0 {
			peer = domain.Peer{Type: domain.PeerTypeUser, ID: msg.SenderUserID}
		}
		if peer.ID != 0 {
			if _, ok := peerSeen[peer]; !ok {
				peerSeen[peer] = struct{}{}
				peers = append(peers, peer)
			}
		}
		if reply := s.channelMessageRepliesLocked(req.UserID, req.ChannelID, msg); reply != nil {
			replies[msg.ID] = reply
		}
	}
	return domain.ChannelMessageViewsResult{Channel: channel, Views: out, Replies: replies, Peers: peers}, nil
}
