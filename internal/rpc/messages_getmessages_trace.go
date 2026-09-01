package rpc

import (
	"context"
	"fmt"

	"github.com/iamxvbaba/td/tg"
	"go.uber.org/zap"

	"telesrv/internal/domain"
)

type getMessagesInputTrace struct {
	inputCount         int
	inputTypes         []string
	inputIDs           []int
	lookupIDs          []int
	invalidIDs         []int
	replyToIDs         []int
	callbackMessageIDs []int
	callbackQueryIDs   []int64
	pinnedCount        int
	unsupportedTypes   []string
	duplicateLookupIDs []int
}

func newGetMessagesInputTrace(inputs []tg.InputMessageClass) getMessagesInputTrace {
	trace := getMessagesInputTrace{
		inputCount: len(inputs),
		inputTypes: make([]string, 0, len(inputs)),
		inputIDs:   make([]int, 0, len(inputs)),
		lookupIDs:  make([]int, 0, len(inputs)),
	}
	seenLookup := make(map[int]int, len(inputs))
	duplicateSeen := make(map[int]struct{})
	for _, input := range inputs {
		switch msg := input.(type) {
		case *tg.InputMessageID:
			trace.recordInputID("inputMessageID", msg.ID)
			if validMessageBoxID(msg.ID) {
				trace.lookupIDs = append(trace.lookupIDs, msg.ID)
				seenLookup[msg.ID]++
				if seenLookup[msg.ID] == 2 {
					trace.duplicateLookupIDs = append(trace.duplicateLookupIDs, msg.ID)
					duplicateSeen[msg.ID] = struct{}{}
				} else if seenLookup[msg.ID] > 2 {
					if _, ok := duplicateSeen[msg.ID]; !ok {
						trace.duplicateLookupIDs = append(trace.duplicateLookupIDs, msg.ID)
						duplicateSeen[msg.ID] = struct{}{}
					}
				}
			} else {
				trace.invalidIDs = append(trace.invalidIDs, msg.ID)
			}
		case *tg.InputMessageReplyTo:
			trace.recordInputID("inputMessageReplyTo", msg.ID)
			trace.replyToIDs = append(trace.replyToIDs, msg.ID)
			trace.unsupportedTypes = append(trace.unsupportedTypes, "inputMessageReplyTo")
			if !validMessageBoxID(msg.ID) {
				trace.invalidIDs = append(trace.invalidIDs, msg.ID)
			}
		case *tg.InputMessagePinned:
			trace.inputTypes = append(trace.inputTypes, "inputMessagePinned")
			trace.pinnedCount++
			trace.unsupportedTypes = append(trace.unsupportedTypes, "inputMessagePinned")
		case *tg.InputMessageCallbackQuery:
			trace.recordInputID("inputMessageCallbackQuery", msg.ID)
			trace.callbackMessageIDs = append(trace.callbackMessageIDs, msg.ID)
			trace.callbackQueryIDs = append(trace.callbackQueryIDs, msg.QueryID)
			trace.unsupportedTypes = append(trace.unsupportedTypes, "inputMessageCallbackQuery")
			if !validMessageBoxID(msg.ID) {
				trace.invalidIDs = append(trace.invalidIDs, msg.ID)
			}
		case nil:
			trace.inputTypes = append(trace.inputTypes, "nil")
			trace.unsupportedTypes = append(trace.unsupportedTypes, "nil")
		default:
			name := fmt.Sprintf("%T", input)
			trace.inputTypes = append(trace.inputTypes, name)
			trace.unsupportedTypes = append(trace.unsupportedTypes, name)
		}
	}
	return trace
}

func (t *getMessagesInputTrace) recordInputID(inputType string, id int) {
	t.inputTypes = append(t.inputTypes, inputType)
	t.inputIDs = append(t.inputIDs, id)
}

func validMessageBoxID(id int) bool {
	return id > 0 && id <= domain.MaxMessageBoxID
}

func (t getMessagesInputTrace) zapFields() []zap.Field {
	fields := []zap.Field{
		zap.Int("input_count", t.inputCount),
		zap.Strings("input_types", t.inputTypes),
		zap.Ints("input_ids", t.inputIDs),
		zap.Ints("lookup_ids", t.lookupIDs),
	}
	if len(t.invalidIDs) > 0 {
		fields = append(fields, zap.Ints("invalid_ids", t.invalidIDs))
	}
	if len(t.replyToIDs) > 0 {
		fields = append(fields, zap.Ints("reply_to_ids", t.replyToIDs))
	}
	if t.pinnedCount > 0 {
		fields = append(fields, zap.Int("pinned_inputs", t.pinnedCount))
	}
	if len(t.callbackMessageIDs) > 0 {
		fields = append(fields,
			zap.Ints("callback_message_ids", t.callbackMessageIDs),
			zap.Int64s("callback_query_ids", t.callbackQueryIDs),
		)
	}
	if len(t.unsupportedTypes) > 0 {
		fields = append(fields, zap.Strings("unsupported_input_types", t.unsupportedTypes))
	}
	if len(t.duplicateLookupIDs) > 0 {
		fields = append(fields, zap.Ints("duplicate_lookup_ids", t.duplicateLookupIDs))
	}
	return fields
}

func (t getMessagesInputTrace) missingLookupIDs(found map[int]struct{}) []int {
	if len(t.lookupIDs) == 0 {
		return nil
	}
	missing := make([]int, 0)
	seenMissing := make(map[int]struct{})
	for _, id := range t.lookupIDs {
		if _, ok := found[id]; ok {
			continue
		}
		if _, ok := seenMissing[id]; ok {
			continue
		}
		missing = append(missing, id)
		seenMissing[id] = struct{}{}
	}
	return missing
}

func (r *Router) logPrivateGetMessagesTrace(ctx context.Context, trace getMessagesInputTrace, found []domain.Message, result *tg.MessagesMessages) {
	if r == nil || r.log == nil || result == nil {
		return
	}
	foundIDs := make([]int, 0, len(found))
	foundSet := make(map[int]struct{}, len(found))
	peers := make([]string, 0, len(found))
	for _, msg := range found {
		foundIDs = append(foundIDs, msg.ID)
		foundSet[msg.ID] = struct{}{}
		peers = append(peers, fmt.Sprintf("id=%d peer=%s:%d from=%s:%d out=%t uid=%d pts=%d",
			msg.ID, msg.Peer.Type, msg.Peer.ID, msg.From.Type, msg.From.ID, msg.Out, msg.UID, msg.Pts))
	}
	fields := append([]zap.Field{zap.String("method", "messages.getMessages")}, r.contextLogFields(ctx)...)
	fields = append(fields, trace.zapFields()...)
	fields = append(fields,
		zap.Ints("found_ids", foundIDs),
		zap.Ints("missing_lookup_ids", trace.missingLookupIDs(foundSet)),
		zap.Strings("found_peers", peers),
		zap.Int("result_messages", len(result.Messages)),
		zap.Int("result_users", len(result.Users)),
		zap.Int("result_chats", len(result.Chats)),
	)
	r.log.Info("messages.getMessages detail", fields...)
}

func (r *Router) logChannelGetMessagesTrace(ctx context.Context, channelID int64, trace getMessagesInputTrace, found []domain.ChannelMessage, result *tg.MessagesMessages) {
	if r == nil || r.log == nil {
		return
	}
	foundIDs := make([]int, 0, len(found))
	foundSet := make(map[int]struct{}, len(found))
	peers := make([]string, 0, len(found))
	for _, msg := range found {
		foundIDs = append(foundIDs, msg.ID)
		foundSet[msg.ID] = struct{}{}
		peers = append(peers, fmt.Sprintf("id=%d channel=%d from=%s:%d sender_user_id=%d post=%t pts=%d",
			msg.ID, msg.ChannelID, msg.From.Type, msg.From.ID, msg.SenderUserID, msg.Post, msg.Pts))
	}
	resultMessages, resultUsers, resultChats := 0, 0, 0
	if result != nil {
		resultMessages = len(result.Messages)
		resultUsers = len(result.Users)
		resultChats = len(result.Chats)
	}
	fields := append([]zap.Field{
		zap.String("method", "channels.getMessages"),
		zap.Int64("channel_id", channelID),
	}, r.contextLogFields(ctx)...)
	fields = append(fields, trace.zapFields()...)
	fields = append(fields,
		zap.Ints("found_ids", foundIDs),
		zap.Ints("missing_lookup_ids", trace.missingLookupIDs(foundSet)),
		zap.Strings("found_peers", peers),
		zap.Int("result_messages", resultMessages),
		zap.Int("result_users", resultUsers),
		zap.Int("result_chats", resultChats),
	)
	r.log.Info("channels.getMessages detail", fields...)
}
