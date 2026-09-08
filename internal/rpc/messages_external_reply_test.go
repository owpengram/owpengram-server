package rpc

import (
	"context"
	"reflect"
	"testing"

	"github.com/iamxvbaba/td/tg"
	"github.com/iamxvbaba/td/tgerr"
	"telesrv/internal/domain"
)

func TestExternalPrivateReplySnapshotWireAndProtection(t *testing.T) {
	r, store, events, a, b := savedForwardFixture(t)
	ctx := WithUserID(context.Background(), a.ID)
	seed, err := store.SendPrivateText(ctx, domain.SendPrivateTextRequest{SenderUserID: a.ID, RecipientUserID: a.ID, RandomID: 1, Message: "a🌕 quote", Date: 1700000000, Media: &domain.MessageMedia{Kind: domain.MessageMediaKindContact, Contact: &domain.MessageContact{FirstName: "snapshot", PhoneNumber: "123"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, manual := range []bool{false, true} {
		reply := &tg.InputReplyToMessage{ReplyToMsgID: seed.SenderMessage.ID}
		reply.SetReplyToPeerID(&tg.InputPeerSelf{})
		random := int64(2)
		if manual {
			random = 3
			reply.SetQuoteText("quote")
			reply.SetQuoteOffset(4)
		}
		req := &tg.MessagesSendMessageRequest{Peer: &tg.InputPeerUser{UserID: b.ID, AccessHash: b.AccessHash}, Message: "external", RandomID: random}
		req.SetReplyTo(reply)
		out, err := r.onMessagesSendMessage(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		m := out.(*tg.Updates).Updates[1].(*tg.UpdateNewMessage).Message.(*tg.Message)
		h := m.ReplyTo.(*tg.MessageReplyHeader)
		if h.ReplyFrom.FromID.(*tg.PeerUser).UserID != a.ID || h.ReplyFrom.Date != seed.SenderMessage.Date || h.ReplyToMsgID != seed.SenderMessage.ID || h.Quote != manual {
			t.Fatalf("sender reply=%+v", h)
		}
		if manual {
			if h.QuoteText != "quote" || h.QuoteOffset != 4 {
				t.Fatal("manual quote")
			}
		} else if h.QuoteText != seed.SenderMessage.Body {
			t.Fatal("external preview text missing")
		}
		if h.ReplyMedia.(*tg.MessageMediaContact).FirstName != "snapshot" {
			t.Fatal("media snapshot")
		}
		ownerEvents := savedForwardEvents(t, events, b.ID)[0]
		recipient := ownerEvents[len(ownerEvents)-1].Message
		rh := tgMessageReplyHeader(recipient).(*tg.MessageReplyHeader)
		if _, set := rh.GetReplyToMsgID(); set {
			t.Fatal("recipient received sender-owned source ID")
		}
		if !reflect.DeepEqual(rh.ReplyFrom, h.ReplyFrom) {
			t.Fatal("external author differs by owner")
		}
		if manual {
			if _, err := store.DeleteMessages(ctx, domain.DeleteMessagesRequest{OwnerUserID: a.ID, IDs: []int{seed.SenderMessage.ID}}); err != nil {
				t.Fatal(err)
			}
		}
		if manual {
			before := savedForwardEvents(t, events, a.ID, b.ID)
			replayed, err := r.onMessagesSendMessage(ctx, req)
			if err != nil || !reflect.DeepEqual(out.(*tg.Updates).Updates, replayed.(*tg.Updates).Updates) || !reflect.DeepEqual(before, savedForwardEvents(t, events, a.ID, b.ID)) {
				t.Fatalf("exact replay=%v", err)
			}
		}
	}
	protected, err := store.SendPrivateText(ctx, domain.SendPrivateTextRequest{SenderUserID: a.ID, RecipientUserID: a.ID, RandomID: 10, Message: "protected", NoForwards: true, Date: 1700000001})
	if err != nil {
		t.Fatal(err)
	}
	reply := &tg.InputReplyToMessage{ReplyToMsgID: protected.SenderMessage.ID}
	reply.SetReplyToPeerID(&tg.InputPeerSelf{})
	req := &tg.MessagesSendMessageRequest{Peer: &tg.InputPeerUser{UserID: b.ID, AccessHash: b.AccessHash}, Message: "forbidden", RandomID: 11}
	req.SetReplyTo(reply)
	before := savedForwardEvents(t, events, a.ID, b.ID)
	if _, err := r.onMessagesSendMessage(ctx, req); !tgerr.Is(err, "CHAT_FORWARDS_RESTRICTED") {
		t.Fatalf("protected source=%v", err)
	}
	if !reflect.DeepEqual(before, savedForwardEvents(t, events, a.ID, b.ID)) {
		t.Fatal("rejection wrote event")
	}
}
