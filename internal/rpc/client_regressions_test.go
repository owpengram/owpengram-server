package rpc

import (
	"context"
	"testing"

	"github.com/iamxvbaba/td/clock"
	"github.com/iamxvbaba/td/tg"
	"go.uber.org/zap/zaptest"

	"telesrv/internal/domain"
)

func TestMessagesSearchPhoneCallsDoesNotFallThroughToOrdinaryHistory(t *testing.T) {
	r := New(Config{}, Deps{}, zaptest.NewLogger(t), clock.System)
	filter, err := r.messageFilterFromSearchRequest(context.Background(), 1001, &tg.MessagesSearchRequest{
		Peer:   &tg.InputPeerSelf{},
		Filter: &tg.InputMessagesFilterPhoneCalls{Missed: true},
		Limit:  50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !filter.PhoneCallsOnly || !filter.MissedPhoneCallsOnly {
		t.Fatalf("phone filter = %+v", filter)
	}
	if searchFilterNeedsMediaStore(&tg.InputMessagesFilterPhoneCalls{}) {
		t.Fatal("phone-call filter must use message service-action search, not media search")
	}
}

func TestCrossDialogReplyKeepsExplicitSourcePeer(t *testing.T) {
	const senderID, destinationID, sourceID = int64(1001), int64(1002), int64(1003)
	r := New(Config{}, Deps{Users: mapUsersService{users: map[int64]domain.User{
		senderID:      {ID: senderID, AccessHash: 11},
		destinationID: {ID: destinationID, AccessHash: 22},
		sourceID:      {ID: sourceID, AccessHash: 33},
	}}}, zaptest.NewLogger(t), clock.System)
	input := &tg.InputReplyToMessage{ReplyToMsgID: 77}
	input.SetReplyToPeerID(&tg.InputPeerUser{UserID: sourceID, AccessHash: 33})
	input.SetQuoteText("source quote")
	reply, err := r.messageReplyFromInput(context.Background(), senderID,
		domain.Peer{Type: domain.PeerTypeUser, ID: destinationID}, input)
	if err != nil {
		t.Fatal(err)
	}
	if reply == nil || reply.MessageID != 77 || reply.Peer != (domain.Peer{Type: domain.PeerTypeUser, ID: sourceID}) {
		t.Fatalf("cross-dialog reply = %+v", reply)
	}
}
