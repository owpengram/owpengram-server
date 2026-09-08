package domain

import "slices"

func EvaluatePrivacy(rules PrivacyRules, ctx PrivacyContext) bool {
	if ctx.OwnerUserID != 0 && ctx.OwnerUserID == ctx.ViewerUserID {
		return true
	}
	if len(rules.Rules) == 0 {
		rules.Rules = DefaultPrivacyRules(rules.Key)
	}
	for _, rule := range rules.Rules {
		if explicitDisallowMatches(rule, ctx) {
			return false
		}
	}
	for _, rule := range rules.Rules {
		if explicitAllowMatches(rule, ctx) {
			return true
		}
	}
	for _, rule := range rules.Rules {
		switch rule.Kind {
		case PrivacyRuleDisallowContacts:
			if ctx.ViewerIsContact {
				return false
			}
		case PrivacyRuleAllowContacts:
			if ctx.ViewerIsContact {
				return true
			}
		}
	}
	for _, rule := range rules.Rules {
		switch rule.Kind {
		case PrivacyRuleDisallowAll:
			return false
		case PrivacyRuleAllowAll:
			return true
		}
	}
	return false
}

func explicitDisallowMatches(rule PrivacyRule, ctx PrivacyContext) bool {
	switch rule.Kind {
	case PrivacyRuleDisallowUsers:
		return slices.Contains(rule.UserIDs, ctx.ViewerUserID)
	case PrivacyRuleDisallowChatParticipants:
		return intersects(rule.ChatIDs, ctx.SharedChatIDs)
	case PrivacyRuleDisallowBots:
		return ctx.ViewerIsBot
	default:
		return false
	}
}

func explicitAllowMatches(rule PrivacyRule, ctx PrivacyContext) bool {
	switch rule.Kind {
	case PrivacyRuleAllowUsers:
		return slices.Contains(rule.UserIDs, ctx.ViewerUserID)
	case PrivacyRuleAllowChatParticipants:
		return intersects(rule.ChatIDs, ctx.SharedChatIDs)
	case PrivacyRuleAllowCloseFriends:
		return ctx.ViewerCloseFriend
	case PrivacyRuleAllowPremium:
		return ctx.ViewerIsPremium
	case PrivacyRuleAllowBots:
		return ctx.ViewerIsBot
	default:
		return false
	}
}

func intersects(a, b []int64) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	set := make(map[int64]struct{}, len(a))
	for _, id := range a {
		set[id] = struct{}{}
	}
	for _, id := range b {
		if _, ok := set[id]; ok {
			return true
		}
	}
	return false
}
