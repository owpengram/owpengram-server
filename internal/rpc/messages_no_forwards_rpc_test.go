package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/owpengram/owpengram-td/clock"
	"github.com/owpengram/owpengram-td/tg"
	"github.com/owpengram/owpengram-td/tgerr"
	"go.uber.org/zap/zaptest"

	appdialogs "telesrv/internal/app/dialogs"
	appmessages "telesrv/internal/app/messages"
	appusers "telesrv/internal/app/users"
	"telesrv/internal/domain"
	"telesrv/internal/store/memory"
)

func TestMessagesToggleNoForwardsPrivateFullFlow(t *testing.T) {
	ctx := context.Background()
	usersStore := memory.NewUserStore()
	alice, _ := usersStore.Create(ctx, domain.User{AccessHash: 5101, Phone: "15550005101", FirstName: "Alice"})
	bob, _ := usersStore.Create(ctx, domain.User{AccessHash: 5102, Phone: "15550005102", FirstName: "Bob"})
	if _, err := usersStore.SetPremiumUntil(ctx, alice.ID, int(time.Now().Add(time.Hour).Unix())); err != nil {
		t.Fatalf("grant alice premium: %v", err)
	}
	dialogsStore := memory.NewDialogStore()
	messagesStore := memory.NewMessageStore(dialogsStore)
	router := New(Config{}, Deps{
		Users:    appusers.NewService(usersStore),
		Dialogs:  appdialogs.NewService(dialogsStore),
		Messages: appmessages.NewService(messagesStore, dialogsStore),
	}, zaptest.NewLogger(t), clock.System)

	enable, err := router.onMessagesToggleNoForwards(WithUserID(ctx, alice.ID), &tg.MessagesToggleNoForwardsRequest{
		Peer: &tg.InputPeerUser{UserID: bob.ID, AccessHash: bob.AccessHash}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("enable private noforwards: %v", err)
	}
	enableMessage := noForwardsServiceMessage(t, enable)
	if _, ok := enableMessage.Action.(*tg.MessageActionNoForwardsToggle); !ok {
		t.Fatalf("enable action = %T", enableMessage.Action)
	}
	assertNoForwardsFullFlags(t, router, ctx, alice, bob, true, false)
	assertNoForwardsFullFlags(t, router, ctx, bob, alice, false, true)
	source, err := messagesStore.SendPrivateText(ctx, domain.SendPrivateTextRequest{
		SenderUserID: alice.ID, RecipientUserID: bob.ID, RandomID: 5199, Message: "protected source", Date: int(time.Now().Unix()),
	})
	if err != nil {
		t.Fatalf("send protected source: %v", err)
	}
	if _, err := router.onMessagesForwardMessages(WithUserID(ctx, alice.ID), &tg.MessagesForwardMessagesRequest{
		FromPeer: &tg.InputPeerUser{UserID: bob.ID, AccessHash: bob.AccessHash},
		ID:       []int{source.SenderMessage.ID},
		RandomID: []int64{5200},
		ToPeer:   &tg.InputPeerSelf{},
	}); !tgerr.Is(err, "CHAT_FORWARDS_RESTRICTED") {
		t.Fatalf("forward protected private chat err=%v, want CHAT_FORWARDS_RESTRICTED", err)
	}

	// The other party cannot steal ownership by setting enabled=true. This is a
	// no-op and does not require that party to be premium.
	noOp, err := router.onMessagesToggleNoForwards(WithUserID(ctx, bob.ID), &tg.MessagesToggleNoForwardsRequest{
		Peer: &tg.InputPeerUser{UserID: alice.ID, AccessHash: alice.AccessHash}, Enabled: true,
	})
	if err != nil || len(noOp.(*tg.Updates).Updates) != 0 {
		t.Fatalf("peer repeat enable = %#v err=%v, want empty no-op", noOp, err)
	}

	requestUpdates, err := router.onMessagesToggleNoForwards(WithUserID(ctx, bob.ID), &tg.MessagesToggleNoForwardsRequest{
		Peer: &tg.InputPeerUser{UserID: alice.ID, AccessHash: alice.AccessHash}, Enabled: false,
	})
	if err != nil {
		t.Fatalf("request sharing: %v", err)
	}
	requestMessage := noForwardsServiceMessage(t, requestUpdates)
	requestAction, ok := requestMessage.Action.(*tg.MessageActionNoForwardsRequest)
	if !ok || requestAction.Expired || !requestAction.PrevValue || requestAction.NewValue {
		t.Fatalf("request action = %#v", requestMessage.Action)
	}
	aliceHistory, err := messagesStore.ListByUser(ctx, alice.ID, domain.MessageFilter{
		HasPeer: true, Peer: domain.Peer{Type: domain.PeerTypeUser, ID: bob.ID}, Limit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	var aliceRequestID int
	for _, msg := range aliceHistory.Messages {
		if msg.Media != nil && msg.Media.ServiceAction != nil &&
			msg.Media.ServiceAction.Kind == domain.MessageServiceActionNoForwardsRequest {
			aliceRequestID = msg.ID
		}
	}
	if aliceRequestID == 0 {
		t.Fatal("alice request box not found")
	}

	answerReq := &tg.MessagesToggleNoForwardsRequest{
		Peer:    &tg.InputPeerUser{UserID: bob.ID, AccessHash: bob.AccessHash},
		Enabled: false,
	}
	answerReq.SetRequestMsgID(aliceRequestID)
	answerUpdates, err := router.onMessagesToggleNoForwards(WithUserID(ctx, alice.ID), answerReq)
	if err != nil {
		t.Fatalf("accept sharing request: %v", err)
	}
	answerMessage := noForwardsServiceMessage(t, answerUpdates)
	answerAction, ok := answerMessage.Action.(*tg.MessageActionNoForwardsToggle)
	if !ok || !answerAction.PrevValue || answerAction.NewValue {
		t.Fatalf("answer action = %#v", answerMessage.Action)
	}
	if answerMessage.ReplyTo == nil {
		t.Fatal("answer service message has no reply_to")
	}
	assertNoForwardsFullFlags(t, router, ctx, alice, bob, false, false)
	assertNoForwardsFullFlags(t, router, ctx, bob, alice, false, false)

	if _, err := router.onMessagesToggleNoForwards(WithUserID(ctx, alice.ID), answerReq); !tgerr.Is(err, "REQUEST_MSG_EXPIRED") {
		t.Fatalf("repeat request answer err=%v, want REQUEST_MSG_EXPIRED", err)
	}
	if _, err := router.onMessagesToggleNoForwards(WithUserID(ctx, bob.ID), &tg.MessagesToggleNoForwardsRequest{
		Peer: &tg.InputPeerUser{UserID: alice.ID, AccessHash: alice.AccessHash}, Enabled: true,
	}); !tgerr.Is(err, "PREMIUM_ACCOUNT_REQUIRED") {
		t.Fatalf("non-premium fresh enable err=%v, want PREMIUM_ACCOUNT_REQUIRED", err)
	}
	if _, err := router.onMessagesToggleNoForwards(WithUserID(ctx, alice.ID), &tg.MessagesToggleNoForwardsRequest{
		Peer: &tg.InputPeerUser{UserID: bob.ID, AccessHash: bob.AccessHash + 1}, Enabled: true,
	}); !tgerr.Is(err, "PEER_ID_INVALID") {
		t.Fatalf("wrong access hash err=%v, want PEER_ID_INVALID", err)
	}
}

func noForwardsServiceMessage(t *testing.T, updates tg.UpdatesClass) *tg.MessageService {
	t.Helper()
	full, ok := updates.(*tg.Updates)
	if !ok || len(full.Updates) != 1 {
		t.Fatalf("updates = %#v, want one updateNewMessage", updates)
	}
	newMessage, ok := full.Updates[0].(*tg.UpdateNewMessage)
	if !ok || newMessage.Pts <= 0 || newMessage.PtsCount != 1 {
		t.Fatalf("update = %#v, want updateNewMessage pts_count=1", full.Updates[0])
	}
	service, ok := newMessage.Message.(*tg.MessageService)
	if !ok {
		t.Fatalf("message = %#v, want messageService (which has no message.noforwards field)", newMessage.Message)
	}
	return service
}

func assertNoForwardsFullFlags(t *testing.T, router *Router, ctx context.Context, viewer, target domain.User, wantMy, wantPeer bool) {
	t.Helper()
	full, err := router.onUsersGetFullUser(WithUserID(ctx, viewer.ID), &tg.InputUser{
		UserID: target.ID, AccessHash: target.AccessHash,
	})
	if err != nil {
		t.Fatalf("get full user %d->%d: %v", viewer.ID, target.ID, err)
	}
	if full.FullUser.GetNoforwardsMyEnabled() != wantMy ||
		full.FullUser.GetNoforwardsPeerEnabled() != wantPeer {
		t.Fatalf("full flags %d->%d my=%v peer=%v, want %v/%v",
			viewer.ID, target.ID,
			full.FullUser.GetNoforwardsMyEnabled(), full.FullUser.GetNoforwardsPeerEnabled(),
			wantMy, wantPeer)
	}
}
