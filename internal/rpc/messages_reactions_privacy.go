package rpc

import (
	"context"
	"strings"

	"github.com/iamxvbaba/td/tg"

	"telesrv/internal/domain"
)

func (r *Router) onMessagesSetDefaultReaction(ctx context.Context, reaction tg.ReactionClass) (bool, error) {
	userID, _, err := r.currentUserID(ctx)
	if err != nil {
		return false, internalErr()
	}
	parsed, err := domainMessageReactionFromTL(reaction)
	if err != nil {
		return false, err
	}
	if err := r.validateDefaultReaction(ctx, parsed); err != nil {
		return false, err
	}
	if svc, ok := r.deps.Account.(accountDefaultReactionService); ok {
		if _, err := svc.SetDefaultReaction(ctx, userID, parsed); err != nil {
			return false, internalErr()
		}
	}
	return true, nil
}

// onMessagesGetPaidReactionPrivacy answers with the account's default paid
// reaction privacy, wrapped as an updatePaidReactionPrivacy the way Telegram
// does (the method returns Updates, not the privacy itself).
//
// Implemented because an unhandled method answers 500 NOT_IMPLEMENTED, and a
// 500 means "server hiccup, try again" to MTProto clients: Web K asks for this
// on the first chat it opens and then retries it forever with backoff. An
// account with nothing stored answers the default, as Telegram does.
func (r *Router) onMessagesGetPaidReactionPrivacy(ctx context.Context) (tg.UpdatesClass, error) {
	userID, _, err := r.currentUserID(ctx)
	if err != nil {
		return nil, internalErr()
	}
	settings := domain.DefaultAccountReactionSettings()
	if svc, ok := r.deps.Account.(accountReactionSettingsReader); ok {
		stored, err := svc.GetReactionSettings(ctx, userID)
		if err != nil {
			return nil, internalErr()
		}
		settings = stored
	}
	return &tg.Updates{
		Updates: []tg.UpdateClass{&tg.UpdatePaidReactionPrivacy{
			Private: r.tgPaidReactionPrivacy(ctx, userID, settings.PaidPrivacy),
		}},
		Users: []tg.UserClass{},
		Chats: []tg.ChatClass{},
		Date:  int(r.clock.Now().Unix()),
	}, nil
}

// tgPaidReactionPrivacy converts the stored privacy. A "send as peer" choice
// whose peer can no longer be addressed falls back to the default rather than
// failing the whole call: the client only needs a usable starting value.
func (r *Router) tgPaidReactionPrivacy(ctx context.Context, userID int64, privacy domain.PaidReactionPrivacy) tg.PaidReactionPrivacyClass {
	switch privacy.Kind {
	case domain.PaidReactionPrivacyAnonymous:
		return &tg.PaidReactionPrivacyAnonymous{}
	case domain.PaidReactionPrivacyPeer:
		if privacy.Peer != nil {
			if peer := r.inputPeerForDomainPeer(ctx, userID, *privacy.Peer); peer != nil {
				return &tg.PaidReactionPrivacyPeer{Peer: peer}
			}
		}
	}
	return &tg.PaidReactionPrivacyDefault{}
}

func (r *Router) validateDefaultReaction(ctx context.Context, reaction domain.MessageReaction) error {
	if reaction.Type == domain.MessageReactionCustomEmoji {
		return nil
	}
	if reaction.Type != domain.MessageReactionEmoji {
		return reactionInvalidErr()
	}

	if r.deps.Files != nil {
		catalog, err := r.deps.Files.ListAvailableReactions(ctx)
		if err != nil {
			return internalErr()
		}
		if len(catalog) > 0 {
			for _, item := range catalog {
				if !item.Inactive && strings.TrimSpace(item.Reaction) == reaction.Emoticon {
					return nil
				}
			}
			return reactionInvalidErr()
		}
	}

	for _, item := range staticReactionCatalog() {
		if item.Key() == reaction.Key() {
			return nil
		}
	}
	return reactionInvalidErr()
}
