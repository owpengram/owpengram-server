package memory

import (
	"context"
	"testing"

	"telesrv/internal/branding"
	"telesrv/internal/domain"
)

func TestBuiltInBotSeedsUseConfiguredProductName(t *testing.T) {
	previous := branding.Current()
	t.Cleanup(func() {
		if err := branding.Configure(previous); err != nil {
			t.Fatalf("restore branding: %v", err)
		}
	})
	cfg := previous
	cfg.ProductName = "Example Chat"
	if err := branding.Configure(cfg); err != nil {
		t.Fatalf("configure branding: %v", err)
	}

	ctx := context.Background()
	users := NewUserStore()
	bots := NewBotStore(users)
	for _, test := range []struct {
		id   int64
		want string
	}{
		{id: domain.ChatBotUserID, want: domain.ChatBotDescription()},
		{id: domain.StickersBotUserID, want: domain.StickersBotDescription()},
	} {
		user, found, err := users.ByID(ctx, test.id)
		if err != nil || !found || user.About != test.want {
			t.Fatalf("bot user %d = %+v found=%v err=%v, want about %q", test.id, user, found, err, test.want)
		}
		profile, found, err := bots.GetBot(ctx, test.id)
		if err != nil || !found || profile.Description != test.want {
			t.Fatalf("bot profile %d = %+v found=%v err=%v, want description %q", test.id, profile, found, err, test.want)
		}
	}
}
