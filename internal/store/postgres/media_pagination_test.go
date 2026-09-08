package postgres

import (
	"context"
	"reflect"
	"telesrv/internal/domain"
	"testing"
)

func TestMediaPaginationBoundaries(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	users := NewUserStore(pool)
	suffix := randomSuffix(t)
	a, err := users.Create(ctx, domain.User{AccessHash: 61, Phone: "+1677" + suffix + "01", FirstName: "Media A"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := users.Create(ctx, domain.User{AccessHash: 62, Phone: "+1677" + suffix + "02", FirstName: "Media B"})
	if err != nil {
		t.Fatal(err)
	}
	owner, other := a.ID, b.ID
	messages := newTestMessageStore(pool)
	channels := newTestChannelStore(pool)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM users WHERE id=ANY($1::bigint[])", []int64{owner, other}) })
	created, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{CreatorUserID: owner, Title: "media boundaries", Megagroup: true, MemberUserIDs: []int64{other}, Date: 1700000000})
	if err != nil {
		t.Fatal(err)
	}
	channel := created.Channel.ID
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id=$1", channel) })

	var privateIDs, channelIDs []int
	for i := 1; i <= 12; i++ {
		var media *domain.MessageMedia
		if i%2 == 1 {
			media = &domain.MessageMedia{Kind: domain.MessageMediaKindPhoto, Photo: &domain.Photo{ID: int64(i), AccessHash: 99}}
		}
		a, err := messages.SendPrivateText(ctx, domain.SendPrivateTextRequest{SenderUserID: owner, RecipientUserID: other, RandomID: int64(i), Message: "media boundary", Media: media, Date: 1700000000 + i})
		if err != nil {
			t.Fatal(err)
		}
		c, err := channels.SendChannelMessage(ctx, domain.SendChannelMessageRequest{UserID: owner, ChannelID: channel, RandomID: int64(i), Message: "media boundary", Media: media, Date: 1700000000 + i})
		if err != nil {
			t.Fatal(err)
		}
		if media != nil {
			privateIDs = append([]int{a.SenderMessage.ID}, privateIDs...)
			channelIDs = append([]int{c.Message.ID}, channelIDs...)
		}
	}
	for _, side := range []string{"private", "channel"} {
		ids := privateIDs
		if side == "channel" {
			ids = channelIDs
		}
		for _, tc := range []struct {
			name  string
			f     domain.MediaSearchRequest
			want  []int
			count int
		}{
			{"all", domain.MediaSearchRequest{Limit: 100}, ids, 6},
			{"strict-range", domain.MediaSearchRequest{Limit: 100, MinID: ids[4], MaxID: ids[1]}, ids[2:4], 2},
			{"strict-count", domain.MediaSearchRequest{Limit: 0, MinID: ids[4], MaxID: ids[1], OffsetID: ids[2], AddOffset: -2}, nil, 2},
			{"around-existing", domain.MediaSearchRequest{Limit: 4, OffsetID: ids[2], AddOffset: -2}, ids[1:5], 6},
			{"after-missing", domain.MediaSearchRequest{Limit: 2, OffsetID: ids[4] + 1, AddOffset: -2}, ids[2:4], 6},
			{"forward-gap", domain.MediaSearchRequest{Limit: 2, OffsetID: ids[4], AddOffset: -4}, ids[1:3], 6},
			{"empty-forward", domain.MediaSearchRequest{Limit: 2, OffsetID: ids[0] + 1, AddOffset: -2}, nil, 6},
			{"around-top", domain.MediaSearchRequest{Limit: 4, OffsetID: ids[0] + 1, AddOffset: -2}, ids[:2], 6},
			{"around-zero", domain.MediaSearchRequest{Limit: 4, AddOffset: -2}, ids[:2], 6},
			{"empty-backward", domain.MediaSearchRequest{Limit: 2, AddOffset: 100}, nil, 6},
		} {
			t.Run(side+"/"+tc.name, func(t *testing.T) {
				f := tc.f
				f.Categories = []domain.MediaCategory{domain.MediaCategoryPhoto, domain.MediaCategoryPhoto}
				f.Query = "boundary"
				var got []int
				var count int
				if side == "private" {
					r, err := messages.SearchPrivateMedia(ctx, owner, other, f)
					if err != nil {
						t.Fatal(err)
					}
					count = r.Count
					for _, m := range r.Messages {
						got = append(got, m.ID)
					}
				} else {
					r, err := channels.SearchChannelMedia(ctx, owner, channel, f)
					if err != nil {
						t.Fatal(err)
					}
					count = r.Count
					for _, m := range r.Messages {
						got = append(got, m.ID)
					}
				}
				if count != tc.count || !reflect.DeepEqual(append([]int{}, got...), append([]int{}, tc.want...)) {
					t.Fatalf("ids=%v count=%d want %v/%d", got, count, tc.want, tc.count)
				}
			})
		}
	}
}
