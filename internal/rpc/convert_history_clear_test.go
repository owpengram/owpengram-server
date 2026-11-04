package rpc

import (
	"testing"

	"github.com/owpengram/owpengram-td/tg"

	"telesrv/internal/domain"
)

func TestTGMessageProjectsHistoryClearServiceAction(t *testing.T) {
	message := domain.NewHistoryClearMessage(
		1001,
		domain.Peer{Type: domain.PeerTypeUser, ID: 1002},
		77,
		88,
		1700000000,
		9,
	)
	got, ok := tgMessage(message).(*tg.MessageService)
	if !ok {
		t.Fatalf("message = %T, want *tg.MessageService", tgMessage(message))
	}
	if got.ID != 77 || !got.Out || got.PeerID == nil || got.FromID == nil {
		t.Fatalf("service message = %+v, want owner-local id/peer/from", got)
	}
	if _, ok := got.Action.(*tg.MessageActionHistoryClear); !ok {
		t.Fatalf("action = %T, want *tg.MessageActionHistoryClear", got.Action)
	}
}
