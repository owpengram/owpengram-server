package postgres

import (
	"context"

	"telesrv/internal/domain"
	"telesrv/internal/readmodelcache"
	"telesrv/internal/store/postgres/sqlcgen"
)

// ChannelTopMessageCache stores the viewer-independent channel_messages row
// used as a dialog's top payload. Viewer overlays (mentioned/media_unread,
// normal/paid reactions) are deliberately applied after this cache.
//
// channel_base is the dependency token: edits/deletes/new tops and any other
// mutation that can alter the visible top payload bump it. The read-model
// listener invalidates every cached key for that channel and flushes on
// reconnect, while the cache epoch prevents a pre-invalidation batch load from
// being written back afterwards.
type ChannelTopMessageCache struct {
	cache            *readmodelcache.Cache[channelMessageLookupKey, domain.ChannelMessage]
	reactionPresence *readmodelcache.Cache[channelMessageLookupKey, channelTopReactionPresence]
}

type channelTopReactionPresence struct {
	Normal bool
	Paid   bool
}

func (p channelTopReactionPresence) any() bool { return p.Normal || p.Paid }

func NewChannelTopMessageCache(max int) *ChannelTopMessageCache {
	cache := readmodelcache.New[channelMessageLookupKey, domain.ChannelMessage](readmodelcache.Config[channelMessageLookupKey, domain.ChannelMessage]{
		MaxEntries: max,
		Clone:      cloneChannelTopMessage,
	})
	if cache == nil {
		return nil
	}
	return &ChannelTopMessageCache{
		cache: cache,
		reactionPresence: readmodelcache.New[channelMessageLookupKey, channelTopReactionPresence](readmodelcache.Config[channelMessageLookupKey, channelTopReactionPresence]{
			MaxEntries: max,
		}),
	}
}

func (c *ChannelTopMessageCache) getOrLoadBatch(
	ctx context.Context,
	keys []channelMessageLookupKey,
	load func(context.Context, []channelMessageLookupKey) (map[channelMessageLookupKey]domain.ChannelMessage, error),
) (map[channelMessageLookupKey]domain.ChannelMessage, error) {
	if c == nil {
		return load(ctx, keys)
	}
	return c.cache.GetOrLoadBatch(
		ctx,
		keys,
		func(channelMessageLookupKey) (int64, bool) { return 0, true },
		func(ctx context.Context, missing []channelMessageLookupKey) (map[channelMessageLookupKey]domain.ChannelMessage, error) {
			loaded, err := load(ctx, missing)
			if err != nil {
				return nil, err
			}
			for _, key := range missing {
				if _, ok := loaded[key]; !ok {
					loaded[key] = domain.ChannelMessage{}
				}
			}
			return loaded, nil
		},
	)
}

func (c *ChannelTopMessageCache) deleteChannel(channelID int64) {
	if c == nil || channelID == 0 {
		return
	}
	c.cache.InvalidateWhere(func(key channelMessageLookupKey) bool { return key.channelID == channelID })
	c.reactionPresence.InvalidateWhere(func(key channelMessageLookupKey) bool { return key.channelID == channelID })
}

func (c *ChannelTopMessageCache) flush() {
	if c == nil {
		return
	}
	c.cache.Flush()
	c.reactionPresence.Flush()
}

// reactionPresenceFor returns only a shared existence bit. It never caches
// counts, chosen state, recent order or paid identities, all of which remain
// viewer/current-data projections. A negative bit is enough to skip three
// guaranteed-empty reaction queries for the many top messages with no
// reactions at all.
func (c *ChannelTopMessageCache) reactionPresenceFor(
	ctx context.Context,
	db sqlcgen.DBTX,
	messages []domain.ChannelMessage,
) (map[channelMessageLookupKey]channelTopReactionPresence, error) {
	keys := make([]channelMessageLookupKey, 0, len(messages))
	for _, msg := range messages {
		if msg.ChannelID == 0 || msg.ID <= 0 || domain.IsChannelHistoryClearMessage(msg) {
			continue
		}
		keys = append(keys, channelMessageLookupKey{channelID: msg.ChannelID, id: msg.ID})
	}
	if len(keys) == 0 {
		return map[channelMessageLookupKey]channelTopReactionPresence{}, nil
	}
	return c.reactionPresence.GetOrLoadBatch(
		ctx,
		keys,
		func(channelMessageLookupKey) (int64, bool) { return 0, true },
		func(ctx context.Context, missing []channelMessageLookupKey) (map[channelMessageLookupKey]channelTopReactionPresence, error) {
			channelIDs := make([]int64, 0, len(missing))
			messageIDs := make([]int32, 0, len(missing))
			for _, key := range missing {
				channelIDs = append(channelIDs, key.channelID)
				messageIDs = append(messageIDs, pgInt32NonNegative(key.id))
			}
			rows, err := db.Query(ctx, `
WITH requested AS (
    SELECT channel_id, message_id
    FROM unnest($1::bigint[], $2::int[]) AS r(channel_id, message_id)
)
SELECT r.channel_id, r.message_id,
       EXISTS (
           SELECT 1 FROM channel_message_reactions normal
           WHERE normal.channel_id=r.channel_id AND normal.message_id=r.message_id
       ),
       EXISTS (
           SELECT 1 FROM channel_message_paid_reactions paid
           WHERE paid.channel_id=r.channel_id AND paid.message_id=r.message_id
       )
FROM requested r`, channelIDs, messageIDs)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			out := make(map[channelMessageLookupKey]channelTopReactionPresence, len(missing))
			for rows.Next() {
				var key channelMessageLookupKey
				var presence channelTopReactionPresence
				if err := rows.Scan(&key.channelID, &key.id, &presence.Normal, &presence.Paid); err != nil {
					return nil, err
				}
				out[key] = presence
			}
			if err := rows.Err(); err != nil {
				return nil, err
			}
			for _, key := range missing {
				if _, ok := out[key]; !ok {
					out[key] = channelTopReactionPresence{}
				}
			}
			return out, nil
		},
	)
}

// cloneChannelTopMessage isolates every mutable field that is enriched by the
// dialog projection path. Media is an immutable decoded storage snapshot; the
// hot path only reads it and never mutates its nested objects.
func cloneChannelTopMessage(msg domain.ChannelMessage) domain.ChannelMessage {
	msg.Entities = append([]domain.MessageEntity(nil), msg.Entities...)
	msg.ReplyTo = cloneMessageReply(msg.ReplyTo)
	msg.Forward = cloneMessageForward(msg.Forward)
	msg.Action = cloneChannelMessageAction(msg.Action)
	if msg.SendAs != nil {
		peer := *msg.SendAs
		msg.SendAs = &peer
	}
	if msg.SuggestedPost != nil {
		suggested := *msg.SuggestedPost
		if suggested.Price != nil {
			price := *suggested.Price
			suggested.Price = &price
		}
		msg.SuggestedPost = &suggested
	}
	if msg.Discussion != nil {
		discussion := *msg.Discussion
		msg.Discussion = &discussion
	}
	if msg.Replies != nil {
		replies := *msg.Replies
		replies.RecentRepliers = append([]domain.Peer(nil), msg.Replies.RecentRepliers...)
		msg.Replies = &replies
	}
	if msg.Reactions != nil {
		reactions := *msg.Reactions
		reactions.Results = append([]domain.ChannelMessageReactionCount(nil), msg.Reactions.Results...)
		reactions.Recent = append([]domain.ChannelMessagePeerReaction(nil), msg.Reactions.Recent...)
		msg.Reactions = &reactions
	}
	if msg.RichMessage != nil {
		rich := *msg.RichMessage
		rich.Blocks = append([]byte(nil), msg.RichMessage.Blocks...)
		rich.Photos = append([]domain.Photo(nil), msg.RichMessage.Photos...)
		rich.Documents = append([]domain.Document(nil), msg.RichMessage.Documents...)
		rich.BotAPIProjection = append([]byte(nil), msg.RichMessage.BotAPIProjection...)
		msg.RichMessage = &rich
	}
	if msg.ReplyMarkup != nil {
		markup := *msg.ReplyMarkup
		if msg.ReplyMarkup.Inline != nil {
			markup.Inline = make([][]domain.MarkupButton, len(msg.ReplyMarkup.Inline))
			for i, row := range msg.ReplyMarkup.Inline {
				markup.Inline[i] = append([]domain.MarkupButton(nil), row...)
				for j := range markup.Inline[i] {
					markup.Inline[i][j].Data = append([]byte(nil), row[j].Data...)
				}
			}
		}
		if msg.ReplyMarkup.Keyboard != nil {
			markup.Keyboard = make([][]domain.MarkupButton, len(msg.ReplyMarkup.Keyboard))
			for i, row := range msg.ReplyMarkup.Keyboard {
				markup.Keyboard[i] = append([]domain.MarkupButton(nil), row...)
			}
		}
		msg.ReplyMarkup = &markup
	}
	return msg
}
