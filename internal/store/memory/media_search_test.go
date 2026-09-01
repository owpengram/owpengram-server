package memory

import (
	"context"
	"testing"

	"telesrv/internal/domain"
)

func testPhotoMedia(id int64) *domain.MessageMedia {
	return &domain.MessageMedia{
		Kind:  domain.MessageMediaKindPhoto,
		Photo: &domain.Photo{ID: id, AccessHash: id + 100},
	}
}

func TestPrivateMediaSearchCombinesQuerySenderAndDate(t *testing.T) {
	ctx := context.Background()
	store := NewMessageStore()
	const alice, bob = int64(1001), int64(1002)
	send := func(sender, recipient, randomID int64, body string, date int) {
		t.Helper()
		if _, err := store.SendPrivateText(ctx, domain.SendPrivateTextRequest{
			SenderUserID: sender, RecipientUserID: recipient, RandomID: randomID,
			Message: body, Media: testPhotoMedia(randomID), Date: date,
		}); err != nil {
			t.Fatalf("send private media: %v", err)
		}
	}
	send(alice, bob, 1, "needle outside date", 100)
	send(alice, bob, 2, "needle wanted", 200)
	send(bob, alice, 3, "needle wrong sender", 210)
	send(alice, bob, 4, "other text", 220)

	got, err := store.SearchPrivateMedia(ctx, bob, alice, domain.MediaSearchRequest{
		Categories:   []domain.MediaCategory{domain.MediaCategoryPhoto},
		Query:        "NEEDLE",
		SenderUserID: alice,
		MinDate:      150,
		MaxDate:      205,
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("search private media: %v", err)
	}
	if got.Count != 1 || len(got.Messages) != 1 || got.Messages[0].Body != "needle wanted" {
		t.Fatalf("combined private media = count %d messages %+v", got.Count, got.Messages)
	}
}

func TestChannelMediaSearchCombinesQuerySenderAndDate(t *testing.T) {
	ctx := context.Background()
	store := NewChannelStore()
	created, err := store.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: 1,
		Title:         "combined media",
		Megagroup:     true,
		MemberUserIDs: []int64{2},
		Date:          100,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	send := func(sender, randomID int64, body string, date int) {
		t.Helper()
		if _, err := store.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
			UserID: sender, ChannelID: created.Channel.ID, RandomID: randomID,
			Message: body, Media: testPhotoMedia(randomID), Date: date,
		}); err != nil {
			t.Fatalf("send channel media: %v", err)
		}
	}
	send(1, 11, "needle outside date", 110)
	send(2, 12, "needle wrong sender", 210)
	send(1, 13, "needle wanted", 220)
	send(1, 14, "other text", 230)

	got, err := store.SearchChannelMedia(ctx, 1, created.Channel.ID, domain.MediaSearchRequest{
		Categories:   []domain.MediaCategory{domain.MediaCategoryPhoto},
		Query:        "NEEDLE",
		SenderUserID: 1,
		MinDate:      200,
		MaxDate:      225,
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("search channel media: %v", err)
	}
	if got.Count != 1 || len(got.Messages) != 1 || got.Messages[0].Body != "needle wanted" {
		t.Fatalf("combined channel media = count %d messages %+v", got.Count, got.Messages)
	}
}
