package rpc

import (
	"testing"

	"github.com/owpengram/owpengram-td/bin"
	"github.com/owpengram/owpengram-td/tg"
	"github.com/owpengram/owpengram-td/tlprofile"

	"telesrv/internal/domain"
)

// updateEditForumTopic only exists from Layer 233. Older clients must still
// decode the container, just without it (they get the service message).
func TestEditForumTopicUpdateLayerProjection(t *testing.T) {
	title := "Renamed"
	closed := true
	update := editForumTopicUpdate(42, domain.EditChannelForumTopicRequest{
		TopicID: 7,
		Title:   &title,
		Closed:  &closed,
	}, domain.ChannelForumTopic{TopicID: 7, Title: title, Closed: true, IconEmojiID: 99})
	if _, ok := update.GetIconEmojiID(); ok {
		t.Fatalf("icon_emoji_id set although the edit did not touch it")
	}

	for profile := tlprofile.Profile225; profile <= tlprofile.Profile233; profile++ {
		container := &tg.Updates{
			Updates: []tg.UpdateClass{
				&tg.UpdatePinnedForumTopic{Peer: &tg.PeerChannel{ChannelID: 42}, TopicID: 7},
				update,
			},
			Chats: []tg.ChatClass{},
			Users: []tg.UserClass{},
			Date:  1700000000,
		}
		var wire bin.Buffer
		if err := tlprofile.EncodeObject(profile, container, &wire); err != nil {
			t.Fatalf("encode Layer %d: %v", profile, err)
		}
		decoded, err := tlprofile.DecodeObject(profile, &bin.Buffer{Buf: wire.Copy()}, tlprofile.Limits{})
		if err != nil {
			t.Fatalf("decode Layer %d: %v", profile, err)
		}
		got := decoded.(*tg.Updates)
		var edit *tg.UpdateEditForumTopic
		for _, u := range got.Updates {
			if e, ok := u.(*tg.UpdateEditForumTopic); ok {
				edit = e
			}
		}
		if profile < tlprofile.Profile233 {
			if edit != nil || len(got.Updates) != 1 {
				t.Fatalf("Layer %d updates = %+v, want updateEditForumTopic dropped", profile, got.Updates)
			}
			continue
		}
		if edit == nil {
			t.Fatalf("Layer 233 lost updateEditForumTopic: %+v", got.Updates)
		}
		if v, ok := edit.GetTitle(); !ok || v != title {
			t.Fatalf("Layer 233 title = %q/%v", v, ok)
		}
		if v, ok := edit.GetClosed(); !ok || !v {
			t.Fatalf("Layer 233 closed = %v/%v", v, ok)
		}
	}
}
