package rpc

import (
	"context"
	"errors"
	"unicode/utf8"

	"github.com/iamxvbaba/td/tg"
	"github.com/iamxvbaba/td/tgerr"

	"telesrv/internal/domain"
)

func (r *Router) onEphemeralSendWelcomeMessage(ctx context.Context, request *tg.EphemeralSendMessageRequest) (tg.UpdatesClass, error) {
	if request == nil {
		return nil, inputRequestInvalidErr()
	}
	_, hasQueryID := request.GetQueryID()
	_, hasReplyTo := request.GetReplyTo()
	if !request.Welcome || r.deps.WelcomeMessages == nil || request.Peer == nil ||
		request.Anchor || hasReplyTo || hasQueryID || request.RandomID == 0 ||
		!welcomeReceiverEmpty(request.ReceiverID) {
		return nil, inputRequestInvalidErr()
	}
	userID, peer, err := r.welcomeActorAndPeer(ctx, request.Peer)
	if err != nil {
		return nil, err
	}
	if err := r.deps.WelcomeMessages.Authorize(ctx, userID, peer); err != nil {
		return nil, welcomeMessageRPCError(err)
	}
	content, err := r.domainWelcomeSendContent(ctx, userID, request)
	if err != nil {
		return nil, err
	}
	message, _, err := r.deps.WelcomeMessages.Create(ctx, userID, peer, request.RandomID, content)
	if err != nil {
		return nil, welcomeMessageRPCError(err)
	}
	return r.welcomeMessageUpdates(ctx, userID, message, false)
}

func (r *Router) onEphemeralEditWelcomeMessage(ctx context.Context, request *tg.EphemeralEditMessageRequest) (tg.UpdatesClass, error) {
	if request == nil || !request.Welcome || r.deps.WelcomeMessages == nil || request.Peer == nil ||
		request.ID <= 0 || request.ID > domain.MaxMessageBoxID || !welcomeReceiverEmpty(request.ReceiverID) {
		return nil, inputRequestInvalidErr()
	}
	userID, peer, err := r.welcomeActorAndPeer(ctx, request.Peer)
	if err != nil {
		return nil, err
	}
	if err := r.deps.WelcomeMessages.Authorize(ctx, userID, peer); err != nil {
		return nil, welcomeMessageRPCError(err)
	}
	fields, err := r.domainWelcomeEditFields(ctx, userID, request)
	if err != nil {
		return nil, err
	}
	message, err := r.deps.WelcomeMessages.Edit(ctx, userID, peer, request.ID, fields)
	if err != nil {
		return nil, welcomeMessageRPCError(err)
	}
	return r.welcomeMessageUpdates(ctx, userID, message, true)
}

func (r *Router) onEphemeralDeleteWelcomeMessage(ctx context.Context, request *tg.EphemeralDeleteWelcomeMessageRequest) (bool, error) {
	if request == nil || r.deps.WelcomeMessages == nil || request.Peer == nil ||
		request.ID <= 0 || request.ID > domain.MaxMessageBoxID {
		return false, messageIDInvalidErr()
	}
	userID, peer, err := r.welcomeActorAndPeer(ctx, request.Peer)
	if err != nil {
		return false, err
	}
	ok, err := r.deps.WelcomeMessages.Delete(ctx, userID, peer, request.ID)
	if err != nil {
		return false, welcomeMessageRPCError(err)
	}
	return ok, nil
}

func (r *Router) onEphemeralDeleteAllWelcomeMessages(ctx context.Context, request *tg.EphemeralDeleteAllWelcomeMessagesRequest) (bool, error) {
	if request == nil || r.deps.WelcomeMessages == nil || request.Peer == nil {
		return false, inputRequestInvalidErr()
	}
	userID, peer, err := r.welcomeActorAndPeer(ctx, request.Peer)
	if err != nil {
		return false, err
	}
	ok, err := r.deps.WelcomeMessages.DeleteAll(ctx, userID, peer)
	if err != nil {
		return false, welcomeMessageRPCError(err)
	}
	return ok, nil
}

func (r *Router) onEphemeralGetWelcomeMessages(ctx context.Context, request *tg.EphemeralGetWelcomeMessagesRequest) (tg.EphemeralWelcomeMessagesClass, error) {
	if request == nil || r.deps.WelcomeMessages == nil || request.Peer == nil || request.Hash < 0 {
		return nil, inputRequestInvalidErr()
	}
	userID, peer, err := r.welcomeActorAndPeer(ctx, request.Peer)
	if err != nil {
		return nil, err
	}
	result, err := r.deps.WelcomeMessages.List(ctx, userID, peer, request.Hash)
	if err != nil {
		return nil, welcomeMessageRPCError(err)
	}
	if result.NotModified {
		return &tg.EphemeralWelcomeMessagesNotModified{}, nil
	}
	messages := make([]tg.EphemeralMessage, 0, len(result.Messages))
	for _, message := range result.Messages {
		wire, err := tgWelcomeMessage(message)
		if err != nil {
			return nil, internalErr()
		}
		messages = append(messages, wire)
	}
	return &tg.EphemeralWelcomeMessages{Hash: result.Hash, Messages: messages}, nil
}

func (r *Router) welcomeActorAndPeer(ctx context.Context, input tg.InputPeerClass) (int64, domain.Peer, error) {
	userID, _, err := r.currentUserID(ctx)
	if err != nil || userID <= 0 {
		return 0, domain.Peer{}, internalErr()
	}
	peer, err := r.checkedDomainPeerFromInputPeer(ctx, userID, input)
	if err != nil {
		return 0, domain.Peer{}, err
	}
	if peer.Type != domain.PeerTypeChannel || peer.ID <= 0 {
		return 0, domain.Peer{}, peerIDInvalidErr()
	}
	return userID, peer, nil
}

func welcomeReceiverEmpty(receiver tg.InputUserClass) bool {
	_, ok := receiver.(*tg.InputUserEmpty)
	return ok
}

func (r *Router) domainWelcomeSendContent(ctx context.Context, userID int64, request *tg.EphemeralSendMessageRequest) (domain.WelcomeMessageContent, error) {
	entities, err := welcomeEntities(userID, request.Message, request.Entities)
	if err != nil {
		return domain.WelcomeMessageContent{}, err
	}
	media, err := r.welcomeInputMedia(ctx, userID, request.Media)
	if err != nil {
		return domain.WelcomeMessageContent{}, err
	}
	markup, err := welcomeReplyMarkup(request.ReplyMarkup)
	if err != nil {
		return domain.WelcomeMessageContent{}, err
	}
	rich, err := r.domainRichMessageFromInput(ctx, request.RichMessage)
	if err != nil {
		return domain.WelcomeMessageContent{}, err
	}
	content := domain.WelcomeMessageContent{
		Message: request.Message, Entities: entities, Media: media, ReplyMarkup: markup,
		RichMessage: rich, InvertMedia: request.InvertMedia, NoForwards: request.Noforwards,
	}
	if err := content.Validate(); err != nil {
		if request.Message == "" && media == nil && rich.IsZero() {
			return domain.WelcomeMessageContent{}, messageEmptyErr()
		}
		return domain.WelcomeMessageContent{}, inputRequestInvalidErr()
	}
	return content, nil
}

func (r *Router) domainWelcomeEditFields(ctx context.Context, userID int64, request *tg.EphemeralEditMessageRequest) (domain.WelcomeMessageEditFields, error) {
	var fields domain.WelcomeMessageEditFields
	if message, ok := request.GetMessage(); ok {
		if !utf8.ValidString(message) || utf8.RuneCountInString(message) > domain.MaxMessageTextLength {
			return fields, messageTooLongErr()
		}
		fields.SetMessage = true
		fields.Message = message
		// TDesktop omits f_entities for an empty vector; a text edit therefore
		// replaces, rather than accidentally retains, the old entity vector.
		fields.SetEntities = true
		fields.Entities = nil
	}
	if entities, ok := request.GetEntities(); ok {
		text := request.Message
		if !fields.SetMessage {
			text = ""
		}
		converted := domainMessageEntitiesForViewer(userID, entities)
		if len(converted) != len(entities) || (fields.SetMessage && !validEphemeralEntityBounds(text, converted)) {
			return fields, entityBoundsInvalidErr()
		}
		fields.SetEntities = true
		fields.Entities = converted
	}
	if media, ok := request.GetMedia(); ok {
		resolved, err := r.welcomeInputMedia(ctx, userID, media)
		if err != nil {
			return fields, err
		}
		fields.SetMedia = true
		fields.Media = resolved
		fields.SetInvertMedia = true
		fields.InvertMedia = request.InvertMedia
	} else if request.InvertMedia {
		fields.SetInvertMedia = true
		fields.InvertMedia = true
	}
	if markup, ok := request.GetReplyMarkup(); ok {
		converted, err := welcomeReplyMarkup(markup)
		if err != nil {
			return fields, err
		}
		fields.SetReplyMarkup = true
		fields.ReplyMarkup = converted
	}
	if rich, ok := request.GetRichMessage(); ok {
		converted, err := r.domainRichMessageFromInput(ctx, rich)
		if err != nil {
			return fields, err
		}
		fields.SetRichMessage = true
		fields.RichMessage = converted
	}
	if fields.Empty() {
		return fields, inputRequestInvalidErr()
	}
	return fields, nil
}

func welcomeEntities(userID int64, text string, input []tg.MessageEntityClass) ([]domain.MessageEntity, error) {
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > domain.MaxMessageTextLength || len(input) > domain.MaxMessageEntityCount {
		return nil, messageTooLongErr()
	}
	entities := domainMessageEntitiesForViewer(userID, input)
	if len(entities) != len(input) || !validEphemeralEntityBounds(text, entities) {
		return nil, entityBoundsInvalidErr()
	}
	return entities, nil
}

func (r *Router) welcomeInputMedia(ctx context.Context, userID int64, input tg.InputMediaClass) (*domain.MessageMedia, error) {
	if input == nil {
		return nil, nil
	}
	media, err := r.resolveInputMedia(ctx, userID, input)
	if err != nil {
		return nil, err
	}
	if media != nil && !ephemeralMediaAllowed(media) {
		return nil, mediaTypeInvalidErr()
	}
	return media, nil
}

func welcomeReplyMarkup(input tg.ReplyMarkupClass) (*domain.MessageReplyMarkup, error) {
	if input == nil {
		return nil, nil
	}
	markup, err := domainReplyMarkupForSender(input, true)
	if err != nil {
		return nil, replyMarkupErr(err)
	}
	return markup, nil
}

func (r *Router) welcomeMessageUpdates(ctx context.Context, viewerUserID int64, message domain.WelcomeMessage, edited bool) (*tg.Updates, error) {
	if r.deps.Users == nil || r.deps.Channels == nil {
		return nil, internalErr()
	}
	users, err := r.deps.Users.ByIDs(ctx, viewerUserID, []int64{message.CreatorUserID})
	if err != nil {
		return nil, internalErr()
	}
	view, err := r.deps.Channels.ResolveChannel(ctx, viewerUserID, message.Peer.ID)
	if err != nil {
		return nil, channelInvalidErr(err)
	}
	wire, err := tgWelcomeMessage(message)
	if err != nil {
		return nil, internalErr()
	}
	var update tg.UpdateClass = &tg.UpdateNewEphemeralMessage{Message: wire}
	if edited {
		update = &tg.UpdateEditEphemeralMessage{Message: wire}
	}
	date := message.Date
	if edited && message.EditDate > 0 {
		date = message.EditDate
	}
	return &tg.Updates{
		Updates: []tg.UpdateClass{update},
		Users:   tgUsersForViewer(viewerUserID, users),
		Chats:   []tg.ChatClass{tgChannelChatForView(viewerUserID, view)},
		Date:    date,
		Seq:     0,
	}, nil
}

func tgWelcomeMessage(message domain.WelcomeMessage) (tg.EphemeralMessage, error) {
	out := tg.EphemeralMessage{
		Out:             true,
		WelcomeTemplate: true,
		InvertMedia:     message.Content.InvertMedia,
		Noforwards:      message.Content.NoForwards,
		ID:              message.ID,
		FromID:          &tg.PeerUser{UserID: message.CreatorUserID},
		PeerID:          tgPeer(message.Peer),
		ReceiverID:      0,
		Date:            message.Date,
		Message:         message.Content.Message,
	}
	if len(message.Content.Entities) != 0 {
		out.SetEntities(tgMessageEntities(message.Content.Entities))
	}
	if message.Content.Media != nil && !message.Content.Media.IsZero() {
		out.SetMedia(tgMessageMedia(message.Content.Media))
	}
	if message.Content.ReplyMarkup != nil && !message.Content.ReplyMarkup.IsZero() {
		out.SetReplyMarkup(tgReplyMarkup(message.Content.ReplyMarkup))
	}
	rich, err := tgRichMessage(message.Content.RichMessage)
	if err != nil {
		return tg.EphemeralMessage{}, err
	}
	if rich != nil {
		out.SetRichMessage(*rich)
	}
	return out, nil
}

func welcomeMessageRPCError(err error) error {
	switch {
	case errors.Is(err, domain.ErrWelcomeMessageForbidden):
		return tgerr.New(400, "CHAT_ADMIN_REQUIRED")
	case errors.Is(err, domain.ErrWelcomeMessagePeerInvalid):
		return peerIDInvalidErr()
	case errors.Is(err, domain.ErrWelcomeMessageNotFound):
		return messageIDInvalidErr()
	case errors.Is(err, domain.ErrWelcomeMessageNotModified):
		return messageNotModifiedErr()
	case errors.Is(err, domain.ErrWelcomeMessageLimit):
		return limitInvalidErr()
	case errors.Is(err, domain.ErrWelcomeMessageInvalid),
		errors.Is(err, domain.ErrWelcomeMessageRandomIDConflict):
		return inputRequestInvalidErr()
	case errors.Is(err, domain.ErrUserFrozen),
		errors.Is(err, domain.ErrChannelInvalid),
		errors.Is(err, domain.ErrChannelPrivate),
		errors.Is(err, domain.ErrChannelUserBanned),
		errors.Is(err, domain.ErrChannelAdminRequired),
		errors.Is(err, domain.ErrChannelMonoforumUnsupported):
		return channelInvalidErr(err)
	default:
		return internalErr()
	}
}
