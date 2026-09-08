package postgres

import (
	"context"
	"reflect"
	"telesrv/internal/domain"
	"testing"
)

func TestMessageSearchSenderAndCountPredicates(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	users := NewUserStore(pool)
	suffix := randomSuffix(t)
	a, err := users.Create(ctx, domain.User{AccessHash: 61, Phone: "+1669" + suffix + "01", FirstName: "Search A"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := users.Create(ctx, domain.User{AccessHash: 62, Phone: "+1669" + suffix + "02", FirstName: "Search B"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM users WHERE id=ANY($1::bigint[])", []int64{a.ID, b.ID}) })
	s := newTestMessageStore(pool)
	for i := 1; i <= 6; i++ {
		sender, recipient := a.ID, b.ID
		if i%2 == 0 {
			sender, recipient = recipient, sender
		}
		_, err = s.SendPrivateText(ctx, domain.SendPrivateTextRequest{SenderUserID: sender, RecipientUserID: recipient, RandomID: int64(9100 + i), Message: "count needle", Date: 1700000000 + i})
		if err != nil {
			t.Fatal(err)
		}
	}
	peer := domain.Peer{Type: domain.PeerTypeUser, ID: b.ID}
	_, err = s.PinPrivateMessage(ctx, domain.PinPrivateMessageRequest{OwnerUserID: a.ID, Peer: peer, MessageID: 3, Pinned: true, PmOneside: true, Date: 1700000010})
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name  string
		f     domain.MessageFilter
		ids   []int
		count int
	}{
		{"sender-backward", domain.MessageFilter{SenderUserID: a.ID, Limit: 10, NeedTotalCount: true}, []int{5, 3, 1}, 3},
		{"sender-around", domain.MessageFilter{SenderUserID: a.ID, OffsetID: 3, AddOffset: -1, Limit: 3, NeedTotalCount: true}, []int{3, 1}, 3},
		{"sender-forward", domain.MessageFilter{SenderUserID: b.ID, OffsetID: 2, AddOffset: -2, Limit: 2, NeedTotalCount: true}, []int{4, 2}, 3},
		{"count-ignores-page", domain.MessageFilter{SenderUserID: a.ID, CountOnly: true, OffsetID: 1, AddOffset: 100}, nil, 3},
		{"count-dates-ids", domain.MessageFilter{SenderUserID: a.ID, CountOnly: true, MinDate: 1700000001, MaxDate: 1700000006, MinID: 1, MaxID: 5}, nil, 1},
		{"empty-backward-total", domain.MessageFilter{SenderUserID: a.ID, AddOffset: 100, Limit: 2, NeedTotalCount: true}, nil, 3},
		{"empty-forward-total", domain.MessageFilter{SenderUserID: a.ID, OffsetID: 6, AddOffset: -2, Limit: 2, NeedTotalCount: true}, nil, 3},
		{"count-empty", domain.MessageFilter{SenderUserID: a.ID, CountOnly: true, MinID: 5}, nil, 0},
		{"count-pinned-own", domain.MessageFilter{SenderUserID: a.ID, CountOnly: true, PinnedOnly: true}, nil, 1},
		{"count-pinned-other", domain.MessageFilter{SenderUserID: b.ID, CountOnly: true, PinnedOnly: true}, nil, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := tt.f
			f.HasPeer = true
			f.Peer = peer
			f.Query = "needle"
			got, err := s.ListByUser(ctx, a.ID, f)
			if err != nil {
				t.Fatal(err)
			}
			ids := messageIDs(got.Messages)
			if len(ids) != len(tt.ids) || (len(ids) > 0 && !reflect.DeepEqual(ids, tt.ids)) || got.Count != tt.count {
				t.Fatalf("ids=%v count=%d want %v/%d", ids, got.Count, tt.ids, tt.count)
			}
			if f.CountOnly && len(got.Users) != 0 {
				t.Fatalf("count hydrated users: %+v", got.Users)
			}
		})
	}
	// The opposite owner cannot observe a one-sided pin through a global count.
	got, err := s.ListByUser(ctx, b.ID, domain.MessageFilter{CountOnly: true, PinnedOnly: true, SenderUserID: a.ID, Query: "needle"})
	if err != nil || got.Count != 0 {
		t.Fatalf("owner isolation %+v %v", got, err)
	}
}
