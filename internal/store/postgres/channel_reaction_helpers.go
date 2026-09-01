package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"telesrv/internal/domain"
	"telesrv/internal/store/postgres/sqlcgen"
)

type channelReactionMessageKey struct {
	channelID int64
	messageID int
}

// channelMessagePairs 把 {channelID: [messageID...]} 摊平成两条等长并行数组，供
// `WHERE (channel_id, message_id) IN (SELECT * FROM unnest($a::bigint[], $b::int[]))`
// 跨频道一次批量取，消除「每频道一条 SQL」的 N+1。
func channelMessagePairs(idsByChannel map[int64][]int32) ([]int64, []int32) {
	var channels []int64
	var messages []int32
	for channelID, ids := range idsByChannel {
		for _, id := range ids {
			channels = append(channels, channelID)
			messages = append(messages, id)
		}
	}
	return channels, messages
}

type channelReactionCursor struct {
	date         int
	userID       int64
	reactionType domain.MessageReactionType
	value        string
	legacyValue  bool
}

func emptyChannelMessageReactions(channel domain.Channel) domain.ChannelMessageReactions {
	return domain.ChannelMessageReactions{
		CanSeeList: !channel.Broadcast || channel.Megagroup,
		Results:    []domain.ChannelMessageReactionCount{},
		Recent:     []domain.ChannelMessagePeerReaction{},
	}
}

func (s *ChannelStore) populateChannelMessagesReactions(ctx context.Context, db sqlcgen.DBTX, viewerUserID int64, channels []domain.Channel, messages []domain.ChannelMessage) error {
	return s.populateChannelMessagesReactionsWhere(ctx, db, viewerUserID, channels, messages, nil, false)
}

func (s *ChannelStore) populateChannelMessagesReactionsWhere(
	ctx context.Context,
	db sqlcgen.DBTX,
	viewerUserID int64,
	channels []domain.Channel,
	messages []domain.ChannelMessage,
	reactionEligible func(domain.ChannelMessage) bool,
	unreadAlreadyProjected bool,
) error {
	if len(messages) == 0 {
		return nil
	}
	// poll enrichment 与 reactions 同点位挂载：所有频道消息读路径都经过本函数（见 channel_polls.go）。
	if err := s.populateChannelMessagesPolls(ctx, db, viewerUserID, messages); err != nil {
		return err
	}
	if !unreadAlreadyProjected {
		if err := populateChannelMessageUnreadFlags(ctx, db, viewerUserID, messages); err != nil {
			return err
		}
	}
	channelsByID := make(map[int64]domain.Channel, len(channels))
	for _, ch := range channels {
		if ch.ID != 0 {
			channelsByID[ch.ID] = ch
		}
	}
	indexes := make(map[channelReactionMessageKey][]int)
	idsByChannel := make(map[int64][]int32)
	for i := range messages {
		if messages[i].ChannelID == 0 || messages[i].ID <= 0 {
			continue
		}
		if reactionEligible != nil && !reactionEligible(messages[i]) {
			continue
		}
		key := channelReactionMessageKey{channelID: messages[i].ChannelID, messageID: messages[i].ID}
		if _, ok := indexes[key]; !ok {
			idsByChannel[messages[i].ChannelID] = append(idsByChannel[messages[i].ChannelID], int32(messages[i].ID))
		}
		indexes[key] = append(indexes[key], i)
	}
	if len(idsByChannel) == 0 {
		return nil
	}
	// 缺失的频道元数据一次批量补齐（emptyChannelMessageReactions 需要 Broadcast/Megagroup），
	// 替代原来「每个未知频道一条 getChannelByID」的 N+1。
	var missing []int64
	for channelID := range idsByChannel {
		if _, ok := channelsByID[channelID]; !ok {
			missing = append(missing, channelID)
		}
	}
	if len(missing) > 0 {
		fetched, err := listChannelsByIDs(ctx, db, missing)
		if err != nil {
			return err
		}
		for _, ch := range fetched {
			channelsByID[ch.ID] = ch
		}
	}
	// 跨频道 (channel, message) 对；recent 反应仅对「非广播或超级群」频道下发（广播 reaction
	// 匿名，与官方一致只给计数，不暴露反应者身份）。
	pairChannels, pairMessages := channelMessagePairs(idsByChannel)
	var recentChannels []int64
	var recentMessages []int32
	for channelID, ids := range idsByChannel {
		ch := channelsByID[channelID]
		if ch.Broadcast && !ch.Megagroup {
			continue
		}
		for _, id := range ids {
			recentChannels = append(recentChannels, channelID)
			recentMessages = append(recentMessages, id)
		}
	}

	// 1) 反应计数：跨频道一次 GROUP BY。
	countRows, err := db.Query(ctx, `
SELECT channel_id, message_id, reaction_type, reaction_value, COUNT(*)::int,
       COALESCE(MAX(CASE WHEN reacted_user_id = $1 THEN chosen_order ELSE 0 END), 0)::int,
       COALESCE(MAX(reaction_date), 0)::int
FROM channel_message_reactions
WHERE (channel_id, message_id) IN (SELECT * FROM unnest($2::bigint[], $3::int[]))
GROUP BY channel_id, message_id, reaction_type, reaction_value
ORDER BY channel_id ASC, message_id ASC, COUNT(*) DESC, COALESCE(MAX(reaction_date), 0) DESC, reaction_type ASC, reaction_value ASC`, viewerUserID, pairChannels, pairMessages)
	if err != nil {
		return fmt.Errorf("load channel message reaction counts: %w", err)
	}
	for countRows.Next() {
		var channelID int64
		var msgID int
		var reactionType, reactionValue string
		var count, chosenOrder, latestDate int
		if err := countRows.Scan(&channelID, &msgID, &reactionType, &reactionValue, &count, &chosenOrder, &latestDate); err != nil {
			countRows.Close()
			return err
		}
		_ = latestDate
		ch := channelsByID[channelID]
		key := channelReactionMessageKey{channelID: channelID, messageID: msgID}
		for _, idx := range indexes[key] {
			if messages[idx].Reactions == nil {
				reactions := emptyChannelMessageReactions(ch)
				messages[idx].Reactions = &reactions
			}
			reaction, ok := domain.MessageReactionFromValue(domain.MessageReactionType(reactionType), reactionValue)
			if !ok {
				continue
			}
			messages[idx].Reactions.Results = append(messages[idx].Reactions.Results, domain.ChannelMessageReactionCount{
				Reaction:    reaction,
				Count:       count,
				ChosenOrder: chosenOrder,
			})
		}
	}
	if err := countRows.Err(); err != nil {
		countRows.Close()
		return err
	}
	countRows.Close()

	// 2) recent 反应（仅超级群/非广播）：跨频道一次 window 查询，PARTITION BY (channel,message)。
	if len(recentChannels) > 0 {
		recentRows, err := db.Query(ctx, `
SELECT channel_id, message_id, reacted_user_id, sender_user_id, reaction_type, reaction_value,
       big, unread, chosen_order, reaction_date
FROM (
    SELECT channel_id, message_id, reacted_user_id, sender_user_id, reaction_type, reaction_value,
           big, unread, chosen_order, reaction_date,
           row_number() OVER (
               PARTITION BY channel_id, message_id
               ORDER BY reaction_date DESC, reacted_user_id DESC, reaction_type ASC, reaction_value ASC
           ) AS rn
    FROM channel_message_reactions
    WHERE (channel_id, message_id) IN (SELECT * FROM unnest($1::bigint[], $2::int[]))
) ranked
WHERE rn <= $3
ORDER BY channel_id ASC, message_id ASC, reaction_date DESC, reacted_user_id DESC, reaction_type ASC, reaction_value ASC`, recentChannels, recentMessages, domain.MaxChannelMessageReactionRecent)
		if err != nil {
			return fmt.Errorf("load channel message recent reactions: %w", err)
		}
		for recentRows.Next() {
			row, err := scanChannelMessagePeerReaction(recentRows, viewerUserID)
			if err != nil {
				recentRows.Close()
				return err
			}
			ch := channelsByID[row.ChannelID]
			key := channelReactionMessageKey{channelID: row.ChannelID, messageID: row.MessageID}
			for _, idx := range indexes[key] {
				if messages[idx].Reactions == nil {
					reactions := emptyChannelMessageReactions(ch)
					messages[idx].Reactions = &reactions
				}
				messages[idx].Reactions.Recent = append(messages[idx].Reactions.Recent, row)
			}
		}
		if err := recentRows.Err(); err != nil {
			recentRows.Close()
			return err
		}
		recentRows.Close()
	}

	return nil
}

// populateChannelDialogTopMessageReactions keeps poll and unread-mention
// enrichment exact for every message, but uses the shared top-message
// existence cache to avoid querying three reaction tables when no reaction row
// can possibly contribute to the viewer projection.
func (s *ChannelStore) populateChannelDialogTopMessageReactions(
	ctx context.Context,
	db sqlcgen.DBTX,
	viewerUserID int64,
	channels []domain.Channel,
	messages []domain.ChannelMessage,
	unreadAlreadyProjected bool,
) error {
	if !s.topMessageCacheActive(db) || len(messages) == 0 {
		return s.populateChannelMessagesReactions(ctx, db, viewerUserID, channels, messages)
	}
	presence, err := s.topMsgCache.reactionPresenceFor(ctx, db, messages)
	if err != nil {
		return fmt.Errorf("load channel top reaction presence: %w", err)
	}
	return s.populateChannelMessagesReactionsWhere(ctx, db, viewerUserID, channels, messages, func(msg domain.ChannelMessage) bool {
		return presence[channelMessageLookupKey{channelID: msg.ChannelID, id: msg.ID}].any()
	}, unreadAlreadyProjected)
}

func channelReactionOffset(row domain.ChannelMessagePeerReaction) string {
	return strconv.Itoa(row.Date) + ":" + strconv.FormatInt(row.UserID, 10) + ":" + string(row.Reaction.Type) + ":" + row.Reaction.Value()
}

func parseChannelReactionOffset(offset string) (channelReactionCursor, bool) {
	parts := strings.SplitN(offset, ":", 4)
	if len(parts) != 3 && len(parts) != 4 {
		return channelReactionCursor{}, false
	}
	date, err := strconv.Atoi(parts[0])
	if err != nil || date < 0 {
		return channelReactionCursor{}, false
	}
	userID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || userID < 0 {
		return channelReactionCursor{}, false
	}
	if len(parts) == 3 {
		return channelReactionCursor{date: date, userID: userID, value: parts[2], legacyValue: true}, true
	}
	return channelReactionCursor{date: date, userID: userID, reactionType: domain.MessageReactionType(parts[2]), value: parts[3]}, true
}

func scanChannelMessagePeerReaction(row rowScanner, viewerUserID int64) (domain.ChannelMessagePeerReaction, error) {
	var out domain.ChannelMessagePeerReaction
	var reactionType, reactionValue string
	if err := row.Scan(
		&out.ChannelID,
		&out.MessageID,
		&out.UserID,
		&out.SenderUserID,
		&reactionType,
		&reactionValue,
		&out.Big,
		&out.Unread,
		&out.ChosenOrder,
		&out.Date,
	); err != nil {
		return domain.ChannelMessagePeerReaction{}, err
	}
	out.My = out.UserID == viewerUserID
	reaction, ok := domain.MessageReactionFromValue(domain.MessageReactionType(reactionType), reactionValue)
	if !ok {
		return domain.ChannelMessagePeerReaction{}, fmt.Errorf("invalid channel message reaction value")
	}
	out.Reaction = reaction
	return out, nil
}
