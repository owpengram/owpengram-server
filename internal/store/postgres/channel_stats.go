package postgres

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"telesrv/internal/domain"
)

func (s *ChannelStore) GetChannelStats(ctx context.Context, req domain.ChannelStatsRequest) (domain.ChannelStats, error) {
	if req.ViewerUserID == 0 || req.ChannelID == 0 || !req.Period.Valid() {
		return domain.ChannelStats{}, domain.ErrChannelInvalid
	}
	channel, member, err := s.getChannelForMember(ctx, s.db, req.ViewerUserID, req.ChannelID)
	if err != nil {
		return domain.ChannelStats{}, err
	}
	if member.Role != domain.ChannelRoleCreator && member.Role != domain.ChannelRoleAdmin {
		return domain.ChannelStats{}, domain.ErrChannelAdminRequired
	}

	stats := domain.ChannelStats{Channel: channel, Period: req.Period}
	days, dayIndex := newPGStatsDays(req.Period)
	previousMin := req.Period.PreviousMinDate()

	memberRows, err := s.db.Query(ctx, `
SELECT joined_at, left_at
FROM channel_members
WHERE channel_id = $1
  AND joined_at > 0
  AND joined_at < $2
  AND (left_at = 0 OR left_at >= $3)`, req.ChannelID, req.Period.MaxDate, previousMin)
	if err != nil {
		return domain.ChannelStats{}, fmt.Errorf("query channel stats members: %w", err)
	}
	for memberRows.Next() {
		var joinedAt, leftAt int
		if err := memberRows.Scan(&joinedAt, &leftAt); err != nil {
			memberRows.Close()
			return domain.ChannelStats{}, err
		}
		if pgStatsMemberActiveAt(joinedAt, leftAt, req.Period.MaxDate-1) {
			stats.Members.Current++
		}
		if pgStatsMemberActiveAt(joinedAt, leftAt, req.Period.MinDate-1) {
			stats.Members.Previous++
		}
		if i, ok := dayIndex[pgStatsDay(joinedAt)]; ok && joinedAt >= req.Period.MinDate && joinedAt < req.Period.MaxDate {
			days[i].NewMembers++
		}
		for i := range days {
			at := days[i].Date + 86400 - 1
			if at >= req.Period.MaxDate {
				at = req.Period.MaxDate - 1
			}
			if pgStatsMemberActiveAt(joinedAt, leftAt, at) {
				days[i].Members++
			}
		}
	}
	if err := memberRows.Err(); err != nil {
		memberRows.Close()
		return domain.ChannelStats{}, err
	}
	memberRows.Close()

	var currentMessages, previousMessages int
	var currentViews, previousViews int64
	var currentPosters, previousPosters int
	if err := s.db.QueryRow(ctx, `
SELECT
  count(*) FILTER (WHERE message_date >= $2)::int,
  count(*) FILTER (WHERE message_date < $2)::int,
  COALESCE(sum(views_count) FILTER (WHERE message_date >= $2), 0)::bigint,
  COALESCE(sum(views_count) FILTER (WHERE message_date < $2), 0)::bigint,
  count(DISTINCT sender_user_id) FILTER (WHERE message_date >= $2 AND sender_user_id <> 0)::int,
  count(DISTINCT sender_user_id) FILTER (WHERE message_date < $2 AND sender_user_id <> 0)::int
FROM channel_messages
WHERE channel_id = $1
  AND NOT deleted
	AND action = '{}'::jsonb
  AND message_date >= $3
  AND message_date < $4`, req.ChannelID, req.Period.MinDate, previousMin, req.Period.MaxDate).Scan(
		&currentMessages, &previousMessages, &currentViews, &previousViews, &currentPosters, &previousPosters,
	); err != nil {
		return domain.ChannelStats{}, fmt.Errorf("aggregate channel stats messages: %w", err)
	}

	var currentViewers, previousViewers int
	if err := s.db.QueryRow(ctx, `
SELECT
  count(DISTINCT viewer_user_id) FILTER (WHERE viewed_at >= $2)::int,
  count(DISTINCT viewer_user_id) FILTER (WHERE viewed_at < $2)::int
FROM channel_message_viewers
WHERE channel_id = $1
  AND viewed_at >= $3
  AND viewed_at < $4`, req.ChannelID, req.Period.MinDate, previousMin, req.Period.MaxDate).Scan(&currentViewers, &previousViewers); err != nil {
		return domain.ChannelStats{}, fmt.Errorf("aggregate channel stats viewers: %w", err)
	}

	var currentReactions, previousReactions int
	if err := s.db.QueryRow(ctx, `
SELECT
  count(*) FILTER (WHERE m.message_date >= $2)::int,
  count(*) FILTER (WHERE m.message_date < $2)::int
FROM channel_message_reactions r
JOIN channel_messages m ON m.channel_id = r.channel_id AND m.id = r.message_id
WHERE m.channel_id = $1
  AND NOT m.deleted
	AND m.action = '{}'::jsonb
  AND m.message_date >= $3
  AND m.message_date < $4`, req.ChannelID, req.Period.MinDate, previousMin, req.Period.MaxDate).Scan(&currentReactions, &previousReactions); err != nil {
		return domain.ChannelStats{}, fmt.Errorf("aggregate channel stats reactions: %w", err)
	}

	var currentShares, previousShares int
	if err := s.db.QueryRow(ctx, `
SELECT
  count(*) FILTER (WHERE src.message_date >= $2)::int,
  count(*) FILTER (WHERE src.message_date < $2)::int
FROM channel_messages f
JOIN channels destination ON destination.id = f.channel_id
JOIN channel_messages src
  ON src.channel_id = $1
 AND src.id::text = f.fwd_from #>> '{ChannelPost}'
 AND NOT src.deleted
	AND src.action = '{}'::jsonb
WHERE NOT f.deleted
  AND NOT destination.deleted
  AND (destination.broadcast OR destination.megagroup)
  AND btrim(COALESCE(destination.username, '')) <> ''
  AND f.fwd_from #>> '{From,Type}' = $5
  AND f.fwd_from #>> '{From,ID}' = $6
  AND src.message_date >= $3
  AND src.message_date < $4`, req.ChannelID, req.Period.MinDate, previousMin, req.Period.MaxDate,
		string(domain.PeerTypeChannel), strconv.FormatInt(req.ChannelID, 10)).Scan(&currentShares, &previousShares); err != nil {
		return domain.ChannelStats{}, fmt.Errorf("aggregate channel stats shares: %w", err)
	}

	stats.Messages = domain.StatsValueAndPrev{Current: float64(currentMessages), Previous: float64(previousMessages)}
	stats.Viewers = domain.StatsValueAndPrev{Current: float64(currentViewers), Previous: float64(previousViewers)}
	stats.Posters = domain.StatsValueAndPrev{Current: float64(currentPosters), Previous: float64(previousPosters)}
	stats.ViewsPerPost = pgStatsAverage(currentViews, currentMessages, previousViews, previousMessages)
	stats.SharesPerPost = pgStatsAverage(int64(currentShares), currentMessages, int64(previousShares), previousMessages)
	stats.ReactionsPerPost = pgStatsAverage(int64(currentReactions), currentMessages, int64(previousReactions), previousMessages)

	messageRows, err := s.db.Query(ctx, `
SELECT (message_date / 86400) * 86400 AS day,
       count(*)::int,
       COALESCE(sum(views_count), 0)::int,
       count(DISTINCT sender_user_id) FILTER (WHERE sender_user_id <> 0)::int
FROM channel_messages
WHERE channel_id = $1 AND NOT deleted AND action = '{}'::jsonb AND message_date >= $2 AND message_date < $3
GROUP BY day
ORDER BY day`, req.ChannelID, req.Period.MinDate, req.Period.MaxDate)
	if err != nil {
		return domain.ChannelStats{}, fmt.Errorf("query channel stats days: %w", err)
	}
	for messageRows.Next() {
		var date, messages, views, posters int
		if err := messageRows.Scan(&date, &messages, &views, &posters); err != nil {
			messageRows.Close()
			return domain.ChannelStats{}, err
		}
		if i, ok := dayIndex[date]; ok {
			days[i].Messages, days[i].Views, days[i].Posters = messages, views, posters
		}
	}
	if err := messageRows.Err(); err != nil {
		messageRows.Close()
		return domain.ChannelStats{}, err
	}
	messageRows.Close()

	viewerRows, err := s.db.Query(ctx, `
SELECT (viewed_at / 86400) * 86400 AS day, count(DISTINCT viewer_user_id)::int
FROM channel_message_viewers
WHERE channel_id = $1 AND viewed_at >= $2 AND viewed_at < $3
GROUP BY day`, req.ChannelID, req.Period.MinDate, req.Period.MaxDate)
	if err != nil {
		return domain.ChannelStats{}, fmt.Errorf("query channel stats viewer days: %w", err)
	}
	for viewerRows.Next() {
		var date, viewers int
		if err := viewerRows.Scan(&date, &viewers); err != nil {
			viewerRows.Close()
			return domain.ChannelStats{}, err
		}
		if i, ok := dayIndex[date]; ok {
			days[i].Viewers = viewers
		}
	}
	if err := viewerRows.Err(); err != nil {
		viewerRows.Close()
		return domain.ChannelStats{}, err
	}
	viewerRows.Close()

	reactionRows, err := s.db.Query(ctx, `
SELECT (m.message_date / 86400) * 86400 AS day, r.reaction_type, r.reaction_value, count(*)::int
FROM channel_message_reactions r
JOIN channel_messages m ON m.channel_id = r.channel_id AND m.id = r.message_id
WHERE m.channel_id = $1 AND NOT m.deleted AND m.action = '{}'::jsonb AND m.message_date >= $2 AND m.message_date < $3
GROUP BY day, r.reaction_type, r.reaction_value
ORDER BY day, r.reaction_type, r.reaction_value`, req.ChannelID, req.Period.MinDate, req.Period.MaxDate)
	if err != nil {
		return domain.ChannelStats{}, fmt.Errorf("query channel stats reaction days: %w", err)
	}
	for reactionRows.Next() {
		var date, count int
		var reactionType, reactionValue string
		if err := reactionRows.Scan(&date, &reactionType, &reactionValue, &count); err != nil {
			reactionRows.Close()
			return domain.ChannelStats{}, err
		}
		reaction, ok := domain.MessageReactionFromValue(domain.MessageReactionType(reactionType), reactionValue)
		if !ok {
			reactionRows.Close()
			return domain.ChannelStats{}, fmt.Errorf("invalid persisted channel stats reaction %q/%q", reactionType, reactionValue)
		}
		if i, ok := dayIndex[date]; ok {
			days[i].Reactions += count
			days[i].ByReaction = append(days[i].ByReaction, domain.StatsReactionCount{Reaction: reaction, Count: count})
		}
	}
	if err := reactionRows.Err(); err != nil {
		reactionRows.Close()
		return domain.ChannelStats{}, err
	}
	reactionRows.Close()

	shareRows, err := s.db.Query(ctx, `
SELECT (src.message_date / 86400) * 86400 AS day, count(*)::int
FROM channel_messages f
JOIN channels destination ON destination.id = f.channel_id
JOIN channel_messages src
  ON src.channel_id = $1
 AND src.id::text = f.fwd_from #>> '{ChannelPost}'
 AND NOT src.deleted
	AND src.action = '{}'::jsonb
WHERE NOT f.deleted
  AND NOT destination.deleted
  AND (destination.broadcast OR destination.megagroup)
  AND btrim(COALESCE(destination.username, '')) <> ''
  AND f.fwd_from #>> '{From,Type}' = $4
  AND f.fwd_from #>> '{From,ID}' = $5
  AND src.message_date >= $2 AND src.message_date < $3
GROUP BY day`, req.ChannelID, req.Period.MinDate, req.Period.MaxDate,
		string(domain.PeerTypeChannel), strconv.FormatInt(req.ChannelID, 10))
	if err != nil {
		return domain.ChannelStats{}, fmt.Errorf("query channel stats share days: %w", err)
	}
	for shareRows.Next() {
		var date, shares int
		if err := shareRows.Scan(&date, &shares); err != nil {
			shareRows.Close()
			return domain.ChannelStats{}, err
		}
		if i, ok := dayIndex[date]; ok {
			days[i].Shares = shares
		}
	}
	if err := shareRows.Err(); err != nil {
		shareRows.Close()
		return domain.ChannelStats{}, err
	}
	shareRows.Close()
	sortPGStatsReactions(days)
	stats.Days = days

	topRows, err := s.db.Query(ctx, `
SELECT sender_user_id, count(*)::int,
       CASE WHEN count(*) = 0 THEN 0 ELSE (sum(char_length(body)) / count(*))::int END
FROM channel_messages
WHERE channel_id = $1 AND NOT deleted AND action = '{}'::jsonb AND sender_user_id <> 0
  AND message_date >= $2 AND message_date < $3
GROUP BY sender_user_id
ORDER BY count(*) DESC, sender_user_id
LIMIT $4`, req.ChannelID, req.Period.MinDate, req.Period.MaxDate, domain.MaxChannelStatsTopPosters)
	if err != nil {
		return domain.ChannelStats{}, fmt.Errorf("query channel stats top posters: %w", err)
	}
	for topRows.Next() {
		var item domain.ChannelStatsTopPoster
		if err := topRows.Scan(&item.UserID, &item.Messages, &item.AvgChars); err != nil {
			topRows.Close()
			return domain.ChannelStats{}, err
		}
		stats.TopPosters = append(stats.TopPosters, item)
	}
	if err := topRows.Err(); err != nil {
		topRows.Close()
		return domain.ChannelStats{}, err
	}
	topRows.Close()

	recentRows, err := s.db.Query(ctx, `
SELECT src.id, src.views_count,
       (SELECT count(*)::int
          FROM channel_messages f
          JOIN channels destination ON destination.id = f.channel_id
         WHERE NOT f.deleted AND NOT destination.deleted
           AND (destination.broadcast OR destination.megagroup)
           AND btrim(COALESCE(destination.username, '')) <> ''
           AND f.fwd_from #>> '{From,Type}' = $3
           AND f.fwd_from #>> '{From,ID}' = $4
           AND f.fwd_from #>> '{ChannelPost}' = src.id::text),
       (SELECT count(*)::int FROM channel_message_reactions r
         WHERE r.channel_id = src.channel_id AND r.message_id = src.id)
FROM channel_messages src
WHERE src.channel_id = $1 AND NOT src.deleted AND src.action = '{}'::jsonb
ORDER BY src.message_date DESC, src.id DESC
LIMIT $2`, req.ChannelID, domain.MaxChannelStatsRecentPosts,
		string(domain.PeerTypeChannel), strconv.FormatInt(req.ChannelID, 10))
	if err != nil {
		return domain.ChannelStats{}, fmt.Errorf("query channel stats recent posts: %w", err)
	}
	for recentRows.Next() {
		var item domain.ChannelStatsRecentPost
		if err := recentRows.Scan(&item.MessageID, &item.Views, &item.Forwards, &item.Reactions); err != nil {
			recentRows.Close()
			return domain.ChannelStats{}, err
		}
		stats.RecentPosts = append(stats.RecentPosts, item)
	}
	if err := recentRows.Err(); err != nil {
		recentRows.Close()
		return domain.ChannelStats{}, err
	}
	recentRows.Close()
	return stats, nil
}

func (s *ChannelStore) GetChannelMessageStats(ctx context.Context, req domain.ChannelMessageStatsRequest) (domain.ChannelMessageStats, error) {
	if req.ViewerUserID == 0 || req.ChannelID == 0 || req.MessageID <= 0 ||
		req.MessageID > domain.MaxMessageBoxID || !req.Period.Valid() {
		return domain.ChannelMessageStats{}, domain.ErrMessageIDInvalid
	}
	channel, member, err := s.getChannelForMember(ctx, s.db, req.ViewerUserID, req.ChannelID)
	if err != nil {
		return domain.ChannelMessageStats{}, err
	}
	if member.Role != domain.ChannelRoleCreator && member.Role != domain.ChannelRoleAdmin {
		return domain.ChannelMessageStats{}, domain.ErrChannelAdminRequired
	}
	message, err := s.getChannelMessage(ctx, s.db, req.ChannelID, req.MessageID)
	if err != nil || message.Deleted {
		return domain.ChannelMessageStats{}, domain.ErrMessageIDInvalid
	}
	days, dayIndex := newPGStatsDays(req.Period)
	viewRows, err := s.db.Query(ctx, `
SELECT (viewed_at / 86400) * 86400 AS day, count(*)::int
FROM channel_message_viewers
WHERE channel_id = $1 AND message_id = $2 AND viewed_at >= $3 AND viewed_at < $4
GROUP BY day`, req.ChannelID, req.MessageID, req.Period.MinDate, req.Period.MaxDate)
	if err != nil {
		return domain.ChannelMessageStats{}, fmt.Errorf("query channel message stats views: %w", err)
	}
	for viewRows.Next() {
		var date, count int
		if err := viewRows.Scan(&date, &count); err != nil {
			viewRows.Close()
			return domain.ChannelMessageStats{}, err
		}
		if i, ok := dayIndex[date]; ok {
			days[i].Views = count
		}
	}
	if err := viewRows.Err(); err != nil {
		viewRows.Close()
		return domain.ChannelMessageStats{}, err
	}
	viewRows.Close()

	reactionRows, err := s.db.Query(ctx, `
SELECT (reaction_date / 86400) * 86400 AS day, reaction_type, reaction_value, count(*)::int
FROM channel_message_reactions
WHERE channel_id = $1 AND message_id = $2 AND reaction_date >= $3 AND reaction_date < $4
GROUP BY day, reaction_type, reaction_value
ORDER BY day, reaction_type, reaction_value`, req.ChannelID, req.MessageID, req.Period.MinDate, req.Period.MaxDate)
	if err != nil {
		return domain.ChannelMessageStats{}, fmt.Errorf("query channel message stats reactions: %w", err)
	}
	for reactionRows.Next() {
		var date, count int
		var reactionType, reactionValue string
		if err := reactionRows.Scan(&date, &reactionType, &reactionValue, &count); err != nil {
			reactionRows.Close()
			return domain.ChannelMessageStats{}, err
		}
		reaction, ok := domain.MessageReactionFromValue(domain.MessageReactionType(reactionType), reactionValue)
		if !ok {
			reactionRows.Close()
			return domain.ChannelMessageStats{}, fmt.Errorf("invalid persisted message stats reaction %q/%q", reactionType, reactionValue)
		}
		if i, ok := dayIndex[date]; ok {
			days[i].Reactions += count
			days[i].ByReaction = append(days[i].ByReaction, domain.StatsReactionCount{Reaction: reaction, Count: count})
		}
	}
	if err := reactionRows.Err(); err != nil {
		reactionRows.Close()
		return domain.ChannelMessageStats{}, err
	}
	reactionRows.Close()
	sortPGStatsReactions(days)
	return domain.ChannelMessageStats{Channel: channel, Message: message, Period: req.Period, Days: days}, nil
}

func (s *ChannelStore) ListChannelMessagePublicForwards(ctx context.Context, req domain.ChannelMessagePublicForwardListRequest) (domain.ChannelMessagePublicForwardList, error) {
	if req.ViewerUserID == 0 || req.ChannelID == 0 || req.MessageID <= 0 ||
		req.MessageID > domain.MaxMessageBoxID || req.Limit <= 0 || req.Limit > domain.MaxChannelMessagePublicForwards {
		return domain.ChannelMessagePublicForwardList{}, domain.ErrChannelInvalid
	}
	cursor, err := domain.ParseChannelMessagePublicForwardCursor(req.Offset)
	if err != nil {
		return domain.ChannelMessagePublicForwardList{}, err
	}
	_, member, err := s.getChannelForMember(ctx, s.db, req.ViewerUserID, req.ChannelID)
	if err != nil {
		return domain.ChannelMessagePublicForwardList{}, err
	}
	if member.Role != domain.ChannelRoleCreator && member.Role != domain.ChannelRoleAdmin {
		return domain.ChannelMessagePublicForwardList{}, domain.ErrChannelAdminRequired
	}
	source, err := s.getChannelMessage(ctx, s.db, req.ChannelID, req.MessageID)
	if err != nil || source.Deleted {
		return domain.ChannelMessagePublicForwardList{}, domain.ErrMessageIDInvalid
	}

	where := `
NOT deleted
AND fwd_from #>> '{From,Type}' = $1
AND fwd_from #>> '{From,ID}' = $2
AND fwd_from #>> '{ChannelPost}' = $3
AND EXISTS (
  SELECT 1 FROM channels c
  WHERE c.id = channel_messages.channel_id
    AND NOT c.deleted
    AND (c.broadcast OR c.megagroup)
    AND btrim(COALESCE(c.username, '')) <> ''
)`
	args := []any{string(domain.PeerTypeChannel), strconv.FormatInt(req.ChannelID, 10), strconv.Itoa(req.MessageID)}
	var count int
	if err := s.db.QueryRow(ctx, `SELECT count(*)::int FROM channel_messages WHERE `+where, args...).Scan(&count); err != nil {
		return domain.ChannelMessagePublicForwardList{}, fmt.Errorf("count channel message public forwards: %w", err)
	}
	cursorClause := ""
	if cursor.Date != 0 {
		args = append(args, cursor.Date, cursor.ChannelID, cursor.MessageID)
		cursorClause = fmt.Sprintf(`
AND (
  message_date < $%d
  OR (message_date = $%d AND (
    channel_id > $%d
    OR (channel_id = $%d AND id < $%d)
  ))
)`, len(args)-2, len(args)-2, len(args)-1, len(args)-1, len(args))
	}
	args = append(args, req.Limit+1)
	rows, err := s.db.Query(ctx, `
SELECT `+channelMessageColumns+`
FROM channel_messages
WHERE `+where+cursorClause+`
ORDER BY message_date DESC, channel_id ASC, id DESC
LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return domain.ChannelMessagePublicForwardList{}, fmt.Errorf("list channel message public forwards: %w", err)
	}
	defer rows.Close()
	messages := make([]domain.ChannelMessage, 0, req.Limit+1)
	for rows.Next() {
		message, err := scanChannelMessage(rows)
		if err != nil {
			return domain.ChannelMessagePublicForwardList{}, err
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return domain.ChannelMessagePublicForwardList{}, err
	}
	next := ""
	if len(messages) > req.Limit {
		messages = messages[:req.Limit]
		next = domain.FormatChannelMessagePublicForwardCursor(messages[len(messages)-1])
	}
	return domain.ChannelMessagePublicForwardList{Count: count, Messages: messages, NextOffset: next}, nil
}

func newPGStatsDays(period domain.StatsPeriod) ([]domain.ChannelStatsDay, map[int]int) {
	start, end := pgStatsDay(period.MinDate), pgStatsDay(period.MaxDate-1)
	days := make([]domain.ChannelStatsDay, 0, (end-start)/86400+1)
	index := make(map[int]int)
	for date := start; date <= end; date += 86400 {
		index[date] = len(days)
		days = append(days, domain.ChannelStatsDay{Date: date})
	}
	return days, index
}

func pgStatsDay(date int) int {
	if date <= 0 {
		return 0
	}
	return date - date%86400
}

func pgStatsMemberActiveAt(joinedAt, leftAt, at int) bool {
	return joinedAt > 0 && joinedAt <= at && (leftAt == 0 || leftAt > at)
}

func pgStatsAverage(current int64, currentCount int, previous int64, previousCount int) domain.StatsValueAndPrev {
	var out domain.StatsValueAndPrev
	if currentCount > 0 {
		out.Current = float64(current) / float64(currentCount)
	}
	if previousCount > 0 {
		out.Previous = float64(previous) / float64(previousCount)
	}
	return out
}

func sortPGStatsReactions(days []domain.ChannelStatsDay) {
	for i := range days {
		sort.Slice(days[i].ByReaction, func(a, b int) bool {
			return days[i].ByReaction[a].Reaction.Key() < days[i].ByReaction[b].Reaction.Key()
		})
	}
}
