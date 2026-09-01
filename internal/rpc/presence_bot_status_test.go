package rpc

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/iamxvbaba/td/clock"
	"go.uber.org/zap/zaptest"

	"telesrv/internal/domain"
)

func TestUserBotStatusUsesBaseFactProviderAndCachesOnlyKnownResults(t *testing.T) {
	users := &captureBaseBotStatusUsers{bot: true, found: true}
	r := New(Config{}, Deps{Users: users}, zaptest.NewLogger(t), clock.System)
	for i := 0; i < 2; i++ {
		bot, known := r.userBotStatus(context.Background(), 42)
		if !known || !bot {
			t.Fatalf("bot status = %v, known=%v", bot, known)
		}
	}
	if users.statusCalls.Load() != 1 || users.byIDCalls.Load() != 0 {
		t.Fatalf("calls = status:%d projected:%d", users.statusCalls.Load(), users.byIDCalls.Load())
	}

	human := &captureBaseBotStatusUsers{found: true}
	r = New(Config{}, Deps{Users: human}, zaptest.NewLogger(t), clock.System)
	for i := 0; i < 2; i++ {
		if bot, known := r.userBotStatus(context.Background(), 43); !known || bot {
			t.Fatalf("human status = %v, known=%v", bot, known)
		}
	}
	if human.statusCalls.Load() != 1 || human.byIDCalls.Load() != 0 {
		t.Fatalf("human calls = status:%d projected:%d", human.statusCalls.Load(), human.byIDCalls.Load())
	}

	for _, tc := range []struct {
		name  string
		users *captureBaseBotStatusUsers
	}{
		{name: "missing", users: &captureBaseBotStatusUsers{}},
		{name: "read error", users: &captureBaseBotStatusUsers{err: errors.New("redis and postgres unavailable")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := New(Config{}, Deps{Users: tc.users}, zaptest.NewLogger(t), clock.System)
			for i := 0; i < 2; i++ {
				if bot, known := r.userBotStatus(context.Background(), 44); known || bot {
					t.Fatalf("unknown status = %v, known=%v", bot, known)
				}
			}
			if tc.users.statusCalls.Load() != 2 {
				t.Fatalf("unknown result was cached; calls = %d", tc.users.statusCalls.Load())
			}
		})
	}
}

func TestAnnounceSessionOnlineSkipsUnknownBotClassification(t *testing.T) {
	users := &captureBaseBotStatusUsers{err: errors.New("base user unavailable")}
	r := New(Config{}, Deps{Users: users}, zaptest.NewLogger(t), clock.System)
	ctx := WithSessionID(WithRawAuthKeyID(context.Background(), [8]byte{1}), 2)
	r.announceSessionOnline(ctx, 45)
	if _, found := r.presence.statusFor(45, int(time.Now().Unix())); found {
		t.Fatal("unknown bot classification announced human presence")
	}
}

type captureBaseBotStatusUsers struct {
	UsersService
	bot         bool
	found       bool
	err         error
	statusCalls atomic.Int64
	byIDCalls   atomic.Int64
}

func (u *captureBaseBotStatusUsers) BotStatus(context.Context, int64) (bool, bool, error) {
	u.statusCalls.Add(1)
	return u.bot, u.found, u.err
}

func (u *captureBaseBotStatusUsers) ByID(context.Context, int64, int64) (domain.User, bool, error) {
	u.byIDCalls.Add(1)
	return domain.User{ID: 42, Bot: u.bot}, u.found, u.err
}
