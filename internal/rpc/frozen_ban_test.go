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
