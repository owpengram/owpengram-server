package postgres

import (
	"context"
	"testing"

	"telesrv/internal/domain"
)

func TestChannelTopMessageCacheBatchesClonesAndInvalidates(t *testing.T) {
	cache := NewChannelTopMessageCache(8)
	if cache == nil {
		t.Fatal("NewChannelTopMessageCache returned nil")
	}
	keys := []channelMessageLookupKey{{channelID: 7, id: 11}, {channelID: 7, id: 12}, {channelID: 8, id: 21}}
	loads := 0
	load := func(_ context.Context, missing []channelMessageLookupKey) (map[channelMessageLookupKey]domain.ChannelMessage, error) {
		loads++
		out := make(map[channelMessageLookupKey]domain.ChannelMessage, len(missing))
		for _, key := range missing {
			out[key] = domain.ChannelMessage{
				ChannelID: key.channelID,
				ID:        key.id,
				Entities:  []domain.MessageEntity{{Offset: 1, Length: 2}},
				Action:    &domain.ChannelMessageAction{UserIDs: []int64{3}},
				RichMessage: &domain.MessageRichMessage{
					Blocks: []byte{4, 5},
				},
			}
		}
		return out, nil
	}

	first, err := cache.getOrLoadBatch(context.Background(), keys, load)
	if err != nil {
		t.Fatal(err)
	}
	if loads != 1 {
		t.Fatalf("cold batch loads = %d, want 1", loads)
	}
	first[keys[0]].Entities[0].Offset = 99
	first[keys[0]].Action.UserIDs[0] = 99
	first[keys[0]].RichMessage.Blocks[0] = 99

	second, err := cache.getOrLoadBatch(context.Background(), keys, load)
	if err != nil {
		t.Fatal(err)
	}
	if loads != 1 {
		t.Fatalf("warm batch loads = %d, want 1", loads)
	}
	if got := second[keys[0]]; got.Entities[0].Offset != 1 || got.Action.UserIDs[0] != 3 || got.RichMessage.Blocks[0] != 4 {
		t.Fatalf("cached message alias-mutated: %+v", got)
	}

	listener := NewReadModelChangeListener("", ReadModelCacheSet{ChannelTopMessages: cache}, nil)
	cache.reactionPresence.Store(keys[0], channelTopReactionPresence{Normal: true})
	listener.handlePayload(`{"model":"channel_base","owner_user_id":0,"peer_type":"channel","peer_id":7}`)
	if _, ok := cache.reactionPresence.Peek(keys[0]); ok {
		t.Fatal("channel_base did not invalidate top reaction presence")
	}
	if _, err := cache.getOrLoadBatch(context.Background(), keys, load); err != nil {
		t.Fatal(err)
	}
	if loads != 2 {
		t.Fatalf("channel invalidation loads = %d, want 2", loads)
	}
}

func TestChannelTopMessageCacheNegativeResultIsCached(t *testing.T) {
	cache := NewChannelTopMessageCache(4)
	key := channelMessageLookupKey{channelID: 9, id: 1}
	loads := 0
	load := func(context.Context, []channelMessageLookupKey) (map[channelMessageLookupKey]domain.ChannelMessage, error) {
		loads++
		return map[channelMessageLookupKey]domain.ChannelMessage{}, nil
	}
	for i := 0; i < 2; i++ {
		got, err := cache.getOrLoadBatch(context.Background(), []channelMessageLookupKey{key}, load)
		if err != nil {
			t.Fatal(err)
		}
		if got[key].ID != 0 {
			t.Fatalf("negative result = %+v", got[key])
		}
	}
	if loads != 1 {
		t.Fatalf("negative cache loads = %d, want 1", loads)
	}
}
