package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/iamxvbaba/td/tg"

	"github.com/iamxvbaba/td/tlprofile"
	"telesrv/internal/domain"
)

const (
	maxStatsPublicForwardsLimit = 100
	maxStatsOffsetLength        = 128
	maxStatsGraphTokenLength    = 128
)

func (r *Router) registerStats(d *tlprofile.Dispatcher) {
	registerRPC[*tg.StatsGetBroadcastStatsRequest](d, tlprofile.SemanticMethodStatsGetBroadcastStats, func(ctx context.Context, layerRequest *tg.StatsGetBroadcastStatsRequest) (any, error) {
		return r.onStatsGetBroadcastStats(ctx, layerRequest)
	})
	registerRPC[*tg.StatsGetMegagroupStatsRequest](d, tlprofile.SemanticMethodStatsGetMegagroupStats, func(ctx context.Context, layerRequest *tg.StatsGetMegagroupStatsRequest) (any, error) {
		return r.onStatsGetMegagroupStats(ctx, layerRequest)
	})
	registerRPC[*tg.StatsGetMessageStatsRequest](d, tlprofile.SemanticMethodStatsGetMessageStats, func(ctx context.Context, layerRequest *tg.StatsGetMessageStatsRequest) (any, error) {
		return r.onStatsGetMessageStats(ctx, layerRequest)
	})
	registerRPC[*tg.StatsGetMessagePublicForwardsRequest](d, tlprofile.SemanticMethodStatsGetMessagePublicForwards, func(ctx context.Context, layerRequest *tg.StatsGetMessagePublicForwardsRequest) (any, error) {
		return r.onStatsGetMessagePublicForwards(ctx, layerRequest)
	})
	registerRPC[*tg.StatsLoadAsyncGraphRequest](d, tlprofile.SemanticMethodStatsLoadAsyncGraph, func(ctx context.Context, layerRequest *tg.StatsLoadAsyncGraphRequest) (any, error) {
		return r.onStatsLoadAsyncGraph(ctx, layerRequest)
	})
	registerRPC[*tg.StatsGetStoryStatsRequest](d, tlprofile.SemanticMethodStatsGetStoryStats, func(ctx context.Context, layerRequest *tg.StatsGetStoryStatsRequest) (any, error) {
		return r.onStatsGetStoryStats(ctx, layerRequest)
	})
	registerRPC[*tg.StatsGetStoryPublicForwardsRequest](d, tlprofile.SemanticMethodStatsGetStoryPublicForwards, func(ctx context.Context, layerRequest *tg.StatsGetStoryPublicForwardsRequest) (any, error) {
		return r.onStatsGetStoryPublicForwards(ctx, layerRequest)
	})
	registerRPC[*tg.StatsGetPollStatsRequest](d, tlprofile.SemanticMethodStatsGetPollStats, func(ctx context.Context, layerRequest *tg.StatsGetPollStatsRequest) (any, error) {
		return r.onStatsGetPollStats(ctx, layerRequest)
	})
}

func (r *Router) onStatsGetBroadcastStats(ctx context.Context, req *tg.StatsGetBroadcastStatsRequest) (*tg.StatsBroadcastStats, error) {
	view, err := r.statsChannelView(ctx, req.Channel)
	if err != nil {
		return nil, err
	}
	if !view.Channel.Broadcast {
		return nil, tgerr400("BROADCAST_REQUIRED")
	}
	stats, err := r.deps.Channels.GetStats(ctx, view.Self.UserID, domain.ChannelStatsRequest{
		ChannelID: view.Channel.ID,
		Period:    r.statsPeriod(),
	})
	if err != nil {
		return nil, statsServiceErr(err)
	}
	return r.tgBroadcastStats(stats), nil
}

func (r *Router) onStatsGetMegagroupStats(ctx context.Context, req *tg.StatsGetMegagroupStatsRequest) (*tg.StatsMegagroupStats, error) {
	view, err := r.statsChannelView(ctx, req.Channel)
	if err != nil {
		return nil, err
	}
	if !view.Channel.Megagroup {
		return nil, tgerr400("MEGAGROUP_REQUIRED")
	}
	stats, err := r.deps.Channels.GetStats(ctx, view.Self.UserID, domain.ChannelStatsRequest{
		ChannelID: view.Channel.ID,
		Period:    r.statsPeriod(),
	})
	if err != nil {
		return nil, statsServiceErr(err)
	}
	return r.tgMegagroupStats(ctx, view.Self.UserID, stats), nil
}

func (r *Router) onStatsGetMessageStats(ctx context.Context, req *tg.StatsGetMessageStatsRequest) (*tg.StatsMessageStats, error) {
	if req.MsgID <= 0 || req.MsgID > domain.MaxMessageBoxID {
		return nil, messageIDInvalidErr()
	}
	view, err := r.statsChannelView(ctx, req.Channel)
	if err != nil {
		return nil, err
	}
	stats, err := r.deps.Channels.GetMessageStats(ctx, view.Self.UserID, domain.ChannelMessageStatsRequest{
		ChannelID: view.Channel.ID,
		MessageID: req.MsgID,
		Period:    r.statsPeriod(),
	})
	if err != nil {
		return nil, statsServiceErr(err)
	}
	return &tg.StatsMessageStats{
		ViewsGraph: r.statsGraph(stats.Days, []statsGraphSeries{{
			Key: "views", Label: "Views", Color: statsGraphColors[0], Value: func(day domain.ChannelStatsDay) int { return day.Views },
		}}),
		ReactionsByEmotionGraph: r.statsReactionGraph(stats.Days),
	}, nil
}

func (r *Router) onStatsGetMessagePublicForwards(ctx context.Context, req *tg.StatsGetMessagePublicForwardsRequest) (*tg.StatsPublicForwards, error) {
	if req.MsgID <= 0 || req.MsgID > domain.MaxMessageBoxID {
		return nil, messageIDInvalidErr()
	}
	if req.Limit < 0 || req.Limit > maxStatsPublicForwardsLimit {
		return nil, limitInvalidErr()
	}
	if len(req.Offset) > maxStatsOffsetLength {
		return nil, tgerr400("OFFSET_INVALID")
	}
	view, err := r.statsChannelView(ctx, req.Channel)
	if err != nil {
		return nil, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = maxStatsPublicForwardsLimit
	}
	list, err := r.deps.Channels.ListMessagePublicForwards(ctx, view.Self.UserID, domain.ChannelMessagePublicForwardListRequest{
		ChannelID: view.Channel.ID,
		MessageID: req.MsgID,
		Offset:    req.Offset,
		Limit:     limit,
	})
	if err != nil {
		return nil, statsServiceErr(err)
	}
	views := make([]domain.StoryView, 0, len(list.Messages))
	for _, message := range list.Messages {
		views = append(views, domain.StoryView{Date: message.Date, PublicForward: &domain.StoryPublicForward{Message: message}})
	}
	return r.tgStatsPublicForwards(ctx, view.Self.UserID, domain.StoryPublicForwardList{
		Count: list.Count, Forwards: views, NextOffset: list.NextOffset,
	}), nil
}

func (r *Router) onStatsLoadAsyncGraph(_ context.Context, req *tg.StatsLoadAsyncGraphRequest) (tg.StatsGraphClass, error) {
	if len(req.Token) > maxStatsGraphTokenLength {
		return &tg.StatsGraphError{Error: "GRAPH_INVALID_RELOAD"}, nil
	}
	return &tg.StatsGraphError{Error: "GRAPH_INVALID_RELOAD"}, nil
}

func (r *Router) onStatsGetStoryStats(ctx context.Context, req *tg.StatsGetStoryStatsRequest) (*tg.StatsStoryStats, error) {
	if req.ID <= 0 || req.ID > domain.MaxStoryID {
		return nil, storyIDInvalidErr()
	}
	userID, _, err := r.currentUserID(ctx)
	if err != nil {
		return nil, internalErr()
	}
	peer, err := r.checkedDomainPeerFromInputPeer(ctx, userID, req.Peer)
	if err != nil {
		return nil, err
	}
	if r.deps.Stories != nil {
		if err := r.deps.Stories.CanViewStoryStats(ctx, userID, peer); err != nil {
			return nil, storyErr(err)
		}
	}
	date := int(r.clock.Now().Unix()) - 1
	views := 0
	reactions := []domain.ChannelMessageReactionCount(nil)
	if r.deps.Stories != nil {
		list, loadErr := r.deps.Stories.GetStoriesByID(ctx, userID, peer, []int{req.ID}, int(r.clock.Now().Unix()))
		if loadErr != nil {
			return nil, storyErr(loadErr)
		}
		for _, story := range list.Stories {
			if story.ID != req.ID || story.Owner != peer || story.Deleted {
				continue
			}
			date = story.Date
			views = story.Views.ViewsCount
			reactions = story.Views.Reactions
			break
		}
	}
	return &tg.StatsStoryStats{
		ViewsGraph:              r.statsSnapshotGraph("views", "Views", date, views),
		ReactionsByEmotionGraph: r.statsReactionSnapshotGraph(date, reactions),
	}, nil
}

func (r *Router) onStatsGetStoryPublicForwards(ctx context.Context, req *tg.StatsGetStoryPublicForwardsRequest) (*tg.StatsPublicForwards, error) {
	if req.ID <= 0 || req.ID > domain.MaxStoryID {
		return nil, storyIDInvalidErr()
	}
	if req.Limit < 0 || req.Limit > maxStatsPublicForwardsLimit || len(req.Offset) > maxStatsOffsetLength {
		return nil, limitInvalidErr()
	}
	if err := domain.ValidateStoryInteractionOffset(req.Offset, false); err != nil {
		return nil, storyErr(err)
	}
	userID, _, err := r.currentUserID(ctx)
	if err != nil {
		return nil, internalErr()
	}
	peer, err := r.checkedDomainPeerFromInputPeer(ctx, userID, req.Peer)
	if err != nil {
		return nil, err
	}
	if r.deps.Stories == nil || userID == 0 {
		return emptyStatsPublicForwards(), nil
	}
	limit := req.Limit
	if limit <= 0 || limit > domain.MaxStoryInteractionListLimit {
		limit = domain.MaxStoryInteractionListLimit
	}
	storyForwards, err := r.deps.Stories.GetStoryPublicForwards(ctx, userID, domain.StoryPublicForwardListRequest{
		ViewerUserID: userID,
		Owner:        peer,
		StoryID:      req.ID,
		Offset:       req.Offset,
		Limit:        limit,
	})
	if err != nil {
		return nil, storyErr(err)
	}
	messageForwards := domain.StoryMessageForwardList{}
	if r.deps.Channels != nil {
		messageForwards, err = r.deps.Channels.ListStoryMessageForwards(ctx, userID, domain.StoryMessageForwardListRequest{
			ViewerUserID:  userID,
			Owner:         peer,
			StoryID:       req.ID,
			Offset:        req.Offset,
			Limit:         limit,
			ForwardsFirst: true,
		})
		if err != nil {
			return nil, storyErr(err)
		}
	}
	hasMore := storyInteractionSourcesHaveMore(len(storyForwards.Forwards), len(messageForwards.Forwards), limit, storyForwards.NextOffset != "" || messageForwards.NextOffset != "")
	forwards := mergeStoryInteractionViews(storyForwards.Forwards, messageForwards.Forwards, limit, false, true)
	return r.tgStatsPublicForwards(ctx, userID, domain.StoryPublicForwardList{
		Count:      storyForwards.Count + messageForwards.Count,
		Forwards:   forwards,
		NextOffset: nextStoryInteractionOffset(forwards, limit, false, true, hasMore),
	}), nil
}

func (r *Router) onStatsGetPollStats(ctx context.Context, req *tg.StatsGetPollStatsRequest) (*tg.StatsPollStats, error) {
	if req.MsgID <= 0 || req.MsgID > domain.MaxMessageBoxID {
		return nil, messageIDInvalidErr()
	}
	userID, _, err := r.currentUserID(ctx)
	if err != nil {
		return nil, internalErr()
	}
	peer, err := r.checkedDomainPeerFromInputPeer(ctx, userID, req.Peer)
	if err != nil {
		return nil, err
	}
	date, votes := int(r.clock.Now().Unix())-1, 0
	if peer.Type == domain.PeerTypeChannel && r.deps.Channels != nil {
		history, loadErr := r.deps.Channels.GetMessages(ctx, userID, peer.ID, []int{req.MsgID})
		if loadErr != nil {
			return nil, channelInvalidErr(loadErr)
		}
		for _, message := range history.Messages {
			if message.ID == req.MsgID && message.Media != nil && message.Media.Poll != nil {
				date = message.Date
				if message.Media.Poll.Results != nil {
					votes = message.Media.Poll.Results.TotalVoters
				}
				break
			}
		}
	} else if peer.Type == domain.PeerTypeUser && r.deps.Messages != nil {
		messages, loadErr := r.deps.Messages.GetMessages(ctx, userID, []int{req.MsgID})
		if loadErr != nil {
			return nil, internalErr()
		}
		for _, message := range messages.Messages {
			if message.ID == req.MsgID && message.Peer == peer && message.Media != nil && message.Media.Poll != nil {
				date = message.Date
				if message.Media.Poll.Results != nil {
					votes = message.Media.Poll.Results.TotalVoters
				}
				break
			}
		}
	}
	return &tg.StatsPollStats{VotesGraph: r.statsSnapshotGraph("votes", "Votes", date, votes)}, nil
}

func (r *Router) statsChannelView(ctx context.Context, input tg.InputChannelClass) (domain.ChannelView, error) {
	_, view, err := r.channelView(ctx, input)
	if err != nil {
		return domain.ChannelView{}, err
	}
	if view.Self.Role != domain.ChannelRoleCreator && view.Self.Role != domain.ChannelRoleAdmin {
		return domain.ChannelView{}, tgerr400("CHAT_ADMIN_REQUIRED")
	}
	return view, nil
}

var statsGraphColors = [...]string{
	"#4A90E2", "#50E3C2", "#F5A623", "#D0021B", "#9013FE", "#7ED321", "#B8E986", "#BD10E0",
}

type statsGraphSeries struct {
	Key   string
	Label string
	Color string
	Value func(domain.ChannelStatsDay) int
}

type statsGraphJSON struct {
	Columns [][]any           `json:"columns"`
	Types   map[string]string `json:"types"`
	Names   map[string]string `json:"names"`
	Colors  map[string]string `json:"colors"`
}

func (r *Router) statsPeriod() domain.StatsPeriod {
	maxDate := int(r.clock.Now().Unix())
	if maxDate <= 7*86400 {
		maxDate = 7*86400 + 1
	}
	return domain.StatsPeriod{MinDate: maxDate - 7*86400, MaxDate: maxDate}
}

func tgStatsPeriod(period domain.StatsPeriod) tg.StatsDateRangeDays {
	return tg.StatsDateRangeDays{MinDate: period.MinDate, MaxDate: period.MaxDate}
}

func tgStatsValue(value domain.StatsValueAndPrev) tg.StatsAbsValueAndPrev {
	return tg.StatsAbsValueAndPrev{Current: value.Current, Previous: value.Previous}
}

func (r *Router) tgBroadcastStats(stats domain.ChannelStats) *tg.StatsBroadcastStats {
	recent := make([]tg.PostInteractionCountersClass, 0, len(stats.RecentPosts))
	for _, item := range stats.RecentPosts {
		recent = append(recent, &tg.PostInteractionCountersMessage{
			MsgID: item.MessageID, Views: item.Views, Forwards: item.Forwards, Reactions: item.Reactions,
		})
	}
	return &tg.StatsBroadcastStats{
		Period:               tgStatsPeriod(stats.Period),
		Followers:            tgStatsValue(stats.Members),
		ViewsPerPost:         tgStatsValue(stats.ViewsPerPost),
		SharesPerPost:        tgStatsValue(stats.SharesPerPost),
		ReactionsPerPost:     tgStatsValue(stats.ReactionsPerPost),
		ViewsPerStory:        tg.StatsAbsValueAndPrev{},
		SharesPerStory:       tg.StatsAbsValueAndPrev{},
		ReactionsPerStory:    tg.StatsAbsValueAndPrev{},
		EnabledNotifications: tg.StatsPercentValue{},
		GrowthGraph: r.statsGraph(stats.Days, []statsGraphSeries{{
			Key: "members", Label: "Followers", Color: statsGraphColors[0], Value: func(day domain.ChannelStatsDay) int { return day.Members },
		}}),
		FollowersGraph: r.statsGraph(stats.Days, []statsGraphSeries{{
			Key: "new_members", Label: "New followers", Color: statsGraphColors[1], Value: func(day domain.ChannelStatsDay) int { return day.NewMembers },
		}}),
		MuteGraph:     statsGraphUnavailable(),
		TopHoursGraph: statsGraphUnavailable(),
		InteractionsGraph: r.statsGraph(stats.Days, []statsGraphSeries{
			{Key: "views", Label: "Views", Color: statsGraphColors[0], Value: func(day domain.ChannelStatsDay) int { return day.Views }},
			{Key: "shares", Label: "Shares", Color: statsGraphColors[1], Value: func(day domain.ChannelStatsDay) int { return day.Shares }},
		}),
		IvInteractionsGraph:          statsGraphUnavailable(),
		ViewsBySourceGraph:           statsGraphUnavailable(),
		NewFollowersBySourceGraph:    statsGraphUnavailable(),
		LanguagesGraph:               statsGraphUnavailable(),
		ReactionsByEmotionGraph:      r.statsReactionGraph(stats.Days),
		StoryInteractionsGraph:       statsGraphUnavailable(),
		StoryReactionsByEmotionGraph: statsGraphUnavailable(),
		RecentPostsInteractions:      recent,
	}
}

func (r *Router) tgMegagroupStats(ctx context.Context, viewerUserID int64, stats domain.ChannelStats) *tg.StatsMegagroupStats {
	posterIDs := make([]int64, 0, len(stats.TopPosters))
	topPosters := make([]tg.StatsGroupTopPoster, 0, len(stats.TopPosters))
	for _, item := range stats.TopPosters {
		posterIDs = append(posterIDs, item.UserID)
		topPosters = append(topPosters, tg.StatsGroupTopPoster{UserID: item.UserID, Messages: item.Messages, AvgChars: item.AvgChars})
	}
	users := tgUsersForViewer(viewerUserID, r.domainUsersForIDs(ctx, viewerUserID, posterIDs))
	out := &tg.StatsMegagroupStats{
		Period:   tgStatsPeriod(stats.Period),
		Members:  tgStatsValue(stats.Members),
		Messages: tgStatsValue(stats.Messages),
		Viewers:  tgStatsValue(stats.Viewers),
		Posters:  tgStatsValue(stats.Posters),
		GrowthGraph: r.statsGraph(stats.Days, []statsGraphSeries{{
			Key: "members", Label: "Members", Color: statsGraphColors[0], Value: func(day domain.ChannelStatsDay) int { return day.Members },
		}}),
		MembersGraph: r.statsGraph(stats.Days, []statsGraphSeries{{
			Key: "new_members", Label: "New members", Color: statsGraphColors[1], Value: func(day domain.ChannelStatsDay) int { return day.NewMembers },
		}}),
		NewMembersBySourceGraph: statsGraphUnavailable(),
		LanguagesGraph:          statsGraphUnavailable(),
		MessagesGraph: r.statsGraph(stats.Days, []statsGraphSeries{{
			Key: "messages", Label: "Messages", Color: statsGraphColors[0], Value: func(day domain.ChannelStatsDay) int { return day.Messages },
		}}),
		ActionsGraph:  statsGraphUnavailable(),
		TopHoursGraph: statsGraphUnavailable(),
		WeekdaysGraph: statsGraphUnavailable(),
		TopPosters:    topPosters,
		TopAdmins:     []tg.StatsGroupTopAdmin{},
		TopInviters:   []tg.StatsGroupTopInviter{},
		Users:         users,
	}
	r.applyPeerReadModels(ctx, viewerUserID, out.Users, nil)
	return out
}

func (r *Router) statsGraph(days []domain.ChannelStatsDay, series []statsGraphSeries) tg.StatsGraphClass {
	graph := statsGraphJSON{
		Columns: make([][]any, 0, len(series)+1),
		Types:   map[string]string{"x": "x"},
		Names:   make(map[string]string, len(series)),
		Colors:  make(map[string]string, len(series)),
	}
	x := make([]any, 1, len(days)+1)
	x[0] = "x"
	for _, day := range days {
		x = append(x, int64(day.Date)*1000)
	}
	graph.Columns = append(graph.Columns, x)
	for i, item := range series {
		key := "y" + strconv.Itoa(i)
		column := make([]any, 1, len(days)+1)
		column[0] = key
		for _, day := range days {
			column = append(column, item.Value(day))
		}
		graph.Columns = append(graph.Columns, column)
		graph.Types[key] = "line"
		graph.Names[key] = item.Label
		color := item.Color
		if color == "" {
			color = statsGraphColors[i%len(statsGraphColors)]
		}
		graph.Colors[key] = color
	}
	data, err := json.Marshal(graph)
	if err != nil {
		return &tg.StatsGraphError{Error: "GRAPH_SERIALIZATION_FAILED"}
	}
	return &tg.StatsGraph{JSON: tg.DataJSON{Data: string(data)}}
}

func (r *Router) statsReactionGraph(days []domain.ChannelStatsDay) tg.StatsGraphClass {
	byKey := make(map[string]domain.MessageReaction)
	for _, day := range days {
		for _, item := range day.ByReaction {
			byKey[item.Reaction.Key()] = item.Reaction
		}
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return r.statsGraph(days, []statsGraphSeries{{
			Key: "reactions", Label: "Reactions", Color: statsGraphColors[2], Value: func(day domain.ChannelStatsDay) int { return day.Reactions },
		}})
	}
	series := make([]statsGraphSeries, 0, len(keys))
	for i, reactionKey := range keys {
		key := reactionKey
		reaction := byKey[key]
		series = append(series, statsGraphSeries{
			Key: key, Label: statsReactionLabel(reaction), Color: statsGraphColors[i%len(statsGraphColors)],
			Value: func(day domain.ChannelStatsDay) int {
				for _, item := range day.ByReaction {
					if item.Reaction.Key() == key {
						return item.Count
					}
				}
				return 0
			},
		})
	}
	return r.statsGraph(days, series)
}

func statsReactionLabel(reaction domain.MessageReaction) string {
	if reaction.Type == domain.MessageReactionCustomEmoji {
		return fmt.Sprintf("Custom emoji %d", reaction.DocumentID)
	}
	if reaction.Emoticon != "" {
		return reaction.Emoticon
	}
	return "Reaction"
}

func (r *Router) statsSnapshotGraph(key, label string, date, value int) tg.StatsGraphClass {
	now := int(r.clock.Now().Unix())
	if date <= 0 || date >= now {
		date = now - 1
	}
	days := []domain.ChannelStatsDay{{Date: date}, {Date: now, Views: value, Reactions: value}}
	return r.statsGraph(days, []statsGraphSeries{{
		Key: key, Label: label, Color: statsGraphColors[0],
		Value: func(day domain.ChannelStatsDay) int {
			if key == "views" {
				return day.Views
			}
			return day.Reactions
		},
	}})
}

func (r *Router) statsReactionSnapshotGraph(date int, counts []domain.ChannelMessageReactionCount) tg.StatsGraphClass {
	now := int(r.clock.Now().Unix())
	if date <= 0 || date >= now {
		date = now - 1
	}
	end := domain.ChannelStatsDay{Date: now}
	for _, item := range counts {
		end.Reactions += item.Count
		end.ByReaction = append(end.ByReaction, domain.StatsReactionCount{Reaction: item.Reaction, Count: item.Count})
	}
	return r.statsReactionGraph([]domain.ChannelStatsDay{{Date: date}, end})
}

func statsGraphUnavailable() tg.StatsGraphClass {
	return &tg.StatsGraphError{Error: "GRAPH_NOT_AVAILABLE"}
}

func statsServiceErr(err error) error {
	switch {
	case errors.Is(err, domain.ErrMessageIDInvalid):
		return messageIDInvalidErr()
	case errors.Is(err, domain.ErrStatsOffsetInvalid):
		return tgerr400("OFFSET_INVALID")
	default:
		return channelInvalidErr(err)
	}
}

func emptyStatsPublicForwards() *tg.StatsPublicForwards {
	return &tg.StatsPublicForwards{
		Count:    0,
		Forwards: []tg.PublicForwardClass{},
		Chats:    []tg.ChatClass{},
		Users:    []tg.UserClass{},
	}
}

func (r *Router) tgStatsPublicForwards(ctx context.Context, viewerUserID int64, list domain.StoryPublicForwardList) *tg.StatsPublicForwards {
	users := r.domainUsersForIDs(ctx, viewerUserID, storyViewUserIDs(list.Forwards))
	peerUsers, peerChannels := r.storyPeerObjects(ctx, viewerUserID, storyViewPeers(list.Forwards))
	users = mergeDomainUsers(users, peerUsers...)
	out := &tg.StatsPublicForwards{
		Count:    list.Count,
		Forwards: tgPublicForwardItems(viewerUserID, list.Forwards),
		Chats:    tgChannels(viewerUserID, peerChannels),
		Users:    tgUsersForViewer(viewerUserID, users),
	}
	if list.NextOffset != "" {
		out.SetNextOffset(list.NextOffset)
	}
	r.applyPeerReadModels(ctx, viewerUserID, out.Users, out.Chats)
	return out
}

func tgPublicForwardItems(viewerUserID int64, views []domain.StoryView) []tg.PublicForwardClass {
	out := make([]tg.PublicForwardClass, 0, len(views))
	for _, view := range views {
		if view.PublicForward != nil {
			msg := tgChannelMessage(viewerUserID, view.PublicForward.Message)
			if msg == nil {
				continue
			}
			out = append(out, &tg.PublicForwardMessage{Message: msg})
			continue
		}
		if view.Repost != nil {
			peer := tgPeer(view.Repost.Owner)
			if peer == nil {
				continue
			}
			out = append(out, &tg.PublicForwardStory{
				Peer:  peer,
				Story: tgStoryItem(*view.Repost),
			})
		}
	}
	return out
}
