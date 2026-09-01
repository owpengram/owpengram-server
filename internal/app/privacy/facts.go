package privacy

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/readmodelcache"
	"telesrv/internal/store"
)

const (
	defaultPrivacyViewerFactsTTL = 10 * time.Minute
	defaultPrivacyMembershipTTL  = 24 * time.Hour

	privacyViewerFactsMaxEntries = 8192
	privacyMembershipMaxEntries  = 65536
	// A single legal cold batch must not replace the complete long-lived pair
	// cache. Large projections still use one exact store batch, but bypass LRU
	// admission and return the loaded facts directly.
	privacyMembershipBatchAdmissionMaxPairs = privacyMembershipMaxEntries / 4
)

// baseUserProvider returns viewer-independent user facts through the users read
// model. Implementations must batch cold misses rather than issue one query per
// user.
type baseUserProvider interface {
	PrivacyBaseUsers(ctx context.Context, userIDs []int64) ([]domain.User, error)
}

// channelMembershipProvider is the exact-pair cold loader behind the bounded
// membership read model. Cache-admitted batches load only misses; oversized
// non-admitted batches reload once without polluting the long-lived LRU.
type channelMembershipProvider interface {
	FilterActiveChannelMemberPairs(ctx context.Context, userIDsByChannel map[int64][]int64) (map[int64][]int64, error)
}

type viewerFacts struct {
	Found        bool
	Bot          bool
	PremiumUntil int64
}

type membershipKey struct {
	ChatID int64
	UserID int64
}

type evaluationNeeds struct {
	viewerBase bool
	chatIDs    []int64
}

func newViewerFactsCache() *readmodelcache.Cache[int64, viewerFacts] {
	return readmodelcache.New[int64, viewerFacts](readmodelcache.Config[int64, viewerFacts]{
		MaxEntries: privacyViewerFactsMaxEntries,
		TTL:        defaultPrivacyViewerFactsTTL,
	})
}

func newMembershipCache() *readmodelcache.Cache[membershipKey, bool] {
	return readmodelcache.New[membershipKey, bool](readmodelcache.Config[membershipKey, bool]{
		MaxEntries: privacyMembershipMaxEntries,
		TTL:        defaultPrivacyMembershipTTL,
		KeyString: func(key membershipKey) string {
			return strconv.FormatInt(key.ChatID, 10) + ":" + strconv.FormatInt(key.UserID, 10)
		},
	})
}

func needsForRules(rules domain.PrivacyRules) evaluationNeeds {
	var needs evaluationNeeds
	seenChats := make(map[int64]struct{})
	for _, rule := range rules.Rules {
		switch rule.Kind {
		case domain.PrivacyRuleAllowPremium,
			domain.PrivacyRuleAllowBots,
			domain.PrivacyRuleDisallowBots:
			needs.viewerBase = true
		case domain.PrivacyRuleAllowChatParticipants,
			domain.PrivacyRuleDisallowChatParticipants:
			for _, chatID := range rule.ChatIDs {
				if chatID <= 0 {
					continue
				}
				if _, ok := seenChats[chatID]; ok {
					continue
				}
				seenChats[chatID] = struct{}{}
				needs.chatIDs = append(needs.chatIDs, chatID)
			}
		}
	}
	return needs
}

func mergeNeeds(dst *evaluationNeeds, src evaluationNeeds) {
	if src.viewerBase {
		dst.viewerBase = true
	}
	if len(src.chatIDs) == 0 {
		return
	}
	seen := make(map[int64]struct{}, len(dst.chatIDs)+len(src.chatIDs))
	for _, id := range dst.chatIDs {
		seen[id] = struct{}{}
	}
	for _, id := range src.chatIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		dst.chatIDs = append(dst.chatIDs, id)
	}
}

func (s *Service) loadViewerFacts(ctx context.Context, viewerUserIDs []int64) (map[int64]viewerFacts, error) {
	ids := dedupNonZero(viewerUserIDs)
	if len(ids) == 0 {
		return map[int64]viewerFacts{}, nil
	}
	loadMissing := func(ctx context.Context, missing []int64) (map[int64]viewerFacts, error) {
		out := make(map[int64]viewerFacts, len(missing))
		for _, id := range missing {
			out[id] = viewerFacts{} // negative cache: user was not found.
		}
		if s == nil || s.baseUsers == nil {
			return out, nil
		}
		users, err := s.baseUsers.PrivacyBaseUsers(ctx, missing)
		if err != nil {
			return nil, err
		}
		for _, user := range users {
			if user.ID == 0 {
				continue
			}
			out[user.ID] = viewerFacts{
				Found:        true,
				Bot:          user.Bot,
				PremiumUntil: int64(user.PremiumUntil),
			}
		}
		return out, nil
	}
	if s == nil || s.viewerFacts == nil {
		return loadMissing(ctx, ids)
	}
	return s.viewerFacts.GetOrLoadBatch(ctx, ids,
		func(int64) (int64, bool) { return 0, true },
		loadMissing,
	)
}

func (s *Service) loadMembershipFacts(ctx context.Context, chatIDs, viewerUserIDs []int64) (map[membershipKey]bool, error) {
	chats := dedupNonZero(chatIDs)
	viewers := dedupNonZero(viewerUserIDs)
	if len(chats) == 0 || len(viewers) == 0 {
		return map[membershipKey]bool{}, nil
	}
	if !activeChannelMembershipPairsAllowed(0, len(chats), len(viewers)) {
		return nil, activeChannelMembershipPairLimitError()
	}
	keys := make([]membershipKey, 0, len(chats)*len(viewers))
	for _, chatID := range chats {
		for _, viewerID := range viewers {
			keys = append(keys, membershipKey{ChatID: chatID, UserID: viewerID})
		}
	}
	return s.loadMembershipFactsForKeys(ctx, keys)
}

func activeChannelMembershipPairsAllowed(current, channelCount, userCount int) bool {
	if current < 0 || channelCount < 0 || userCount < 0 || current > store.MaxActiveChannelMemberPairs {
		return false
	}
	if channelCount == 0 || userCount == 0 {
		return true
	}
	return channelCount <= (store.MaxActiveChannelMemberPairs-current)/userCount
}

func activeChannelMembershipPairLimitError() error {
	return fmt.Errorf("%w: maximum %d", store.ErrActiveChannelMemberPairsLimit, store.MaxActiveChannelMemberPairs)
}

func applyViewerFacts(ctx *domain.PrivacyContext, facts viewerFacts, now int64) {
	if ctx == nil || !facts.Found {
		return
	}
	ctx.ViewerIsBot = facts.Bot
	ctx.ViewerIsPremium = !facts.Bot && facts.PremiumUntil > now
}

func applyMembershipFacts(ctx *domain.PrivacyContext, chatIDs []int64, facts map[membershipKey]bool) {
	if ctx == nil || len(chatIDs) == 0 {
		return
	}
	for _, chatID := range chatIDs {
		if facts[membershipKey{ChatID: chatID, UserID: ctx.ViewerUserID}] {
			ctx.SharedChatIDs = append(ctx.SharedChatIDs, chatID)
		}
	}
}

// InvalidateViewerFacts invalidates bot/premium facts after a user-base change.
func (s *Service) InvalidateViewerFacts(userIDs ...int64) {
	if s == nil || s.viewerFacts == nil {
		return
	}
	s.viewerFacts.Invalidate(dedupNonZero(userIDs)...)
}

// InvalidateMembership invalidates one membership pair after a channel-member change.
func (s *Service) InvalidateMembership(channelID, userID int64) {
	if s == nil || s.membershipFacts == nil || channelID == 0 || userID == 0 {
		return
	}
	s.membershipFacts.Invalidate(membershipKey{ChatID: channelID, UserID: userID})
}

// InvalidateChannelMemberships invalidates all cached pairs for a changed/deleted channel.
func (s *Service) InvalidateChannelMemberships(channelID int64) {
	if s == nil || s.membershipFacts == nil || channelID == 0 {
		return
	}
	s.membershipFacts.InvalidateWhere(func(key membershipKey) bool { return key.ChatID == channelID })
}

func (s *Service) flushFactCaches() {
	if s == nil {
		return
	}
	if s.viewerFacts != nil {
		s.viewerFacts.Flush()
	}
	if s.membershipFacts != nil {
		s.membershipFacts.Flush()
	}
}
