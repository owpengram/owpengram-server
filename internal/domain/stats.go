package domain

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	// MaxChannelStatsDays bounds every aggregate query independently of the
	// client request.  The RPC currently requests seven days, while keeping a
	// small domain ceiling makes future callers safe by construction.
	MaxChannelStatsDays = 31
	// MaxChannelStatsTopPosters bounds the user hydration work at the RPC edge.
	MaxChannelStatsTopPosters = 10
	// MaxChannelStatsRecentPosts bounds correlated interaction aggregation.
	MaxChannelStatsRecentPosts = 10
	// MaxChannelMessagePublicForwards is Telegram's public-forwards page cap.
	MaxChannelMessagePublicForwards = 100
)

var ErrStatsOffsetInvalid = errors.New("stats offset invalid")

// StatsPeriod is a half-open Unix-second range [MinDate, MaxDate).  Previous
// values always use the equally sized range immediately before MinDate.
type StatsPeriod struct {
	MinDate int
	MaxDate int
}

func (p StatsPeriod) Valid() bool {
	return p.MinDate > 0 && p.MaxDate > p.MinDate &&
		p.MaxDate-p.MinDate <= MaxChannelStatsDays*86400
}

func (p StatsPeriod) PreviousMinDate() int {
	return p.MinDate - (p.MaxDate - p.MinDate)
}

// StatsValueAndPrev is one current-period value and its previous-period peer.
type StatsValueAndPrev struct {
	Current  float64
	Previous float64
}

// StatsReactionCount is one protocol-neutral reaction series value.
type StatsReactionCount struct {
	Reaction MessageReaction
	Count    int
}

// ChannelStatsDay is a UTC-day bucket. Date is the bucket start.
type ChannelStatsDay struct {
	Date       int
	Members    int
	NewMembers int
	Messages   int
	Viewers    int
	Posters    int
	Views      int
	Shares     int
	Reactions  int
	ByReaction []StatsReactionCount
}

type ChannelStatsTopPoster struct {
	UserID   int64
	Messages int
	AvgChars int
}

type ChannelStatsRecentPost struct {
	MessageID int
	Views     int
	Forwards  int
	Reactions int
}

// ChannelStats is the durable minimum needed by broadcast/megagroup stats.
// Unsupported Telegram dimensions (language, notification mute, IV sources)
// are deliberately absent so the RPC edge can return statsGraphError instead
// of manufacturing zero-valued facts.
type ChannelStats struct {
	Channel          Channel
	Period           StatsPeriod
	Members          StatsValueAndPrev
	Messages         StatsValueAndPrev
	Viewers          StatsValueAndPrev
	Posters          StatsValueAndPrev
	ViewsPerPost     StatsValueAndPrev
	SharesPerPost    StatsValueAndPrev
	ReactionsPerPost StatsValueAndPrev
	Days             []ChannelStatsDay
	TopPosters       []ChannelStatsTopPoster
	RecentPosts      []ChannelStatsRecentPost
}

type ChannelStatsRequest struct {
	ViewerUserID int64
	ChannelID    int64
	Period       StatsPeriod
}

// ChannelMessageStats contains event-time view and reaction buckets for one
// existing channel message.
type ChannelMessageStats struct {
	Channel Channel
	Message ChannelMessage
	Period  StatsPeriod
	Days    []ChannelStatsDay
}

type ChannelMessageStatsRequest struct {
	ViewerUserID int64
	ChannelID    int64
	MessageID    int
	Period       StatsPeriod
}

// ChannelMessagePublicForwardListRequest pages public channel/supergroup
// messages whose forward header identifies one exact source channel post.
type ChannelMessagePublicForwardListRequest struct {
	ViewerUserID int64
	ChannelID    int64
	MessageID    int
	Offset       string
	Limit        int
}

type ChannelMessagePublicForwardList struct {
	Count      int
	Messages   []ChannelMessage
	NextOffset string
}

// ChannelMessagePublicForwardCursor is ordered by date DESC, destination
// channel ASC, message id DESC.  A versioned textual form is intentionally
// opaque to clients while staying easy to validate and log.
type ChannelMessagePublicForwardCursor struct {
	Date      int
	ChannelID int64
	MessageID int
}

func ParseChannelMessagePublicForwardCursor(offset string) (ChannelMessagePublicForwardCursor, error) {
	if offset == "" {
		return ChannelMessagePublicForwardCursor{}, nil
	}
	parts := strings.Split(offset, ":")
	if len(parts) != 4 || parts[0] != "cmf1" {
		return ChannelMessagePublicForwardCursor{}, ErrStatsOffsetInvalid
	}
	date, err1 := strconv.Atoi(parts[1])
	channelID, err2 := strconv.ParseInt(parts[2], 10, 64)
	messageID, err3 := strconv.Atoi(parts[3])
	if err1 != nil || err2 != nil || err3 != nil || date <= 0 || channelID <= 0 ||
		messageID <= 0 || messageID > MaxMessageBoxID {
		return ChannelMessagePublicForwardCursor{}, ErrStatsOffsetInvalid
	}
	return ChannelMessagePublicForwardCursor{Date: date, ChannelID: channelID, MessageID: messageID}, nil
}

func FormatChannelMessagePublicForwardCursor(message ChannelMessage) string {
	if message.Date <= 0 || message.ChannelID <= 0 || message.ID <= 0 {
		return ""
	}
	return fmt.Sprintf("cmf1:%d:%d:%d", message.Date, message.ChannelID, message.ID)
}
