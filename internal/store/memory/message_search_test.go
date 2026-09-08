package memory

import (
	"reflect"
	"telesrv/internal/domain"
	"testing"
)

func TestMessageSearchSenderAndCountFilters(t *testing.T) {
	peer := domain.Peer{Type: domain.PeerTypeUser, ID: 22}
	var messages []domain.Message
	for i := 1; i <= 6; i++ {
		sender := int64(11)
		if i%2 == 0 {
			sender = 22
		}
		messages = append(messages, domain.Message{ID: i, OwnerUserID: 11, Peer: peer, From: domain.Peer{Type: domain.PeerTypeUser, ID: sender}, Body: "count needle", Date: 100 + i, Pinned: i == 3})
	}
	for _, tt := range []struct {
		name  string
		f     domain.MessageFilter
		ids   []int
		count int
	}{
		{"sender", domain.MessageFilter{SenderUserID: 11, Limit: 10}, []int{5, 3, 1}, 3},
		{"sender-around", domain.MessageFilter{SenderUserID: 11, OffsetID: 3, AddOffset: -1, Limit: 3}, []int{3, 1}, 3},
		{"count-ignores-page", domain.MessageFilter{SenderUserID: 11, CountOnly: true, OffsetID: 1, AddOffset: 100}, nil, 3},
		{"count-intersection", domain.MessageFilter{SenderUserID: 11, CountOnly: true, MinDate: 101, MaxDate: 106, MinID: 1, MaxID: 5}, nil, 1},
		{"count-pinned", domain.MessageFilter{SenderUserID: 11, CountOnly: true, PinnedOnly: true}, nil, 1},
		{"count-pinned-other", domain.MessageFilter{SenderUserID: 22, CountOnly: true, PinnedOnly: true}, nil, 0},
		{"count-peer-isolation", domain.MessageFilter{CountOnly: true, RestrictPeerIDs: true, PeerIDs: []int64{33}}, nil, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := tt.f
			f.HasPeer = true
			f.Peer = peer
			f.Query = "needle"
			got := filterMessageList(cloneMessages(messages), f)
			var ids []int
			for _, m := range got.Messages {
				ids = append(ids, m.ID)
			}
			if !reflect.DeepEqual(ids, tt.ids) || got.Count != tt.count {
				t.Fatalf("ids=%v count=%d want %v/%d", ids, got.Count, tt.ids, tt.count)
			}
			if f.CountOnly && len(got.Users) > 0 {
				t.Fatal("count projected users")
			}
		})
	}
}
