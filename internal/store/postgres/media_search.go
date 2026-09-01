package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"telesrv/internal/domain"
)

// 共享媒体标签页读路径(迁移 0118):按类别查媒体索引拿到消息 id(JOIN 基表过滤软删 deleted),
// 再复用既有「按 id 取消息」路径(GetByIDs / GetChannelMessages)做富化与打包,并按索引顺序(id 倒序)重排。

const mediaSearchPageLimit = 100

func mediaCategoriesToInt16(cats []domain.MediaCategory) []int16 {
	out := make([]int16, 0, len(cats))
	for _, c := range cats {
		if c != domain.MediaCategoryNone {
			out = append(out, int16(c))
		}
	}
	return out
}

func mediaSearchPaging(req domain.MediaSearchRequest) (limit, offset int) {
	limit = req.Limit
	if limit < 0 || limit > mediaSearchPageLimit {
		limit = mediaSearchPageLimit
	}
	offset = req.AddOffset
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

type mediaSearchQueryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func mediaSearchCount(ctx context.Context, db mediaSearchQueryer, fallbackSQL string, args ...any) (int, error) {
	var count int
	if err := db.QueryRow(ctx, fallbackSQL, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func reorderMessagesByID(msgs []domain.Message, order []int) []domain.Message {
	byID := make(map[int]domain.Message, len(msgs))
	for _, m := range msgs {
		byID[m.ID] = m
	}
	out := make([]domain.Message, 0, len(order))
	for _, id := range order {
		if m, ok := byID[id]; ok {
			out = append(out, m)
		}
	}
	return out
}

func reorderChannelMessagesByID(msgs []domain.ChannelMessage, order []int) []domain.ChannelMessage {
	byID := make(map[int]domain.ChannelMessage, len(msgs))
	for _, m := range msgs {
		byID[m.ID] = m
	}
	out := make([]domain.ChannelMessage, 0, len(order))
	for _, id := range order {
		if m, ok := byID[id]; ok {
			out = append(out, m)
		}
	}
	return out
}

func privateMediaSearchBase(ownerUserID, peerID int64, cats []int16, req domain.MediaSearchRequest) (string, []any) {
	args := []any{ownerUserID, peerID, cats}
	where := `
FROM message_box_media mi
JOIN message_boxes mb ON mb.owner_user_id = mi.owner_user_id AND mb.box_id = mi.box_id
WHERE mi.owner_user_id = $1 AND mi.peer_id = $2 AND mi.category = ANY($3::smallint[])
  AND NOT mb.deleted`
	add := func(clause string, value any) {
		args = append(args, value)
		where += fmt.Sprintf(clause, len(args))
	}
	if req.MaxID > 0 {
		add(" AND mi.box_id <= $%d", pgInt32NonNegative(req.MaxID))
	}
	if req.MinID > 0 {
		add(" AND mi.box_id >= $%d", pgInt32NonNegative(req.MinID))
	}
	if req.Query != "" {
		add(" AND mb.body ILIKE '%%' || $%d || '%%'", req.Query)
	}
	if req.SenderUserID != 0 {
		add(" AND mb.from_user_id = $%d", req.SenderUserID)
	}
	if req.MinDate > 0 {
		add(" AND mb.message_date > $%d", pgInt32NonNegative(req.MinDate))
	}
	if req.MaxDate > 0 {
		add(" AND mb.message_date < $%d", pgInt32NonNegative(req.MaxDate))
	}
	if req.TopMsgID > 0 {
		args = append(args, pgInt32NonNegative(req.TopMsgID))
		where += fmt.Sprintf(" AND (mb.box_id = $%d OR mb.reply_to_top_id = $%d)", len(args), len(args))
	}
	if req.SavedPeer.ID != 0 {
		args = append(args, string(req.SavedPeer.Type), req.SavedPeer.ID)
		where += fmt.Sprintf(" AND mb.saved_peer_type = $%d AND mb.saved_peer_id = $%d", len(args)-1, len(args))
	}
	if keys := postgresSavedReactionKeys(req.SavedReactions); len(keys) > 0 {
		args = append(args, keys)
		where += fmt.Sprintf(` AND EXISTS (
    SELECT 1 FROM saved_message_reaction_tags tag
    WHERE tag.user_id = mb.owner_user_id AND tag.message_box_id = mb.box_id
      AND (tag.reaction_type || ':' || tag.reaction_value) = ANY($%d::text[])
  )`, len(args))
	}
	return where, args
}

func channelMediaSearchBase(
	viewerUserID, channelID int64,
	cats []int16,
	channel domain.Channel,
	member domain.ChannelMember,
	req domain.MediaSearchRequest,
) (string, []any) {
	args := []any{channelID, cats}
	where := `
FROM channel_message_media mi
JOIN channel_messages m ON m.channel_id = mi.channel_id AND m.id = mi.id
WHERE mi.channel_id = $1 AND mi.category = ANY($2::smallint[])
  AND NOT m.deleted`
	add := func(clause string, value any) {
		args = append(args, value)
		where += fmt.Sprintf(clause, len(args))
	}
	if member.AvailableMinID > 0 {
		add(" AND mi.id > $%d", pgInt32NonNegative(member.AvailableMinID))
	}
	if channel.Monoforum {
		if member.CanManageDirectMessages() {
			where += " AND m.saved_peer_id = 0"
		} else {
			add(" AND m.saved_peer_type = 'user' AND m.saved_peer_id = $%d", viewerUserID)
		}
	}
	if req.MaxID > 0 {
		add(" AND mi.id <= $%d", pgInt32NonNegative(req.MaxID))
	}
	if req.MinID > 0 {
		add(" AND mi.id >= $%d", pgInt32NonNegative(req.MinID))
	}
	if req.Query != "" {
		add(" AND m.body ILIKE '%%' || $%d || '%%'", req.Query)
	}
	if req.SenderUserID != 0 {
		add(" AND m.sender_user_id = $%d", req.SenderUserID)
	}
	if req.MinDate > 0 {
		add(" AND m.message_date > $%d", pgInt32NonNegative(req.MinDate))
	}
	if req.MaxDate > 0 {
		add(" AND m.message_date < $%d", pgInt32NonNegative(req.MaxDate))
	}
	if req.TopMsgID > 0 {
		args = append(args, pgInt32NonNegative(req.TopMsgID))
		where += fmt.Sprintf(" AND (m.id = $%d OR m.reply_to_top_id = $%d)", len(args), len(args))
	}
	return where, args
}

// SearchPrivateMedia 返回某私聊会话中属于给定类别的消息(newest-first 分页)。
func (s *MessageStore) SearchPrivateMedia(ctx context.Context, ownerUserID, peerID int64, req domain.MediaSearchRequest) (domain.MessageList, error) {
	cats := mediaCategoriesToInt16(req.Categories)
	if ownerUserID == 0 || peerID == 0 || len(cats) == 0 {
		return domain.MessageList{}, nil
	}
	limit, offset := mediaSearchPaging(req)
	base, baseArgs := privateMediaSearchBase(ownerUserID, peerID, cats, req)

	count := req.KnownCount
	if !req.HasKnownCount {
		var err error
		count, err = mediaSearchCount(ctx, s.db, "SELECT count(DISTINCT mi.box_id)::int"+base, baseArgs...)
		if err != nil {
			return domain.MessageList{}, fmt.Errorf("count private media: %w", err)
		}
	}
	if limit == 0 {
		return domain.MessageList{Count: count}, nil
	}
	args := append([]any(nil), baseArgs...)
	if req.OffsetID > 0 {
		args = append(args, pgInt32NonNegative(req.OffsetID))
		base += fmt.Sprintf(" AND mi.box_id < $%d", len(args))
	}
	args = append(args, offset, limit)
	rows, err := s.db.Query(ctx, "SELECT DISTINCT mi.box_id"+base+
		fmt.Sprintf(" ORDER BY mi.box_id DESC OFFSET $%d LIMIT $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return domain.MessageList{}, fmt.Errorf("list private media ids: %w", err)
	}
	defer rows.Close()
	ids := make([]int, 0, limit)
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			return domain.MessageList{}, fmt.Errorf("scan private media id: %w", err)
		}
		ids = append(ids, int(id))
	}
	if err := rows.Err(); err != nil {
		return domain.MessageList{}, fmt.Errorf("iterate private media ids: %w", err)
	}

	list, err := s.GetByIDs(ctx, ownerUserID, ids)
	if err != nil {
		return domain.MessageList{}, err
	}
	list.Messages = reorderMessagesByID(list.Messages, ids)
	list.Count = count
	return list, nil
}

// CountPrivateMediaCategories 返回某私聊会话按基础媒体类别聚合的精确计数。
func (s *MessageStore) CountPrivateMediaCategories(ctx context.Context, ownerUserID, peerID int64) (domain.MediaCategoryCounts, error) {
	if ownerUserID == 0 || peerID == 0 {
		return domain.MediaCategoryCounts{}, nil
	}
	rows, err := s.db.Query(ctx, `
SELECT category, media_count
FROM private_media_category_counts
WHERE owner_user_id = $1 AND peer_id = $2 AND media_count > 0`, ownerUserID, peerID)
	if err != nil {
		return nil, fmt.Errorf("count private media categories: %w", err)
	}
	defer rows.Close()
	out := domain.MediaCategoryCounts{}
	for rows.Next() {
		var category int16
		var count int
		if err := rows.Scan(&category, &count); err != nil {
			return nil, fmt.Errorf("scan private media category count: %w", err)
		}
		out[domain.MediaCategory(category)] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate private media category counts: %w", err)
	}
	return out, nil
}

// SearchChannelMedia 返回某频道中属于给定类别的消息(newest-first 分页)。
func (s *ChannelStore) SearchChannelMedia(ctx context.Context, viewerUserID, channelID int64, req domain.MediaSearchRequest) (domain.ChannelHistory, error) {
	cats := mediaCategoriesToInt16(req.Categories)
	if viewerUserID == 0 || channelID == 0 || len(cats) == 0 {
		return domain.ChannelHistory{}, nil
	}
	limit, offset := mediaSearchPaging(req)

	channel, member, err := s.getChannelForMember(ctx, s.db, viewerUserID, channelID)
	if err != nil {
		return domain.ChannelHistory{}, err
	}
	base, baseArgs := channelMediaSearchBase(viewerUserID, channelID, cats, channel, member, req)
	count := req.KnownCount
	if !req.HasKnownCount {
		var err error
		count, err = mediaSearchCount(ctx, s.db, "SELECT count(DISTINCT mi.id)::int"+base, baseArgs...)
		if err != nil {
			return domain.ChannelHistory{}, fmt.Errorf("count channel media: %w", err)
		}
	}
	if limit == 0 {
		return domain.ChannelHistory{Channel: channel, Self: member, Count: count}, nil
	}
	args := append([]any(nil), baseArgs...)
	if req.OffsetID > 0 {
		args = append(args, pgInt32NonNegative(req.OffsetID))
		base += fmt.Sprintf(" AND mi.id < $%d", len(args))
	}
	args = append(args, offset, limit)
	rows, err := s.db.Query(ctx, "SELECT DISTINCT mi.id"+base+
		fmt.Sprintf(" ORDER BY mi.id DESC OFFSET $%d LIMIT $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return domain.ChannelHistory{}, fmt.Errorf("list channel media ids: %w", err)
	}
	defer rows.Close()
	ids := make([]int, 0, limit)
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			return domain.ChannelHistory{}, fmt.Errorf("scan channel media id: %w", err)
		}
		ids = append(ids, int(id))
	}
	if err := rows.Err(); err != nil {
		return domain.ChannelHistory{}, fmt.Errorf("iterate channel media ids: %w", err)
	}

	hist, err := s.getChannelMessagesForMember(ctx, viewerUserID, channel, member, ids)
	if err != nil {
		return domain.ChannelHistory{}, err
	}
	hist.Messages = reorderChannelMessagesByID(hist.Messages, ids)
	hist.Count = count
	return hist, nil
}

// CountChannelMediaCategories 返回某频道对当前 viewer 可见消息按基础媒体类别聚合的精确计数。
func (s *ChannelStore) CountChannelMediaCategories(ctx context.Context, viewerUserID, channelID int64) (domain.MediaCategoryCounts, error) {
	if viewerUserID == 0 || channelID == 0 {
		return domain.MediaCategoryCounts{}, nil
	}
	var availableMinID int32
	if err := s.db.QueryRow(ctx, `
SELECT available_min_id
FROM channel_members
WHERE channel_id = $1 AND user_id = $2
  AND status = 'active'
  AND NOT COALESCE((banned_rights->>'ViewMessages')::boolean, false)`, channelID, viewerUserID).Scan(&availableMinID); err != nil {
		if err == pgx.ErrNoRows {
			return domain.MediaCategoryCounts{}, nil
		}
		return nil, fmt.Errorf("load channel media count member visibility: %w", err)
	}
	if availableMinID <= 0 {
		return s.countFullChannelMediaCategories(ctx, channelID)
	}
	rows, err := s.db.Query(ctx, `
SELECT mi.category, count(*)::int
FROM channel_message_media mi
JOIN channel_messages m ON m.channel_id = mi.channel_id AND m.id = mi.id
WHERE mi.channel_id = $1
  AND NOT m.deleted
  AND mi.id > $2
GROUP BY mi.category`, channelID, availableMinID)
	if err != nil {
		return nil, fmt.Errorf("count channel media categories: %w", err)
	}
	defer rows.Close()
	out := domain.MediaCategoryCounts{}
	for rows.Next() {
		var category int16
		var count int
		if err := rows.Scan(&category, &count); err != nil {
			return nil, fmt.Errorf("scan channel media category count: %w", err)
		}
		out[domain.MediaCategory(category)] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate channel media category counts: %w", err)
	}
	return out, nil
}

func (s *ChannelStore) countFullChannelMediaCategories(ctx context.Context, channelID int64) (domain.MediaCategoryCounts, error) {
	rows, err := s.db.Query(ctx, `
SELECT category, media_count
FROM channel_media_category_counts
WHERE channel_id = $1 AND media_count > 0`, channelID)
	if err != nil {
		return nil, fmt.Errorf("count full channel media categories: %w", err)
	}
	defer rows.Close()
	out := domain.MediaCategoryCounts{}
	for rows.Next() {
		var category int16
		var count int
		if err := rows.Scan(&category, &count); err != nil {
			return nil, fmt.Errorf("scan full channel media category count: %w", err)
		}
		out[domain.MediaCategory(category)] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate full channel media category counts: %w", err)
	}
	return out, nil
}
