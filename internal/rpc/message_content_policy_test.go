package rpc

import (
	"context"
	"testing"

	"github.com/owpengram/owpengram-td/tgerr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestMessageTextPolicyBlocksKeysAndReportsOpaqueTokens(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	r := &Router{log: zap.New(core)}
	ctx := WithUserID(context.Background(), 42)

	renamedKeyExchange := "NewHello3:MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEIMYERjudK_secZ7VHC3KwvhXzJg1SZz6sCDnziiSWzTVqtfahNrKkOuUHwH_ZyyIoGXvhxgh2ZxDZ3nYwjBlTw"
	if err := r.checkMessageTextPolicy(ctx, "messages.sendMessage", renamedKeyExchange); !tgerr.Is(err, "MESSAGE_CONTENT_BLOCKED") {
		t.Fatalf("raw key err = %v, want MESSAGE_CONTENT_BLOCKED", err)
	}
	opaque := "EZF4w0AYrgW693AG168Ijt3o:1793851541:319c9854898041419c536eb799693479:9609b77e4881884"
	if err := r.checkMessageTextPolicy(ctx, "messages.sendMessage", opaque); err != nil {
		t.Fatalf("opaque token must only be reported, got %v", err)
	}
	if err := r.checkMessageTextPolicy(ctx, "messages.sendMessage", "Привіт, як справи? Зустрінемось завтра о п'ятій."); err != nil {
		t.Fatalf("plain text err = %v", err)
	}
	hits := logs.FilterMessage("message content policy hit").All()
	if len(hits) != 2 {
		t.Fatalf("policy log hits = %d, want 2", len(hits))
	}
	for _, h := range hits {
		for _, f := range h.Context {
			if f.Key == "text" || f.String == opaque || f.String == renamedKeyExchange {
				t.Fatalf("message text leaked into logs: %+v", f)
			}
		}
	}
	if got := hits[1].ContextMap()["verdict"]; got != "opaque_token" {
		t.Fatalf("second hit verdict = %v", got)
	}
}
