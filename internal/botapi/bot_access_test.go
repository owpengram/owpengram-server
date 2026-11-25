package botapi

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"telesrv/internal/domain"
)

type accessCheckingGateway struct {
	*fakeBotAPIGateway
	blocked bool
	err     error
}

func (g *accessCheckingGateway) BotAPIAccessBlocked(context.Context, int64) (bool, error) {
	return g.blocked, g.err
}

func TestBotAPIRefusesBannedBots(t *testing.T) {
	bots := &fakeBotAPIBots{profile: domain.BotProfile{BotUserID: 1001, OwnerUserID: 5, TokenSecret: "secret"}}
	gw := &accessCheckingGateway{fakeBotAPIGateway: &fakeBotAPIGateway{self: domain.User{ID: 1001, FirstName: "Bot", Bot: true}}}
	h := (&handler{bots: bots, gateway: gw}).routes()

	if rec := performBotAPIRequest(t, h, bots.profile, "getMe", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("allowed bot: status=%d body=%s", rec.Code, rec.Body.String())
	}
	gw.blocked = true
	if rec := performBotAPIRequest(t, h, bots.profile, "getMe", `{}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("banned bot: status=%d body=%s", rec.Code, rec.Body.String())
	}
	// A failed lookup is retryable (500), never a 401 that bot libraries treat
	// as a revoked token.
	gw.blocked, gw.err = false, errors.New("db down")
	if rec := performBotAPIRequest(t, h, bots.profile, "getMe", `{}`); rec.Code != http.StatusInternalServerError {
		t.Fatalf("lookup error: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
