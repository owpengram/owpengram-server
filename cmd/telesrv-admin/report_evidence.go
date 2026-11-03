package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"telesrv/internal/domain"
)

// A report carries a frozen snapshot of what was reported -- the message,
// post, story or photo exactly as the reporter saw it, plus holds that keep
// its files from being purged. A reviewer sees that snapshot as it looked in
// the app, without the private-content password: the reporter handed it
// over, and the review can't be done without it. Only files the report
// itself holds can be fetched this way.

// ReportEvidenceItem is one reported thing, drawn like a chat message.
type ReportEvidenceItem struct {
	Kind    string       `json:"kind"` // message, story, profile_photo, reaction, peer, other
	Peer    ChatPeerRef  `json:"peer"`
	Message *ChatMessage `json:"message,omitempty"`
}

// ChatPeerRef names the chat or owner an evidence item belongs to.
type ChatPeerRef struct {
	Type string `json:"type"`
	ID   int64  `json:"id"`
}

type reportEvidenceResponse struct {
	Items   []ReportEvidenceItem `json:"items"`
	Peers   map[string]ChatPeer  `json:"peers"`
	Replies map[string]ChatReply `json:"replies"`
}

func (s *server) fetchModerationReport(ctx context.Context, id int64) (domain.ModerationReport, error) {
	var report domain.ModerationReport
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/v1/moderation/reports/%d", s.cfg.AdminAPIURL, id), nil)
	if err != nil {
		return report, err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.AdminAPIToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return report, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return report, fmt.Errorf("moderation report: status %d", resp.StatusCode)
	}
	return report, json.Unmarshal(raw, &report)
}

// evidenceMessage is the subset of the private/channel message snapshots
// (moderation evidence v1) the viewer draws.
type evidenceMessage struct {
	MessageID    int                    `json:"message_id"`
	SenderUserID int64                  `json:"sender_user_id"`
	From         domain.Peer            `json:"from"`
	Date         int64                  `json:"date"`
	EditDate     int                    `json:"edit_date"`
	Post         bool                   `json:"post"`
	Body         string                 `json:"body"`
	ReplyTo      *domain.MessageReply   `json:"reply_to"`
	Forward      *domain.MessageForward `json:"forward"`
	Media        json.RawMessage        `json:"media"`
	Action       json.RawMessage        `json:"action"`
	// Story snapshot fields.
	StoryID int         `json:"story_id"`
	Caption string      `json:"caption"`
	Owner   domain.Peer `json:"owner"`
	// Profile photo snapshot.
	Photo json.RawMessage `json:"photo"`
	// Reaction snapshots wrap the message.
	Message *evidenceMessage `json:"message"`
}

func evidenceToChat(item domain.ModerationReportItem) ReportEvidenceItem {
	out := ReportEvidenceItem{Kind: string(item.Kind), Peer: ChatPeerRef{Type: string(item.Peer.Type), ID: item.Peer.ID}}
	var ev evidenceMessage
	if len(item.Evidence) == 0 || json.Unmarshal(item.Evidence, &ev) != nil {
		return out
	}
	if ev.Message != nil {
		ev = *ev.Message
	}
	m := &ChatMessage{ID: ev.MessageID, Date: ev.Date, EditDate: ev.EditDate, Post: ev.Post, Text: ev.Body}
	switch item.Kind {
	case domain.ModerationItemStory:
		m.ID = ev.StoryID
		m.Text = ev.Caption
		m.FromType, m.FromID = string(ev.Owner.Type), ev.Owner.ID
	case domain.ModerationItemProfilePhoto:
		m.FromType, m.FromID = string(ev.Owner.Type), ev.Owner.ID
		if len(ev.Photo) > 0 {
			ev.Media, _ = json.Marshal(map[string]json.RawMessage{"kind": json.RawMessage(`"photo"`), "photo": ev.Photo})
		}
	default:
		m.FromType, m.FromID = string(ev.From.Type), ev.From.ID
		if m.FromID == 0 && ev.SenderUserID > 0 {
			m.FromType, m.FromID = "user", ev.SenderUserID
		}
	}
	if m.FromType == "" {
		m.FromType = "user"
	}
	if ev.ReplyTo != nil {
		m.ReplyToID = ev.ReplyTo.MessageID
	}
	if ev.Forward != nil {
		m.FwdFromType, m.FwdFromID, m.FwdFromName = string(ev.Forward.From.Type), ev.Forward.From.ID, ev.Forward.FromName
	}
	m.Media, m.Service = chatMediaFromJSON(ev.Media)
	if svc := channelActionKind(ev.Action); svc != "" && svc != "service" {
		m.Service = svc
	}
	if m.Date == 0 && m.Text == "" && m.Media == nil && m.Service == "" {
		return out
	}
	out.Message = m
	return out
}

func (s *server) handleModerationReportEvidenceAPI(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid moderation report id")
		return
	}
	report, err := s.fetchModerationReport(r.Context(), id)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	resp := reportEvidenceResponse{Items: []ReportEvidenceItem{}, Peers: map[string]ChatPeer{}, Replies: map[string]ChatReply{}}
	page := newChatPage()
	for _, item := range report.Items {
		converted := evidenceToChat(item)
		resp.Items = append(resp.Items, converted)
		if converted.Message != nil {
			page.Messages = append(page.Messages, *converted.Message)
		}
	}
	// Name the senders and the chats the items came from.
	for _, item := range resp.Items {
		if item.Peer.ID > 0 {
			page.Messages = append(page.Messages, ChatMessage{FromType: item.Peer.Type, FromID: item.Peer.ID})
		}
	}
	page.Messages = append(page.Messages, ChatMessage{FromType: "user", FromID: report.ReporterUserID})
	if s.read != nil {
		if err := s.read.fillPeers(r.Context(), &page); err != nil {
			writeAPIError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	resp.Peers = page.Peers
	writeJSON(w, http.StatusOK, resp)
}

// handleModerationReportMediaAPI streams one file the report holds.
func (s *server) handleModerationReportMediaAPI(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid moderation report id")
		return
	}
	key := r.URL.Query().Get("key")
	report, err := s.fetchModerationReport(r.Context(), id)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	held := false
	for _, hold := range report.MediaHolds {
		if hold.StorageKey == key {
			held = true
			break
		}
	}
	if !held {
		writeAPIError(w, http.StatusForbidden, "this file is not part of the report")
		return
	}
	s.streamStoredFile(w, r, key)
}
