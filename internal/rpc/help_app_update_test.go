package rpc

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/iamxvbaba/td/clock"
	"github.com/iamxvbaba/td/tg"

	"telesrv/internal/updatecdn"
)

type fakeAppUpdateResolver struct {
	request updatecdn.ResolveRequest
	result  *updatecdn.ResolvedUpdate
	err     error
}

func (f *fakeAppUpdateResolver) Resolve(_ context.Context, req updatecdn.ResolveRequest) (*updatecdn.ResolvedUpdate, error) {
	f.request = req
	return f.result, f.err
}

func TestHelpGetAppUpdateUsesClientPlatformVersionSourceAndLanguage(t *testing.T) {
	resolver := &fakeAppUpdateResolver{result: &updatecdn.ResolvedUpdate{
		ID: 91, Version: "12.9.1", Text: "Новая версия", URL: "https://updates.example/app.apk", CanNotSkip: true,
	}}
	router := New(Config{PublicBaseURL: "https://telesrv.example"}, Deps{AppUpdates: resolver}, zap.NewNop(), clock.System)
	ctx := WithClientInfo(WithUserID(context.Background(), 1000000001), ClientInfo{
		Type: ClientTypeAndroid, AppVersion: "12.9.0 (500)", LangCode: "ru", SystemLangCode: "en",
	})
	result, err := router.onHelpGetAppUpdate(ctx, "com.example.store")
	if err != nil {
		t.Fatal(err)
	}
	update, ok := result.(*tg.HelpAppUpdate)
	if !ok {
		t.Fatalf("result = %T, want *tg.HelpAppUpdate", result)
	}
	if update.ID != 91 || update.Version != "12.9.1" || update.Text != "Новая версия" || !update.GetCanNotSkip() {
		t.Fatalf("update = %#v", update)
	}
	if got, ok := update.GetURL(); !ok || got != "https://updates.example/app.apk" {
		t.Fatalf("url = %q, %v", got, ok)
	}
	if resolver.request.Platform != "android" || resolver.request.Version != "12.9.0 (500)" ||
		resolver.request.Source != "com.example.store" || resolver.request.LangCode != "ru" {
		t.Fatalf("resolve request = %#v", resolver.request)
	}
}

func TestHelpGetAppUpdateFailsClosedWithoutRPCError(t *testing.T) {
	resolver := &fakeAppUpdateResolver{err: errors.New("service unavailable")}
	router := New(Config{PublicBaseURL: "https://telesrv.example"}, Deps{AppUpdates: resolver}, zap.NewNop(), clock.System)
	ctx := WithClientInfo(WithUserID(context.Background(), 1000000001), ClientInfo{Type: ClientTypeIOS, AppVersion: "12.9.0"})
	result, err := router.onHelpGetAppUpdate(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.(*tg.HelpNoAppUpdate); !ok {
		t.Fatalf("result = %T, want *tg.HelpNoAppUpdate", result)
	}
}
