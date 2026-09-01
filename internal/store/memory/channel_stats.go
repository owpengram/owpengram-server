package memory

import (
	"context"
	"sort"
	"strings"
	"unicode/utf8"

	"telesrv/internal/domain"
)

func (s *ChannelStore) GetChannelStats(_ context.Context, req domain.ChannelStatsRequest) (domain.ChannelStats, error) {
	if req.ViewerUserID == 0 || req.ChannelID == 0 || !req.Period.Valid() {
		return domain.ChannelStats{}, domain.ErrChannelInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	channel, _, err := s.statsAdminChannelLocked(req.ViewerUserID, req.ChannelID)
	if err != nil {
		return domain.ChannelStats{}, err
	}

	stats := domain.ChannelStats{Channel: cloneChannel(channel), Period: req.Period}
	days, dayIndex := newMemoryStatsDays(req.Period)
	prevMin := req.Period.PreviousMinDate()
	var currentMessages, previousMessages int
	var currentViews, previousViews int
	currentPosters := make(map[int64]struct{})
	previousPosters := make(map[int64]struct{})
	currentMessageDay := make(map[int]int)
	previousMessageIDs := make(map[int]struct{})
	currentMessageIDs := make(map[int]struct{})
	top := make(map[int64]struct{ messages, chars int })

	for _, member := range s.members[req.ChannelID] {
		if memoryStatsMemberActiveAt(member, req.Period.MaxDate-1) {
			stats.Members.Current++
		}
		if memoryStatsMemberActiveAt(member, req.Period.MinDate-1) {
			stats.Members.Previous++
		}
		if i, ok := dayIndex[memoryStatsDay(member.JoinedAt)]; ok && member.JoinedAt >= req.Period.MinDate && member.JoinedAt < req.Period.MaxDate {
			days[i].NewMembers++
		}
	}
	for i := range days {
		at := days[i].Date + 86400 - 1
		if at >= req.Period.MaxDate {
			at = req.Period.MaxDate - 1
		}
		for _, member := range s.members[req.ChannelID] {
			if memoryStatsMemberActiveAt(member, at) {
				days[i].Members++
			}
		}
	}

	for _, msg := range s.messages[req.ChannelID] {
		if msg.Deleted || msg.Action != nil {
			continue
		}
		switch {
		case msg.Date >= req.Period.MinDate && msg.Date < req.Period.MaxDate:
			currentMessages++
			currentViews += msg.ViewsCount
			currentPosters[msg.SenderUserID] = struct{}{}
			currentMessageIDs[msg.ID] = struct{}{}
			if i, ok := dayIndex[memoryStatsDay(msg.Date)]; ok {
				currentMessageDay[msg.ID] = i
				days[i].Messages++
				days[i].Views += msg.ViewsCount
			}
			entry := top[msg.SenderUserID]
			entry.messages++
			entry.chars += utf8.RuneCountInString(msg.Body)
			top[msg.SenderUserID] = entry
		case msg.Date >= prevMin && msg.Date < req.Period.MinDate:
			previousMessages++
			previousViews += msg.ViewsCount
			previousPosters[msg.SenderUserID] = struct{}{}
			previousMessageIDs[msg.ID] = struct{}{}
		}
	}

	currentViewerIDs := make(map[int64]struct{})
	previousViewerIDs := make(map[int64]struct{})
	dayViewerIDs := make(map[int]map[int64]struct{}, len(days))
	for _, viewers := range s.msgViewers[req.ChannelID] {
		for userID, viewedAt := range viewers {
			switch {
			case viewedAt >= req.Period.MinDate && viewedAt < req.Period.MaxDate:
				currentViewerIDs[userID] = struct{}{}
				if i, ok := dayIndex[memoryStatsDay(viewedAt)]; ok {
					if dayViewerIDs[i] == nil {
						dayViewerIDs[i] = make(map[int64]struct{})
					}
					dayViewerIDs[i][userID] = struct{}{}
				}
			case viewedAt >= prevMin && viewedAt < req.Period.MinDate:
				previousViewerIDs[userID] = struct{}{}
			}
		}
	}

	var currentReactions, previousReactions int
	for messageID, byUser := range s.reactions[req.ChannelID] {
		_, current := currentMessageIDs[messageID]
		_, previous := previousMessageIDs[messageID]
		for _, rows := range byUser {
			for _, row := range rows {
				if current {
					currentReactions++
					if i, ok := currentMessageDay[messageID]; ok {
						days[i].Reactions++
						addMemoryStatsReaction(&days[i], row.Reaction)
					}
				} else if previous {
					previousReactions++
				}
			}
		}
	}

	forwardCounts := s.publicForwardCountsLocked(req.ChannelID)
	currentShares, previousShares := 0, 0
	for messageID, count := range forwardCounts {
		if _, ok := currentMessageIDs[messageID]; ok {
			currentShares += count
			if i, ok := currentMessageDay[messageID]; ok {
				days[i].Shares += count
			}
		} else if _, ok := previousMessageIDs[messageID]; ok {
			previousShares += count
		}
	}

	for i := range days {
		days[i].Viewers = len(dayViewerIDs[i])
		posters := make(map[int64]struct{})
		start, end := days[i].Date, days[i].Date+86400
		for _, msg := range s.messages[req.ChannelID] {
			if !msg.Deleted && msg.Action == nil && msg.Date >= start && msg.Date < end {
				posters[msg.SenderUserID] = struct{}{}
			}
		}
		days[i].Posters = len(posters)
		sort.Slice(days[i].ByReaction, func(a, b int) bool {
			return days[i].ByReaction[a].Reaction.Key() < days[i].ByReaction[b].Reaction.Key()
		})
	}

	stats.Messages = domain.StatsValueAndPrev{Current: float64(currentMessages), Previous: float64(previousMessages)}
	stats.Viewers = domain.StatsValueAndPrev{Current: float64(len(currentViewerIDs)), Previous: float64(len(previousViewerIDs))}
	stats.Posters = domain.StatsValueAndPrev{Current: float64(len(currentPosters)), Previous: float64(len(previousPosters))}
	stats.ViewsPerPost = memoryStatsAverage(currentViews, currentMessages, previousViews, previousMessages)
	stats.SharesPerPost = memoryStatsAverage(currentShares, currentMessages, previousShares, previousMessages)
	stats.ReactionsPerPost = memoryStatsAverage(currentReactions, currentMessages, previousReactions, previousMessages)
	stats.Days = days

	for userID, entry := range top {
		if userID == 0 || entry.messages == 0 {
			continue
		}
		stats.TopPosters = append(stats.TopPosters, domain.ChannelStatsTopPoster{
			UserID: userID, Messages: entry.messages, AvgChars: entry.chars / entry.messages,
		})
	}
	sort.Slice(stats.TopPosters, func(i, j int) bool {
		if stats.TopPosters[i].Messages != stats.TopPosters[j].Messages {
			return stats.TopPosters[i].Messages > stats.TopPosters[j].Messages
		}
		return stats.TopPosters[i].UserID < stats.TopPosters[j].UserID
	})
	if len(stats.TopPosters) > domain.MaxChannelStatsTopPosters {
		stats.TopPosters = stats.TopPosters[:domain.MaxChannelStatsTopPosters]
	}

	messages := append([]domain.ChannelMessage(nil), s.messages[req.ChannelID]...)
	sort.Slice(messages, func(i, j int) bool {
		if messages[i].Date != messages[j].Date {
			return messages[i].Date > messages[j].Date
		}
		return messages[i].ID > messages[j].ID
	})
	for _, msg := range messages {
		if msg.Deleted || msg.Action != nil {
			continue
		}
		stats.RecentPosts = append(stats.RecentPosts, domain.ChannelStatsRecentPost{
			MessageID: msg.ID,
			Views:     msg.ViewsCount,
			Forwards:  forwardCounts[msg.ID],
			Reactions: memoryStatsReactionCount(s.reactions[req.ChannelID][msg.ID]),
		})
		if len(stats.RecentPosts) == domain.MaxChannelStatsRecentPosts {
			break
		}
	}
	return stats, nil
}

func (s *ChannelStore) GetChannelMessageStats(_ context.Context, req domain.ChannelMessageStatsRequest) (domain.ChannelMessageStats, error) {
	if req.ViewerUserID == 0 || req.ChannelID == 0 || req.MessageID <= 0 ||
		req.MessageID > domain.MaxMessageBoxID || !req.Period.Valid() {
		return domain.ChannelMessageStats{}, domain.ErrMessageIDInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	channel, _, err := s.statsAdminChannelLocked(req.ViewerUserID, req.ChannelID)
	if err != nil {
		return domain.ChannelMessageStats{}, err
	}
	message, ok := s.findMessageLocked(req.ChannelID, req.MessageID)
	if !ok || message.Deleted {
		return domain.ChannelMessageStats{}, domain.ErrMessageIDInvalid
	}
	days, dayIndex := newMemoryStatsDays(req.Period)
	for _, viewedAt := range s.msgViewers[req.ChannelID][req.MessageID] {
		if i, ok := dayIndex[memoryStatsDay(viewedAt)]; ok && viewedAt >= req.Period.MinDate && viewedAt < req.Period.MaxDate {
			days[i].Views++
		}
	}
	for _, rows := range s.reactions[req.ChannelID][req.MessageID] {
		for _, row := range rows {
			if i, ok := dayIndex[memoryStatsDay(row.Date)]; ok && row.Date >= req.Period.MinDate && row.Date < req.Period.MaxDate {
				days[i].Reactions++
				addMemoryStatsReaction(&days[i], row.Reaction)
			}
		}
	}
	for i := range days {
		sort.Slice(days[i].ByReaction, func(a, b int) bool {
			return days[i].ByReaction[a].Reaction.Key() < days[i].ByReaction[b].Reaction.Key()
		})
	}
	return domain.ChannelMessageStats{
		Channel: cloneChannel(channel), Message: cloneChannelMessage(message), Period: req.Period, Days: days,
	}, nil
}

func (s *ChannelStore) ListChannelMessagePublicForwards(_ context.Context, req domain.ChannelMessagePublicForwardListRequest) (domain.ChannelMessagePublicForwardList, error) {
	if req.ViewerUserID == 0 || req.ChannelID == 0 || req.MessageID <= 0 ||
		req.MessageID > domain.MaxMessageBoxID || req.Limit <= 0 || req.Limit > domain.MaxChannelMessagePublicForwards {
		return domain.ChannelMessagePublicForwardList{}, domain.ErrChannelInvalid
	}
	cursor, err := domain.ParseChannelMessagePublicForwardCursor(req.Offset)
	if err != nil {
		return domain.ChannelMessagePublicForwardList{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, _, err := s.statsAdminChannelLocked(req.ViewerUserID, req.ChannelID); err != nil {
		return domain.ChannelMessagePublicForwardList{}, err
	}
	source, ok := s.findMessageLocked(req.ChannelID, req.MessageID)
	if !ok || source.Deleted {
		return domain.ChannelMessagePublicForwardList{}, domain.ErrMessageIDInvalid
	}
	all := make([]domain.ChannelMessage, 0)
	for channelID, channel := range s.channels {
		if !memoryStatsPublicChannel(channel) {
			continue
		}
		for _, msg := range s.messages[channelID] {
			if memoryStatsForwardsPost(msg, req.ChannelID, req.MessageID) {
				all = append(all, cloneChannelMessage(msg))
			}
		}
	}
	sort.Slice(all, func(i, j int) bool { return memoryStatsForwardBefore(all[i], all[j]) })
	page := make([]domain.ChannelMessage, 0, req.Limit)
	for _, msg := range all {
		if cursor.Date != 0 && !memoryStatsForwardAfterCursor(msg, cursor) {
			continue
		}
		page = append(page, msg)
		if len(page) == req.Limit+1 {
			break
		}
	}
	next := ""
	if len(page) > req.Limit {
		page = page[:req.Limit]
		next = domain.FormatChannelMessagePublicForwardCursor(page[len(page)-1])
	}
	return domain.ChannelMessagePublicForwardList{Count: len(all), Messages: page, NextOffset: next}, nil
}

func (s *ChannelStore) statsAdminChannelLocked(userID, channelID int64) (domain.Channel, domain.ChannelMember, error) {
	channel, member, err := s.channelAndMemberLocked(userID, channelID)
	if err != nil {
		return domain.Channel{}, domain.ChannelMember{}, err
	}
	if member.Role != domain.ChannelRoleCreator && member.Role != domain.ChannelRoleAdmin {
		return domain.Channel{}, domain.ChannelMember{}, domain.ErrChannelAdminRequired
	}
	return channel, member, nil
}

func newMemoryStatsDays(period domain.StatsPeriod) ([]domain.ChannelStatsDay, map[int]int) {
	start := memoryStatsDay(period.MinDate)
	end := memoryStatsDay(period.MaxDate - 1)
	days := make([]domain.ChannelStatsDay, 0, (end-start)/86400+1)
	index := make(map[int]int)
	for date := start; date <= end; date += 86400 {
		index[date] = len(days)
		days = append(days, domain.ChannelStatsDay{Date: date})
	}
	return days, index
}

func memoryStatsDay(date int) int {
	if date <= 0 {
		return 0
	}
	return date - date%86400
}

func memoryStatsMemberActiveAt(member domain.ChannelMember, at int) bool {
	return member.JoinedAt > 0 && member.JoinedAt <= at && (member.LeftAt == 0 || member.LeftAt > at)
}

func memoryStatsAverage(current, currentCount, previous, previousCount int) domain.StatsValueAndPrev {
	var out domain.StatsValueAndPrev
	if currentCount > 0 {
		out.Current = float64(current) / float64(currentCount)
	}
	if previousCount > 0 {
		out.Previous = float64(previous) / float64(previousCount)
	}
	return out
}

func addMemoryStatsReaction(day *domain.ChannelStatsDay, reaction domain.MessageReaction) {
	for i := range day.ByReaction {
		if day.ByReaction[i].Reaction.Key() == reaction.Key() {
			day.ByReaction[i].Count++
			return
		}
	}
	day.ByReaction = append(day.ByReaction, domain.StatsReactionCount{Reaction: reaction, Count: 1})
}

func memoryStatsReactionCount(byUser map[int64][]domain.ChannelMessagePeerReaction) int {
	count := 0
	for _, rows := range byUser {
		count += len(rows)
	}
	return count
}

func (s *ChannelStore) publicForwardCountsLocked(sourceChannelID int64) map[int]int {
	counts := make(map[int]int)
	for channelID, channel := range s.channels {
		if !memoryStatsPublicChannel(channel) {
			continue
		}
		for _, msg := range s.messages[channelID] {
			if msg.Deleted || msg.Forward == nil || msg.Forward.From.Type != domain.PeerTypeChannel ||
				msg.Forward.From.ID != sourceChannelID || msg.Forward.ChannelPost <= 0 {
				continue
			}
			counts[msg.Forward.ChannelPost]++
		}
	}
	return counts
}

func memoryStatsPublicChannel(channel domain.Channel) bool {
	return !channel.Deleted && strings.TrimSpace(channel.Username) != "" && (channel.Broadcast || channel.Megagroup)
}

func memoryStatsForwardsPost(msg domain.ChannelMessage, channelID int64, messageID int) bool {
	return !msg.Deleted && msg.Forward != nil && msg.Forward.From.Type == domain.PeerTypeChannel &&
		msg.Forward.From.ID == channelID && msg.Forward.ChannelPost == messageID
}

func memoryStatsForwardBefore(a, b domain.ChannelMessage) bool {
	if a.Date != b.Date {
		return a.Date > b.Date
	}
	if a.ChannelID != b.ChannelID {
		return a.ChannelID < b.ChannelID
	}
	return a.ID > b.ID
}

func memoryStatsForwardAfterCursor(msg domain.ChannelMessage, cursor domain.ChannelMessagePublicForwardCursor) bool {
	return msg.Date < cursor.Date ||
		(msg.Date == cursor.Date && (msg.ChannelID > cursor.ChannelID ||
			(msg.ChannelID == cursor.ChannelID && msg.ID < cursor.MessageID)))
}
