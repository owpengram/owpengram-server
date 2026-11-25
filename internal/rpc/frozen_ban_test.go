package rpc

import (
	"context"
	"testing"

	"github.com/owpengram/owpengram-td/tgerr"

	"telesrv/internal/domain"
)

func TestFrozenBanModeRejectsEveryAuthorizedRPCExceptLogOut(t *testing.T) {
	const (
		frozenUser = int64(2001)
		otherUser  = int64(2002)
	)
	provider := &frozenGateFreezeProvider{items: map[int64]domain.AccountFreeze{
		frozenUser: frozenGateActiveState(frozenUser),
	}}
	router := &Router{deps: Deps{AccountFreeze: provider, FrozenAccountsBanned: true}}
	ctx := WithUserID(context.Background(), frozenUser)

	// Reads that read-only freeze allows are refused too, with the 401 that
	// makes clients drop the session.
	for _, method := range []string{"messages.getHistory", "channels.getMessages", "updates.getDifference", "messages.sendMessage", "help.getConfig"} {
		if err := router.checkFrozenRPC(ctx, method); !tgerr.Is(err, "USER_DEACTIVATED_BAN") {
			t.Fatalf("%s err = %v, want USER_DEACTIVATED_BAN", method, err)
		}
	}
	if err := router.checkFrozenRPC(ctx, "auth.logOut"); err != nil {
		t.Fatalf("auth.logOut gated: %v", err)
	}
	if err := router.checkFrozenRPC(WithUserID(context.Background(), otherUser), "messages.getHistory"); err != nil {
		t.Fatalf("other user gated: %v", err)
	}
	// Unauthorized calls (login flow) are not gated here; auth service refuses login.
	if err := router.checkFrozenRPC(context.Background(), "auth.sendCode"); err != nil {
		t.Fatalf("unauthorized call gated: %v", err)
	}
}

func TestFrozenReadOnlyModeKeepsReadsAllowed(t *testing.T) {
	const frozenUser = int64(2003)
	provider := &frozenGateFreezeProvider{items: map[int64]domain.AccountFreeze{
		frozenUser: frozenGateActiveState(frozenUser),
	}}
	router := &Router{deps: Deps{AccountFreeze: provider}}
	ctx := WithUserID(context.Background(), frozenUser)
	if err := router.checkFrozenRPC(ctx, "messages.getHistory"); err != nil {
		t.Fatalf("read gated in readonly mode: %v", err)
	}
	if err := router.checkFrozenRPC(ctx, "messages.sendMessage"); !tgerr.Is(err, "FROZEN_METHOD_INVALID") {
		t.Fatalf("write err = %v, want FROZEN_METHOD_INVALID", err)
	}
}

type frozenBanBots struct {
	BotsService
	owner map[int64]int64
}

func (b frozenBanBots) BotInfo(_ context.Context, botID int64) (domain.BotProfile, bool, error) {
	owner, ok := b.owner[botID]
	return domain.BotProfile{BotUserID: botID, OwnerUserID: owner}, ok, nil
}

func TestBotAPIAccessBlockedForFrozenBotOrOwner(t *testing.T) {
	const (
		owner      = int64(3001)
		bot        = int64(3002)
		frozenBot  = int64(3003)
		otherOwner = int64(3004)
		otherBot   = int64(3005)
	)
	provider := &frozenGateFreezeProvider{items: map[int64]domain.AccountFreeze{
		owner:     frozenGateActiveState(owner),
		frozenBot: frozenGateActiveState(frozenBot),
	}}
	bots := frozenBanBots{owner: map[int64]int64{bot: owner, frozenBot: otherOwner, otherBot: otherOwner}}
	router := &Router{deps: Deps{AccountFreeze: provider, FrozenAccountsBanned: true, Bots: bots}}
	ctx := context.Background()
	for id, want := range map[int64]bool{bot: true, frozenBot: true, otherBot: false} {
		got, err := router.BotAPIAccessBlocked(ctx, id)
		if err != nil || got != want {
			t.Fatalf("bot %d blocked=%v err=%v, want %v", id, got, err, want)
		}
	}
	readonly := &Router{deps: Deps{AccountFreeze: provider, Bots: bots}}
	if got, _ := readonly.BotAPIAccessBlocked(ctx, bot); got {
		t.Fatal("readonly mode must not ban bots of frozen owners")
	}
}
