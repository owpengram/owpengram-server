package rpc

import (
	"context"
	"testing"

	"github.com/iamxvbaba/td/bin"
	"github.com/iamxvbaba/td/tg"
	"github.com/iamxvbaba/td/tgerr"
	"github.com/iamxvbaba/td/tlprofile"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestDispatchGeneratedSafelyIsolatesHandlerPanic(t *testing.T) {
	dispatcher := tlprofile.NewDispatcher()
	registerRPC[*tg.HelpGetNearestDCRequest](dispatcher, tlprofile.SemanticMethodHelpGetNearestDC, func(context.Context, *tg.HelpGetNearestDCRequest) (any, error) {
		panic("projection boom")
	})
	core, observed := observer.New(zap.ErrorLevel)
	r := &Router{dispatcher: dispatcher, log: zap.New(core)}

	var wire bin.Buffer
	if err := tlprofile.EncodeObject(tlprofile.Profile229, &tg.HelpGetNearestDCRequest{}, &wire); err != nil {
		t.Fatal(err)
	}
	admission, err := dispatcher.Admit(tlprofile.Profile229, &wire, tlprofile.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.dispatchGeneratedSafely(context.Background(), "help.getNearestDc", admission)
	if result != nil {
		t.Fatalf("panic returned result %T", result)
	}
	if !tgerr.Is(err, "INTERNAL_SERVER_ERROR") {
		t.Fatalf("panic error = %v, want INTERNAL_SERVER_ERROR", err)
	}
	if got := observed.FilterMessage("RPC handler panic isolated").Len(); got != 1 {
		t.Fatalf("panic log count = %d, want 1", got)
	}
}
