package privacy

import (
	"context"
	"fmt"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

// CanSeeForViewerUserIDs evaluates only the requested viewer->owner pairs.
// contactsByOwner must contain the inverse owner->viewer contact rows prefetched
// by the caller; accepting them here lets user projection share one sparse
// contact read for contact overlays, personal photos, and privacy relations.
func (s *Service) CanSeeForViewerUserIDs(
	ctx context.Context,
	ownerUserIDsByViewer map[int64][]int64,
	keys []domain.PrivacyKey,
	contactsByOwner map[int64]map[int64]domain.Contact,
) (map[int64]map[int64]map[domain.PrivacyKey]bool, error) {
	out := make(map[int64]map[int64]map[domain.PrivacyKey]bool)
	if len(ownerUserIDsByViewer) == 0 || len(keys) == 0 {
		return out, nil
	}
	for _, key := range keys {
		if !ValidKey(key) {
			return nil, domain.ErrPrivacyKeyInvalid
		}
	}
	viewersByOwner := make(map[int64][]int64)
	viewerSet := make(map[int64]struct{})
	for viewerID, ownerIDs := range ownerUserIDsByViewer {
		if viewerID == 0 {
			continue
		}
		seenOwners := make(map[int64]struct{}, len(ownerIDs))
		for _, ownerID := range ownerIDs {
			if ownerID == 0 {
				continue
			}
			if _, ok := seenOwners[ownerID]; ok {
				continue
			}
			seenOwners[ownerID] = struct{}{}
			viewersByOwner[ownerID] = append(viewersByOwner[ownerID], viewerID)
			viewerSet[viewerID] = struct{}{}
		}
	}
	if len(viewersByOwner) == 0 {
		return out, nil
	}
	if contactsByOwner == nil && s != nil && s.contacts != nil {
		loader, ok := s.contacts.(store.SparseContactProjectionStore)
		if !ok {
			return nil, fmt.Errorf("privacy contact store does not support sparse projection")
		}
		batch, err := loader.ContactProjectionForViewerUserIDs(ctx, viewersByOwner)
		if err != nil {
			return nil, err
		}
		contactsByOwner = batch.Contacts
	}
	owners := make([]int64, 0, len(viewersByOwner))
	for ownerID := range viewersByOwner {
		owners = append(owners, ownerID)
	}
	rulesByOwner := make(map[int64]map[domain.PrivacyKey]domain.PrivacyRules, len(owners))
	if s != nil && s.rules != nil {
		list, err := s.rules.ListPrivacyRules(ctx, owners, keys)
		if err != nil {
			return nil, err
		}
		for _, rules := range list {
			if !ValidKey(rules.Key) {
				continue
			}
			if len(rules.Rules) == 0 {
				rules.Rules = domain.DefaultPrivacyRules(rules.Key)
			}
			if rulesByOwner[rules.OwnerUserID] == nil {
				rulesByOwner[rules.OwnerUserID] = make(map[domain.PrivacyKey]domain.PrivacyRules, len(keys))
			}
			rulesByOwner[rules.OwnerUserID][rules.Key] = cloneRules(rules)
		}
	}

	needsByOwner := make(map[int64]evaluationNeeds, len(owners))
	needsViewerFacts := false
	membershipPairCount := 0
	for _, ownerID := range owners {
		var needs evaluationNeeds
		for _, key := range keys {
			rules, ok := rulesByOwner[ownerID][key]
			if !ok {
				rules = defaultRules(ownerID, key)
			}
			mergeNeeds(&needs, needsForRules(rules))
		}
		needsByOwner[ownerID] = needs
		needsViewerFacts = needsViewerFacts || needs.viewerBase
		viewerCount := len(viewersByOwner[ownerID])
		if !activeChannelMembershipPairsAllowed(membershipPairCount, len(needs.chatIDs), viewerCount) {
			return nil, activeChannelMembershipPairLimitError()
		}
		membershipPairCount += len(needs.chatIDs) * viewerCount
	}
	membershipKeys := make([]membershipKey, 0, membershipPairCount)
	for _, ownerID := range owners {
		needs := needsByOwner[ownerID]
		for _, chatID := range needs.chatIDs {
			for _, viewerID := range viewersByOwner[ownerID] {
				membershipKeys = append(membershipKeys, membershipKey{ChatID: chatID, UserID: viewerID})
			}
		}
	}
	viewers := make([]int64, 0, len(viewerSet))
	for viewerID := range viewerSet {
		viewers = append(viewers, viewerID)
	}
	var baseFacts map[int64]viewerFacts
	if needsViewerFacts {
		var err error
		baseFacts, err = s.loadViewerFacts(ctx, viewers)
		if err != nil {
			return nil, err
		}
	}
	membershipFacts, err := s.loadMembershipFactsForKeys(ctx, membershipKeys)
	if err != nil {
		return nil, err
	}
	now := s.now().Unix()
	for _, ownerID := range owners {
		perViewer := make(map[int64]map[domain.PrivacyKey]bool, len(viewersByOwner[ownerID]))
		for _, viewerID := range viewersByOwner[ownerID] {
			visibility := make(map[domain.PrivacyKey]bool, len(keys))
			if ownerID == viewerID {
				for _, key := range keys {
					visibility[key] = true
				}
				perViewer[viewerID] = visibility
				continue
			}
			contact, isContact := contactsByOwner[ownerID][viewerID]
			for _, key := range keys {
				rules, ok := rulesByOwner[ownerID][key]
				if !ok {
					rules = defaultRules(ownerID, key)
				}
				evalCtx := domain.PrivacyContext{
					OwnerUserID: ownerID, ViewerUserID: viewerID,
					ViewerIsContact: isContact, ViewerCloseFriend: isContact && contact.CloseFriend,
				}
				applyViewerFacts(&evalCtx, baseFacts[viewerID], now)
				applyMembershipFacts(&evalCtx, needsByOwner[ownerID].chatIDs, membershipFacts)
				visibility[key] = Evaluate(rules, evalCtx)
			}
			perViewer[viewerID] = visibility
		}
		out[ownerID] = perViewer
	}
	return out, nil
}

func (s *Service) loadMembershipFactsForKeys(ctx context.Context, input []membershipKey) (map[membershipKey]bool, error) {
	capacity := len(input)
	if capacity > store.MaxActiveChannelMemberPairs {
		capacity = store.MaxActiveChannelMemberPairs
	}
	seen := make(map[membershipKey]struct{}, capacity)
	keys := make([]membershipKey, 0, capacity)
	for _, key := range input {
		if key.ChatID == 0 || key.UserID == 0 {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		if len(keys) >= store.MaxActiveChannelMemberPairs {
			return nil, activeChannelMembershipPairLimitError()
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return map[membershipKey]bool{}, nil
	}
	loadMissing := func(ctx context.Context, missing []membershipKey) (map[membershipKey]bool, error) {
		out := make(map[membershipKey]bool, len(missing))
		byChat := make(map[int64][]int64)
		for _, key := range missing {
			out[key] = false
			byChat[key.ChatID] = append(byChat[key.ChatID], key.UserID)
		}
		if s == nil || s.memberships == nil {
			return out, nil
		}
		activeByChat, err := s.memberships.FilterActiveChannelMemberPairs(ctx, byChat)
		if err != nil {
			return nil, err
		}
		for chatID, userIDs := range activeByChat {
			for _, userID := range userIDs {
				key := membershipKey{ChatID: chatID, UserID: userID}
				if _, requested := out[key]; requested {
					out[key] = true
				}
			}
		}
		return out, nil
	}
	if s == nil || s.membershipFacts == nil {
		return loadMissing(ctx, keys)
	}
	if len(keys) > privacyMembershipBatchAdmissionMaxPairs {
		for {
			loadEpoch := s.membershipFacts.LoadEpoch()
			loaded, err := loadMissing(ctx, keys)
			if err != nil {
				return nil, err
			}
			if s.membershipFacts.LoadEpoch() == loadEpoch {
				return loaded, nil
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
	}
	return s.membershipFacts.GetOrLoadBatch(ctx, keys,
		func(membershipKey) (int64, bool) { return 0, true }, loadMissing)
}
