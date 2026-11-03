package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"telesrv/internal/domain"
)

// The private-content viewer: an account's chats, a channel's posts and
// comments, profile photos, stories and gifts, laid out the way a client
// shows them. Every read here sits behind a password re-check and an audit
// row (see privateview.go); this file is only the projection.

const (
	chatPageLimit     = 50
	chatListLimit     = 500
	profileMediaLimit = 200
)

// ChatListItem is one row of an account's chat list: a private dialog, or a
// group/channel the account is (or was) a member of.
type ChatListItem struct {
	Kind         string `json:"kind"` // user, group, channel
	PeerID       int64  `json:"peer_id"`
	Title        string `json:"title"`
	Username     string `json:"username"`
	Bot          bool   `json:"bot,omitempty"`
	Self         bool   `json:"self,omitempty"`
	Role         string `json:"role,omitempty"`
	Status       string `json:"status,omitempty"` // membership: active, left, kicked, banned
	Unread       int    `json:"unread,omitempty"`
	Pinned       bool   `json:"pinned,omitempty"`
	LastDate     int64  `json:"last_date"`
	LastText     string `json:"last_text"`
	LastMedia    string `json:"last_media,omitempty"`
	LastOutgoing bool   `json:"last_outgoing,omitempty"`
}

// ChatMessage is one message as the viewer draws it, private or channel.
type ChatMessage struct {
	ID          int        `json:"id"`
	Date        int64      `json:"date"`
	EditDate    int        `json:"edit_date,omitempty"`
	FromType    string     `json:"from_type"` // user, channel
	FromID      int64      `json:"from_id"`
	Out         bool       `json:"out,omitempty"`
	Text        string     `json:"text"`
	Deleted     bool       `json:"deleted,omitempty"`
	Pinned      bool       `json:"pinned,omitempty"`
	Post        bool       `json:"post,omitempty"`
	PostAuthor  string     `json:"post_author,omitempty"`
	Views       int        `json:"views,omitempty"`
	ReplyToID   int        `json:"reply_to_id,omitempty"`
	FwdFromType string     `json:"fwd_from_type,omitempty"`
	FwdFromID   int64      `json:"fwd_from_id,omitempty"`
	FwdFromName string     `json:"fwd_from_name,omitempty"`
	ViaBotID    int64      `json:"via_bot_id,omitempty"`
	Media       *ChatMedia `json:"media,omitempty"`
	Service     string     `json:"service,omitempty"`
	Comments    int        `json:"comments,omitempty"`
	HasThread   bool       `json:"has_thread,omitempty"`
}

// ChatReply is the quoted line above a reply.
type ChatReply struct {
	FromType string `json:"from_type"`
	FromID   int64  `json:"from_id"`
	Text     string `json:"text"`
	Media    string `json:"media,omitempty"`
}

// ChatMedia is a message attachment reduced to what the viewer renders. File
// fields are storage keys the panel's media route streams (thumb for the
// bubble, full for the lightbox/player).
type ChatMedia struct {
	Kind     string  `json:"kind"` // photo, video, round, gif, sticker, voice, audio, file, contact, geo, poll, webpage, dice, story, other
	Thumb    string  `json:"thumb,omitempty"`
	Full     string  `json:"full,omitempty"`
	W        int     `json:"w,omitempty"`
	H        int     `json:"h,omitempty"`
	Duration float64 `json:"duration,omitempty"`
	MimeType string  `json:"mime_type,omitempty"`
	FileName string  `json:"file_name,omitempty"`
	Size     int64   `json:"size,omitempty"`
	Title    string  `json:"title,omitempty"`
	Subtitle string  `json:"subtitle,omitempty"`
	Spoiler  bool    `json:"spoiler,omitempty"`
	Animated bool    `json:"animated,omitempty"` // a TGS sticker: Full is gzip Lottie
}

// ChatPeer names a user or channel that appears in a page of messages.
type ChatPeer struct {
	Name     string `json:"name"`
	Username string `json:"username,omitempty"`
}

// ChatPage is one page of a conversation, newest first.
type ChatPage struct {
	Messages []ChatMessage        `json:"messages"`
	Replies  map[string]ChatReply `json:"replies"`
	Peers    map[string]ChatPeer  `json:"peers"`
	HasMore  bool                 `json:"has_more"`
}

// ProfilePhoto is one entry of a peer's profile photo history.
type ProfilePhoto struct {
	PhotoID int64  `json:"photo_id"`
	Date    int    `json:"date"`
	Kind    string `json:"kind"`
	Active  bool   `json:"active"`
	Thumb   string `json:"thumb,omitempty"`
	Full    string `json:"full,omitempty"`
	Video   string `json:"video,omitempty"`
}

// ProfileStory is one story, live or archived.
type ProfileStory struct {
	ID         int        `json:"id"`
	Date       int        `json:"date"`
	ExpireDate int        `json:"expire_date"`
	Deleted    bool       `json:"deleted,omitempty"`
	Pinned     bool       `json:"pinned,omitempty"`
	Public     bool       `json:"public,omitempty"`
	Caption    string     `json:"caption,omitempty"`
	Media      *ChatMedia `json:"media,omitempty"`
}

// ProfileGift is one received star gift.
type ProfileGift struct {
	ID         int64  `json:"id"`
	GiftID     int64  `json:"gift_id"`
	FromUserID int64  `json:"from_user_id"`
	FromName   string `json:"from_name,omitempty"`
	Date       int    `json:"date"`
	Title      string `json:"title"`
	Stars      int64  `json:"stars"`
	Message    string `json:"message,omitempty"`
	Hidden     bool   `json:"hidden,omitempty"` // not shown on the profile
	Converted  bool   `json:"converted,omitempty"`
	NameHidden bool   `json:"name_hidden,omitempty"`
	Unique     bool   `json:"unique,omitempty"`
	UniqueName string `json:"unique_name,omitempty"`
}

// ProfileContent is everything a profile shows besides chats.
type ProfileContent struct {
	Photos  []ProfilePhoto `json:"photos"`
	Stories []ProfileStory `json:"stories"`
	Gifts   []ProfileGift  `json:"gifts"`
}

// MediaItem is one photo/video from an account's chats, for the media grid.
type MediaItem struct {
	PeerID int64      `json:"peer_id"`
	ID     int        `json:"id"`
	Date   int64      `json:"date"`
	Out    bool       `json:"out,omitempty"`
	Media  *ChatMedia `json:"media"`
}

func peerKey(kind string, id int64) string { return kind + ":" + strconv.FormatInt(id, 10) }

func (s *readStore) UserChats(ctx context.Context, userID int64) ([]ChatListItem, error) {
	out := make([]ChatListItem, 0)
	rows, err := s.pool.Query(ctx, `
SELECT d.peer_id, d.top_message_date, d.unread_count, d.pinned,
	COALESCE(u.first_name, ''), COALESCE(u.last_name, ''),
	COALESCE(NULLIF(u.username, ''), p.username_lower, ''), COALESCE(u.is_bot, false),
	COALESCE(m.body, ''), COALESCE(m.media->>'kind', ''), COALESCE(m.outgoing, false)
FROM dialogs d
LEFT JOIN users u ON u.id = d.peer_id
LEFT JOIN peer_usernames p ON p.peer_type = 'user' AND p.peer_id = d.peer_id AND p.editable
LEFT JOIN message_boxes m ON m.owner_user_id = d.user_id AND m.box_id = d.top_message_id
WHERE d.user_id = $1
ORDER BY d.top_message_date DESC
LIMIT $2`, userID, chatListLimit)
	if err != nil {
		return nil, fmt.Errorf("list user dialogs: %w", err)
	}
	for rows.Next() {
		var item ChatListItem
		var first, last string
		if err := rows.Scan(&item.PeerID, &item.LastDate, &item.Unread, &item.Pinned, &first, &last, &item.Username, &item.Bot,
			&item.LastText, &item.LastMedia, &item.LastOutgoing); err != nil {
			rows.Close()
			return nil, err
		}
		item.Kind = "user"
		item.Self = item.PeerID == userID
		item.Title = joinName(first, last)
		out = append(out, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.pool.Query(ctx, `
SELECT c.id, c.title, COALESCE(NULLIF(c.username, ''), p.username_lower, ''), c.broadcast, c.megagroup,
	cm.role, cm.status,
	COALESCE(lm.message_date, cm.joined_at, 0)::bigint, COALESCE(lm.body, ''), COALESCE(lm.media->>'kind', '')
FROM channel_members cm
JOIN channels c ON c.id = cm.channel_id
LEFT JOIN peer_usernames p ON p.peer_type = 'channel' AND p.peer_id = c.id AND p.editable
LEFT JOIN LATERAL (
	SELECT message_date, body, media FROM channel_messages
	WHERE channel_id = c.id AND NOT deleted
	ORDER BY id DESC LIMIT 1
) lm ON true
WHERE cm.user_id = $1 AND NOT c.deleted AND NOT c.monoforum
ORDER BY 8 DESC
LIMIT $2`, userID, chatListLimit)
	if err != nil {
		return nil, fmt.Errorf("list user channels: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item ChatListItem
		var broadcast, megagroup bool
		if err := rows.Scan(&item.PeerID, &item.Title, &item.Username, &broadcast, &megagroup, &item.Role, &item.Status,
			&item.LastDate, &item.LastText, &item.LastMedia); err != nil {
			return nil, err
		}
		item.Kind = "group"
		if broadcast && !megagroup {
			item.Kind = "channel"
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastDate > out[j].LastDate })
	return out, nil
}

// IsChannelMember reports whether userID has a membership row (any status)
// in channelID -- the condition for reading that chat from the user's side.
func (s *readStore) IsChannelMember(ctx context.Context, userID, channelID int64) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM channel_members WHERE channel_id = $1 AND user_id = $2)`, channelID, userID).Scan(&ok)
	return ok, err
}

// PrivateChatPage reads ownerUserID's copy of the conversation with peerID.
func (s *readStore) PrivateChatPage(ctx context.Context, ownerUserID, peerID, beforeDate int64, beforeID int) (ChatPage, error) {
	page := newChatPage()
	rows, err := s.pool.Query(ctx, `
SELECT box_id, from_user_id, message_date, edit_date, outgoing, body, deleted, pinned, reply_to_msg_id,
	fwd_from_peer_type, fwd_from_peer_id, fwd_from_name, COALESCE(media, '{}'::jsonb), via_bot_id
FROM message_boxes
WHERE owner_user_id = $1 AND peer_type = 'user' AND peer_id = $2
	AND ($3::bigint = 0 OR (message_date, box_id) < ($3::bigint, $4::int))
ORDER BY message_date DESC, box_id DESC
LIMIT $5`, ownerUserID, peerID, beforeDate, beforeID, chatPageLimit+1)
	if err != nil {
		return page, fmt.Errorf("read private chat: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var m ChatMessage
		var media []byte
		if err := rows.Scan(&m.ID, &m.FromID, &m.Date, &m.EditDate, &m.Out, &m.Text, &m.Deleted, &m.Pinned, &m.ReplyToID,
			&m.FwdFromType, &m.FwdFromID, &m.FwdFromName, &media, &m.ViaBotID); err != nil {
			return page, err
		}
		m.FromType = "user"
		m.Media, m.Service = chatMediaFromJSON(media)
		page.Messages = append(page.Messages, m)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	rows.Close()
	page.trim()
	if err := s.fillPrivateReplies(ctx, ownerUserID, &page); err != nil {
		return page, err
	}
	return page, s.fillPeers(ctx, &page)
}

// ChannelChatPage reads a channel/group's history. thread > 0 narrows it to
// one discussion thread (the comments under a post, in its linked group).
func (s *readStore) ChannelChatPage(ctx context.Context, channelID int64, thread int, beforeDate int64, beforeID int) (ChatPage, error) {
	page := newChatPage()
	rows, err := s.pool.Query(ctx, `
SELECT cm.id, cm.from_peer_type, cm.from_peer_id, cm.message_date, cm.edit_date, cm.post, cm.body, cm.deleted, cm.pinned,
	cm.reply_to_msg_id, cm.fwd_from, COALESCE(cm.media, '{}'::jsonb), cm.action, cm.views_count, cm.post_author, cm.via_bot_id,
	cm.discussion_channel_id, cm.discussion_message_id,
	CASE WHEN cm.discussion_channel_id > 0 THEN (
		SELECT count(*) FROM channel_messages d
		WHERE d.channel_id = cm.discussion_channel_id AND NOT d.deleted
			AND (d.reply_to_top_id = cm.discussion_message_id OR (d.reply_to_top_id = 0 AND d.reply_to_msg_id = cm.discussion_message_id))
	) ELSE 0 END
FROM channel_messages cm
WHERE cm.channel_id = $1
	AND ($2::int = 0 OR cm.id = $2 OR cm.reply_to_top_id = $2 OR (cm.reply_to_top_id = 0 AND cm.reply_to_msg_id = $2))
	AND ($3::bigint = 0 OR (cm.message_date, cm.id) < ($3::bigint, $4::int))
ORDER BY cm.message_date DESC, cm.id DESC
LIMIT $5`, channelID, thread, beforeDate, beforeID, chatPageLimit+1)
	if err != nil {
		return page, fmt.Errorf("read channel chat: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var m ChatMessage
		var fwd, media, action []byte
		var discussionChannel int64
		var discussionMessage int
		var comments int64
		if err := rows.Scan(&m.ID, &m.FromType, &m.FromID, &m.Date, &m.EditDate, &m.Post, &m.Text, &m.Deleted, &m.Pinned,
			&m.ReplyToID, &fwd, &media, &action, &m.Views, &m.PostAuthor, &m.ViaBotID,
			&discussionChannel, &discussionMessage, &comments); err != nil {
			return page, err
		}
		m.Media, m.Service = chatMediaFromJSON(media)
		if svc := channelActionKind(action); svc != "" {
			m.Service = svc
		}
		applyChannelForward(&m, fwd)
		m.Comments = int(comments)
		m.HasThread = discussionChannel > 0 && discussionMessage > 0
		page.Messages = append(page.Messages, m)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	rows.Close()
	page.trim()
	if err := s.fillChannelReplies(ctx, channelID, &page); err != nil {
		return page, err
	}
	return page, s.fillPeers(ctx, &page)
}

// PostDiscussion resolves a channel post to its comment thread: the linked
// group and the thread's top message id there.
func (s *readStore) PostDiscussion(ctx context.Context, channelID int64, postID int) (int64, int, error) {
	var groupID int64
	var top int
	err := s.pool.QueryRow(ctx, `
SELECT discussion_channel_id, discussion_message_id FROM channel_messages
WHERE channel_id = $1 AND id = $2`, channelID, postID).Scan(&groupID, &top)
	return groupID, top, err
}

// UserMediaPage lists photos and videos across an account's private chats.
func (s *readStore) UserMediaPage(ctx context.Context, userID, beforeDate int64, beforeID int) ([]MediaItem, bool, error) {
	rows, err := s.pool.Query(ctx, `
SELECT peer_id, box_id, message_date, outgoing, media
FROM message_boxes
WHERE owner_user_id = $1 AND NOT deleted
	AND (media->>'kind' = 'photo' OR (media->>'kind' = 'document' AND COALESCE((media->>'video')::boolean, false)))
	AND ($2::bigint = 0 OR (message_date, box_id) < ($2::bigint, $3::int))
ORDER BY message_date DESC, box_id DESC
LIMIT $4`, userID, beforeDate, beforeID, profileMediaLimit/2+1)
	if err != nil {
		return nil, false, fmt.Errorf("list user media: %w", err)
	}
	defer rows.Close()
	out := make([]MediaItem, 0)
	for rows.Next() {
		var item MediaItem
		var media []byte
		if err := rows.Scan(&item.PeerID, &item.ID, &item.Date, &item.Out, &media); err != nil {
			return nil, false, err
		}
		item.Media, _ = chatMediaFromJSON(media)
		if item.Media != nil {
			out = append(out, item)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(out) > profileMediaLimit/2
	if hasMore {
		out = out[:profileMediaLimit/2]
	}
	return out, hasMore, nil
}

// PeerProfileContent loads a user's or channel's profile photos, stories and
// gifts.
func (s *readStore) PeerProfileContent(ctx context.Context, peerType string, peerID int64) (ProfileContent, error) {
	out := ProfileContent{Photos: []ProfilePhoto{}, Stories: []ProfileStory{}, Gifts: []ProfileGift{}}

	rows, err := s.pool.Query(ctx, `
SELECT pp.photo_id, pp.date, pp.kind, pp.active, ph.sizes
FROM profile_photos pp
JOIN photos ph ON ph.id = pp.photo_id
WHERE pp.owner_peer_type = $1 AND pp.owner_peer_id = $2
ORDER BY pp.date DESC, pp.photo_id DESC
LIMIT $3`, peerType, peerID, profileMediaLimit)
	if err != nil {
		return out, fmt.Errorf("list profile photos: %w", err)
	}
	for rows.Next() {
		var p ProfilePhoto
		var sizes []byte
		if err := rows.Scan(&p.PhotoID, &p.Date, &p.Kind, &p.Active, &sizes); err != nil {
			rows.Close()
			return out, err
		}
		var parsed []domain.PhotoSize
		_ = json.Unmarshal(sizes, &parsed)
		p.Thumb, p.Full, _, _ = photoSizeKeys(p.PhotoID, parsed)
		for _, size := range parsed {
			if size.Kind == domain.PhotoSizeKindVideo && validSizeType(size.Type) {
				p.Video = fmt.Sprintf("photo:%d:%s", p.PhotoID, size.Type)
			}
		}
		out.Photos = append(out.Photos, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}

	rows, err = s.pool.Query(ctx, `
SELECT story_id, date, expire_date, deleted, pinned, public, caption, media
FROM stories
WHERE owner_peer_type = $1 AND owner_peer_id = $2
ORDER BY date DESC, story_id DESC
LIMIT $3`, peerType, peerID, profileMediaLimit)
	if err != nil {
		return out, fmt.Errorf("list stories: %w", err)
	}
	for rows.Next() {
		var st ProfileStory
		var media []byte
		if err := rows.Scan(&st.ID, &st.Date, &st.ExpireDate, &st.Deleted, &st.Pinned, &st.Public, &st.Caption, &media); err != nil {
			rows.Close()
			return out, err
		}
		st.Media, _ = chatMediaFromJSON(media)
		out.Stories = append(out.Stories, st)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}

	rows, err = s.pool.Query(ctx, `
SELECT g.id, g.gift_id, g.from_user_id, g.gift_date, COALESCE(r.title, ''), COALESCE(r.stars, 0), g.message,
	g.unsaved, g.converted, g.name_hidden, g.unique_gift_id IS NOT NULL,
	COALESCE(ug.title, ''), COALESCE(ug.num, 0),
	COALESCE(u.first_name, ''), COALESCE(u.last_name, '')
FROM peer_star_gifts g
LEFT JOIN star_gift_catalog c ON c.gift_id = g.gift_id
LEFT JOIN star_gift_catalog_revisions r ON r.id = c.active_revision_id
LEFT JOIN unique_star_gifts ug ON ug.id = g.unique_gift_id
LEFT JOIN users u ON u.id = g.from_user_id
WHERE g.owner_peer_type = $1 AND g.owner_peer_id = $2
ORDER BY g.gift_date DESC, g.id DESC
LIMIT $3`, peerType, peerID, profileMediaLimit)
	if err != nil {
		return out, fmt.Errorf("list gifts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var g ProfileGift
		var uniqueTitle string
		var uniqueNum int
		var first, last string
		if err := rows.Scan(&g.ID, &g.GiftID, &g.FromUserID, &g.Date, &g.Title, &g.Stars, &g.Message,
			&g.Hidden, &g.Converted, &g.NameHidden, &g.Unique, &uniqueTitle, &uniqueNum, &first, &last); err != nil {
			return out, err
		}
		g.FromName = joinName(first, last)
		if g.Unique {
			title := uniqueTitle
			if title == "" {
				title = g.Title
			}
			g.UniqueName = fmt.Sprintf("%s #%d", title, uniqueNum)
		}
		out.Gifts = append(out.Gifts, g)
	}
	return out, rows.Err()
}

func newChatPage() ChatPage {
	return ChatPage{Messages: []ChatMessage{}, Replies: map[string]ChatReply{}, Peers: map[string]ChatPeer{}}
}

func (p *ChatPage) trim() {
	if len(p.Messages) > chatPageLimit {
		p.Messages = p.Messages[:chatPageLimit]
		p.HasMore = true
	}
}

// replyTargets lists reply ids not already on the page.
func (p *ChatPage) replyTargets() []int32 {
	onPage := make(map[int]bool, len(p.Messages))
	for _, m := range p.Messages {
		onPage[m.ID] = true
	}
	var ids []int32
	seen := map[int]bool{}
	for _, m := range p.Messages {
		if m.ReplyToID > 0 && !onPage[m.ReplyToID] && !seen[m.ReplyToID] {
			seen[m.ReplyToID] = true
			ids = append(ids, int32(m.ReplyToID))
		}
	}
	return ids
}

func (s *readStore) fillPrivateReplies(ctx context.Context, ownerUserID int64, page *ChatPage) error {
	ids := page.replyTargets()
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.pool.Query(ctx, `
SELECT box_id, from_user_id, body, COALESCE(media, '{}'::jsonb)
FROM message_boxes WHERE owner_user_id = $1 AND box_id = ANY($2::int[])`, ownerUserID, ids)
	if err != nil {
		return fmt.Errorf("read reply targets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var r ChatReply
		var media []byte
		if err := rows.Scan(&id, &r.FromID, &r.Text, &media); err != nil {
			return err
		}
		r.FromType = "user"
		if m, svc := chatMediaFromJSON(media); m != nil {
			r.Media = m.Kind
		} else if svc != "" {
			r.Media = "service"
		}
		page.Replies[strconv.Itoa(id)] = r
	}
	return rows.Err()
}

func (s *readStore) fillChannelReplies(ctx context.Context, channelID int64, page *ChatPage) error {
	ids := page.replyTargets()
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, from_peer_type, from_peer_id, body, COALESCE(media, '{}'::jsonb)
FROM channel_messages WHERE channel_id = $1 AND id = ANY($2::int[])`, channelID, ids)
	if err != nil {
		return fmt.Errorf("read reply targets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var r ChatReply
		var media []byte
		if err := rows.Scan(&id, &r.FromType, &r.FromID, &r.Text, &media); err != nil {
			return err
		}
		if m, _ := chatMediaFromJSON(media); m != nil {
			r.Media = m.Kind
		}
		page.Replies[strconv.Itoa(id)] = r
	}
	return rows.Err()
}

// fillPeers names every user and channel the page mentions: senders, forward
// sources, reply authors and inline bots.
func (s *readStore) fillPeers(ctx context.Context, page *ChatPage) error {
	users := map[int64]bool{}
	channels := map[int64]bool{}
	add := func(kind string, id int64) {
		if id <= 0 {
			return
		}
		if kind == "channel" {
			channels[id] = true
		} else if kind == "user" {
			users[id] = true
		}
	}
	for _, m := range page.Messages {
		add(m.FromType, m.FromID)
		add(m.FwdFromType, m.FwdFromID)
		add("user", m.ViaBotID)
	}
	for _, r := range page.Replies {
		add(r.FromType, r.FromID)
	}
	if len(users) > 0 {
		rows, err := s.pool.Query(ctx, `
SELECT u.id, u.first_name, u.last_name, COALESCE(NULLIF(u.username, ''), p.username_lower, '')
FROM users u
LEFT JOIN peer_usernames p ON p.peer_type = 'user' AND p.peer_id = u.id AND p.editable
WHERE u.id = ANY($1::bigint[])`, keysOf(users))
		if err != nil {
			return fmt.Errorf("name users: %w", err)
		}
		for rows.Next() {
			var id int64
			var first, last, username string
			if err := rows.Scan(&id, &first, &last, &username); err != nil {
				rows.Close()
				return err
			}
			page.Peers[peerKey("user", id)] = ChatPeer{Name: joinName(first, last), Username: username}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	if len(channels) > 0 {
		rows, err := s.pool.Query(ctx, `
SELECT c.id, c.title, COALESCE(NULLIF(c.username, ''), p.username_lower, '')
FROM channels c
LEFT JOIN peer_usernames p ON p.peer_type = 'channel' AND p.peer_id = c.id AND p.editable
WHERE c.id = ANY($1::bigint[])`, keysOf(channels))
		if err != nil {
			return fmt.Errorf("name channels: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var title, username string
			if err := rows.Scan(&id, &title, &username); err != nil {
				return err
			}
			page.Peers[peerKey("channel", id)] = ChatPeer{Name: title, Username: username}
		}
		return rows.Err()
	}
	return nil
}

func keysOf(set map[int64]bool) []int64 {
	out := make([]int64, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	return out
}

func joinName(first, last string) string {
	switch {
	case first == "":
		return last
	case last == "":
		return first
	default:
		return first + " " + last
	}
}

// applyChannelForward reads a channel message's forward header, stored as the
// Go-default JSON of domain.MessageForward.
func applyChannelForward(m *ChatMessage, raw []byte) {
	if len(raw) == 0 || string(raw) == "{}" {
		return
	}
	var fwd domain.MessageForward
	if err := json.Unmarshal(raw, &fwd); err != nil {
		return
	}
	m.FwdFromType = string(fwd.From.Type)
	m.FwdFromID = fwd.From.ID
	m.FwdFromName = fwd.FromName
}

// channelActionKind returns a channel service message's action type, or "".
func channelActionKind(raw []byte) string {
	if len(raw) == 0 || string(raw) == "{}" {
		return ""
	}
	var action struct{ Type string }
	if err := json.Unmarshal(raw, &action); err != nil || action.Type == "" {
		return "service"
	}
	return action.Type
}

// chatMediaFromJSON reduces a stored domain.MessageMedia to what the viewer
// draws. A service action comes back as its kind instead.
func chatMediaFromJSON(raw []byte) (*ChatMedia, string) {
	if len(raw) == 0 || string(raw) == "{}" {
		return nil, ""
	}
	var media domain.MessageMedia
	if err := json.Unmarshal(raw, &media); err != nil {
		return &ChatMedia{Kind: "other"}, ""
	}
	switch media.Kind {
	case domain.MessageMediaKindNone:
		return nil, ""
	case domain.MessageMediaKindService:
		if media.ServiceAction != nil && media.ServiceAction.Kind != "" {
			return nil, string(media.ServiceAction.Kind)
		}
		return nil, "service"
	case domain.MessageMediaKindPhoto:
		if media.Photo == nil {
			return &ChatMedia{Kind: "photo"}, ""
		}
		out := &ChatMedia{Kind: "photo", Spoiler: media.Spoiler}
		out.Thumb, out.Full, out.W, out.H = photoSizeKeys(media.Photo.ID, media.Photo.Sizes)
		return out, ""
	case domain.MessageMediaKindDocument:
		if media.Document == nil {
			return &ChatMedia{Kind: "file"}, ""
		}
		return documentMedia(media), ""
	case domain.MessageMediaKindContact:
		out := &ChatMedia{Kind: "contact"}
		if media.Contact != nil {
			out.Title = joinName(media.Contact.FirstName, media.Contact.LastName)
			out.Subtitle = media.Contact.PhoneNumber
		}
		return out, ""
	case domain.MessageMediaKindGeo, domain.MessageMediaKindGeoLive, domain.MessageMediaKindVenue:
		return &ChatMedia{Kind: "geo", Title: string(media.Kind)}, ""
	case domain.MessageMediaKindPoll:
		return &ChatMedia{Kind: "poll", Title: pollQuestion(raw)}, ""
	case domain.MessageMediaKindWebPage:
		out := &ChatMedia{Kind: "webpage"}
		var wp struct {
			WebPage struct {
				URL         string `json:"url"`
				SiteName    string `json:"site_name"`
				Title       string `json:"title"`
				Description string `json:"description"`
			} `json:"web_page"`
		}
		if json.Unmarshal(raw, &wp) == nil {
			out.Title = firstNonEmpty(wp.WebPage.Title, wp.WebPage.SiteName, wp.WebPage.URL)
			out.Subtitle = wp.WebPage.Description
		}
		return out, ""
	case domain.MessageMediaKindDice:
		return &ChatMedia{Kind: "dice"}, ""
	case domain.MessageMediaKindStory:
		return &ChatMedia{Kind: "story"}, ""
	default:
		return &ChatMedia{Kind: "other", Title: string(media.Kind)}, ""
	}
}

func pollQuestion(raw []byte) string {
	var p struct {
		Poll struct {
			Question any `json:"question"`
		} `json:"poll"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return ""
	}
	switch q := p.Poll.Question.(type) {
	case string:
		return q
	case map[string]any:
		if text, ok := q["text"].(string); ok {
			return text
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func documentMedia(media domain.MessageMedia) *ChatMedia {
	doc := media.Document
	out := &ChatMedia{
		Kind:     "file",
		Full:     fmt.Sprintf("doc:%d", doc.ID),
		MimeType: doc.MimeType,
		Size:     doc.Size,
		Spoiler:  media.Spoiler,
	}
	var sticker, animated, video, round, audio, voice bool
	for _, attr := range doc.Attributes {
		switch attr.Kind {
		case domain.DocAttrSticker, domain.DocAttrCustomEmoji:
			sticker = true
			out.Title = attr.Alt
			if attr.W > 0 {
				out.W, out.H = attr.W, attr.H
			}
		case domain.DocAttrAnimated:
			animated = true
		case domain.DocAttrVideo:
			video = true
			round = round || attr.RoundMessage
			out.Duration = attr.Duration
			if attr.W > 0 {
				out.W, out.H = attr.W, attr.H
			}
		case domain.DocAttrAudio:
			audio = true
			voice = voice || attr.Voice
			out.Duration = float64(attr.AudioDuration)
			out.Title = attr.Title
			out.Subtitle = attr.Performer
		case domain.DocAttrFilename:
			out.FileName = attr.FileName
		case domain.DocAttrImageSize:
			if out.W == 0 {
				out.W, out.H = attr.W, attr.H
			}
		}
	}
	switch {
	case sticker:
		out.Kind = "sticker"
		out.Animated = doc.MimeType == "application/x-tgsticker"
	case animated:
		out.Kind = "gif"
	case round || media.Round:
		out.Kind = "round"
	case video || media.Video:
		out.Kind = "video"
	case voice || media.Voice:
		out.Kind = "voice"
	case audio:
		out.Kind = "audio"
	}
	// The largest downloadable thumbnail stands in for the bubble.
	best := -1
	for _, thumb := range doc.Thumbs {
		if !thumb.Downloadable() || thumb.Kind == domain.PhotoSizeKindVideo || !validSizeType(thumb.Type) {
			continue
		}
		if area := thumb.W * thumb.H; area > best {
			best = area
			out.Thumb = fmt.Sprintf("doc:%d:%s", doc.ID, thumb.Type)
		}
	}
	return out
}

// photoSizeKeys picks a bubble-sized and a full-sized stored file for a photo:
// the smallest size at least 320px on its long side, and the largest one.
func photoSizeKeys(photoID int64, sizes []domain.PhotoSize) (thumb, full string, w, h int) {
	type cand struct {
		size domain.PhotoSize
		long int
	}
	var cands []cand
	for _, size := range sizes {
		if (size.Kind != domain.PhotoSizeKindDefault && size.Kind != domain.PhotoSizeKindProgressive) || !validSizeType(size.Type) {
			continue
		}
		long := size.W
		if size.H > long {
			long = size.H
		}
		cands = append(cands, cand{size, long})
	}
	if len(cands) == 0 {
		return "", "", 0, 0
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].long < cands[j].long })
	largest := cands[len(cands)-1].size
	thumbSize := largest
	for _, c := range cands {
		if c.long >= 320 {
			thumbSize = c.size
			break
		}
	}
	key := func(t string) string { return fmt.Sprintf("photo:%d:%s", photoID, t) }
	return key(thumbSize.Type), key(largest.Type), largest.W, largest.H
}

func validSizeType(value string) bool {
	if value == "" || len(value) > 8 {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
			return false
		}
	}
	return true
}
