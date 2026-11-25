package admin

import (
	"context"
	"strings"
	"testing"
	"time"

	"telesrv/internal/domain"
)

type cascadeBots struct {
	BotService
	owned map[int64][]domain.User
}

func (b cascadeBots) ListOwnedBots(_ context.Context, owner int64) ([]domain.User, error) {
	return b.owned[owner], nil
}

func TestSetAccountFrozenCascadesToOwnedBots(t *testing.T) {
	ctx := context.Background()
	restrictions := &fakeRestrictionStore{items: map[int64]domain.AccountFreeze{
		// independently frozen bot: must stay frozen after the owner is unfrozen
		9003: {UserID: 9003, Frozen: true, Reason: "spam bot", Since: fixedNow(), Until: fixedNow().Add(time.Hour)},
	}}
	svc := NewService(Dependencies{
		Commands:     newMemoryCommandRepo(),
		Restrictions: restrictions,
		Bots:         cascadeBots{owned: map[int64][]domain.User{900: {{ID: 9001, Bot: true}, {ID: 9002, Bot: true}, {ID: 9003, Bot: true}}}},
		Now:          fixedNow,
	})
	freeze := SetAccountFrozenRequest{
		CommandMeta: CommandMeta{CommandID: "freeze-owner", Actor: "ops", Reason: "csam"},
		UserID:      900, Frozen: true,
		Until:     fixedNow().Add(24 * time.Hour),
		AppealURL: "https://owpengram.org",
	}
	if _, err := svc.SetAccountFrozen(ctx, freeze); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{9001, 9002} {
		f := restrictions.items[id]
		if !f.Frozen || !strings.HasPrefix(f.Reason, "owner frozen: 900:") || !f.Until.Equal(freeze.Until) {
			t.Fatalf("bot %d not cascaded: %+v", id, f)
		}
	}
	if restrictions.items[9003].Reason != "spam bot" {
		t.Fatalf("independent bot freeze overwritten: %+v", restrictions.items[9003])
	}

	unfreeze := SetAccountFrozenRequest{
		CommandMeta: CommandMeta{CommandID: "unfreeze-owner", Actor: "ops", Reason: "appeal"},
		UserID:      900, Frozen: false,
	}
	if _, err := svc.SetAccountFrozen(ctx, unfreeze); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{9001, 9002} {
		if restrictions.items[id].Frozen {
			t.Fatalf("cascaded bot %d still frozen", id)
		}
	}
	if !restrictions.items[9003].Frozen {
		t.Fatal("independently frozen bot was unfrozen")
	}
}
