package rpc

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/iamxvbaba/td/clock"
	"github.com/iamxvbaba/td/tg"
	"github.com/iamxvbaba/td/tgerr"
	"go.uber.org/zap/zaptest"
	appdialogs "telesrv/internal/app/dialogs"
	appmessages "telesrv/internal/app/messages"
	appusers "telesrv/internal/app/users"
	"telesrv/internal/domain"
	"telesrv/internal/store/memory"
)

func savedForwardFixture(t *testing.T) (*Router, *memory.MessageStore, *memory.UpdateEventStore, domain.User, domain.User) {
	t.Helper()
	ctx := context.Background()
	users := memory.NewUserStore()
	a, err := users.Create(ctx, domain.User{AccessHash: 51, Phone: "15550009501", FirstName: "A"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := users.Create(ctx, domain.User{AccessHash: 52, Phone: "15550009502", FirstName: "B"})
	if err != nil {
		t.Fatal(err)
	}
	dialogs := memory.NewDialogStore()
	messages := memory.NewMessageStore(dialogs)
	events := memory.NewUpdateEventStore()
	messages.AttachUpdateEventStore(events)
	r := New(Config{}, Deps{Users: appusers.NewService(users), Dialogs: appdialogs.NewService(dialogs), Messages: appmessages.NewService(messages, dialogs)}, zaptest.NewLogger(t), clock.System)
	return r, messages, events, a, b
}

func savedForwardEvents(t *testing.T, events *memory.UpdateEventStore, owners ...int64) [][]domain.UpdateEvent {
	t.Helper()
	result := make([][]domain.UpdateEvent, len(owners))
	for i, owner := range owners {
		var err error
		result[i], err = events.ListAfter(context.Background(), owner, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func TestSavedForwardSourceScopeProtectionAndDeletedReplay(t *testing.T) {
	for _, scope := range []string{"self", "explicit-user", "inferred"} {
		for _, saved := range []bool{false, true} {
			for _, protected := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/saved=%v/protected=%v", scope, saved, protected), func(t *testing.T) {
					r, store, events, a, b := savedForwardFixture(t)
					ctx := WithUserID(context.Background(), a.ID)
					seed, err := store.SendPrivateText(ctx, domain.SendPrivateTextRequest{SenderUserID: a.ID, RecipientUserID: a.ID, RandomID: 1, Message: "saved 🌕 quote", NoForwards: protected, Date: 1700000000})
					if err != nil {
						t.Fatal(err)
					}
					to := tg.InputPeerClass(&tg.InputPeerSelf{})
					targetSender := a.ID
					if !saved {
						to = &tg.InputPeerUser{UserID: b.ID, AccessHash: b.AccessHash}
						targetSender = b.ID
					}
					target, err := store.SendPrivateText(ctx, domain.SendPrivateTextRequest{SenderUserID: targetSender, RecipientUserID: a.ID, RandomID: 2, Message: "target", Date: 1700000001})
					if err != nil {
						t.Fatal(err)
					}
					from := tg.InputPeerClass(&tg.InputPeerSelf{})
					if scope == "explicit-user" {
						from = &tg.InputPeerUser{UserID: a.ID, AccessHash: a.AccessHash}
					}
					if scope == "inferred" {
						from = &tg.InputPeerEmpty{}
					}
					req := &tg.MessagesForwardMessagesRequest{FromPeer: from, ToPeer: to, ID: []int{seed.SenderMessage.ID}, RandomID: []int64{3}}
					reply := &tg.InputReplyToMessage{ReplyToMsgID: target.RecipientMessage.ID}
					reply.SetQuoteText("target")
					req.SetReplyTo(reply)
					before := savedForwardEvents(t, events, a.ID, b.ID)
					out, err := r.onMessagesForwardMessages(ctx, req)
					if protected {
						if !tgerr.Is(err, "CHAT_FORWARDS_RESTRICTED") || !reflect.DeepEqual(before, savedForwardEvents(t, events, a.ID, b.ID)) {
							t.Fatalf("protected Saved source err=%v or wrote events", err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					full := out.(*tg.Updates)
					msg := full.Updates[1].(*tg.UpdateNewMessage).Message.(*tg.Message)
					if msg.Message != seed.SenderMessage.Body || msg.FwdFrom.FromID.(*tg.PeerUser).UserID != a.ID || msg.FwdFrom.Date != seed.SenderMessage.Date || msg.ReplyTo.(*tg.MessageReplyHeader).ReplyToMsgID != target.RecipientMessage.ID {
						t.Fatalf("forward metadata: %+v", msg)
					}
					after := savedForwardEvents(t, events, a.ID, b.ID)
					for i := range after {
						want := 1
						if saved && i == 1 {
							want = 0
						}
						if len(after[i])-len(before[i]) != want {
							t.Fatal("new forward event cardinality")
						}
						if want == 1 && after[i][len(after[i])-1].PtsCount != 1 {
							t.Fatal("new forward PTS count")
						}
					}
					if !saved {
						received := after[1][len(after[1])-1].Message
						if received.ReplyTo == nil || received.ReplyTo.MessageID != target.SenderMessage.ID {
							t.Fatal("recipient reply not mapped")
						}
					}
					if _, err := store.DeleteMessages(ctx, domain.DeleteMessagesRequest{OwnerUserID: a.ID, IDs: []int{seed.SenderMessage.ID}, Date: 1700000002}); err != nil {
						t.Fatal(err)
					}
					beforeReplay := savedForwardEvents(t, events, a.ID, b.ID)
					replay, err := r.onMessagesForwardMessages(ctx, req)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(full.Updates, replay.(*tg.Updates).Updates) || !reflect.DeepEqual(beforeReplay, savedForwardEvents(t, events, a.ID, b.ID)) {
						t.Fatal("replay changed message or appended event")
					}
					fresh := *req
					fresh.RandomID = []int64{4}
					if _, err := r.onMessagesForwardMessages(ctx, &fresh); !tgerr.Is(err, "MESSAGE_ID_INVALID") {
						t.Fatalf("fresh deleted source err=%v", err)
					}
					if !reflect.DeepEqual(beforeReplay, savedForwardEvents(t, events, a.ID, b.ID)) {
						t.Fatal("rejected fresh request appended event")
					}
				})
			}
		}
	}
}

// The injected boundary fails before the second send enters the real store.
// All successful sends and replay lookups retain the normal service/store path.
type failSecondForward struct {
	*appmessages.Service
	failRandom int64
	calls      []int64
}

func (s *failSecondForward) SendPrivateText(ctx context.Context, user int64, req domain.SendPrivateTextRequest) (domain.SendPrivateTextResult, error) {
	s.calls = append(s.calls, req.RandomID)
	if req.RandomID == s.failRandom {
		return domain.SendPrivateTextResult{}, errors.New("injected second-send failure")
	}
	return s.Service.SendPrivateText(ctx, user, req)
}

func TestSavedForwardPartialCommitRetryAfterCommittedSourceDeleted(t *testing.T) {
	r, store, events, a, b := savedForwardFixture(t)
	ctx := WithUserID(context.Background(), a.ID)
	ids := []int{}
	for i := int64(1); i <= 2; i++ {
		source, err := store.SendPrivateText(ctx, domain.SendPrivateTextRequest{SenderUserID: a.ID, RecipientUserID: a.ID, RandomID: i, Message: fmt.Sprintf("source-%d", i), Date: 1700000000})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, source.SenderMessage.ID)
	}
	fault := &failSecondForward{Service: r.deps.Messages.(*appmessages.Service), failRandom: 12}
	r.deps.Messages = fault
	req := &tg.MessagesForwardMessagesRequest{FromPeer: &tg.InputPeerSelf{}, ToPeer: &tg.InputPeerUser{UserID: b.ID, AccessHash: b.AccessHash}, ID: ids, RandomID: []int64{11, 12}}
	before := savedForwardEvents(t, events, a.ID, b.ID)
	if _, err := r.onMessagesForwardMessages(ctx, req); !tgerr.Is(err, "INTERNAL_SERVER_ERROR") {
		t.Fatalf("partial send err=%v", err)
	}
	after := savedForwardEvents(t, events, a.ID, b.ID)
	for i := range after {
		if len(after[i])-len(before[i]) != 1 {
			t.Fatal("first item must commit before second fails")
		}
	}
	first := after[0][len(after[0])-1].Message
	if _, err := store.DeleteMessages(ctx, domain.DeleteMessagesRequest{OwnerUserID: a.ID, IDs: ids[:1], Date: 1700000001}); err != nil {
		t.Fatal(err)
	}
	before = savedForwardEvents(t, events, a.ID, b.ID)
	fault.failRandom = 0
	out, err := r.onMessagesForwardMessages(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fault.calls, []int64{11, 12, 12}) {
		t.Fatalf("send calls=%v; committed item must not be re-sent", fault.calls)
	}
	full := out.(*tg.Updates)
	if full.Updates[0].(*tg.UpdateMessageID).ID != first.ID || len(full.Updates) != 4 {
		t.Fatal("partial retry lost first committed ID")
	}
	after = savedForwardEvents(t, events, a.ID, b.ID)
	for i := range after {
		if len(after[i])-len(before[i]) != 1 {
			t.Fatal("retry must only append second item")
		}
	}
	if _, err := r.onMessagesForwardMessages(ctx, req); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, savedForwardEvents(t, events, a.ID, b.ID)) {
		t.Fatal("full replay appended events")
	}
}

func TestReplyAndForwardExplicitSourceCredentials(t *testing.T) {
	for _, method := range []string{"reply", "forward"} {
		for _, wrong := range []tg.InputPeerClass{&tg.InputPeerUser{UserID: 22, AccessHash: 78}, &tg.InputPeerUser{UserID: 23, AccessHash: 77}, &tg.InputPeerEmpty{}} {
			t.Run(fmt.Sprintf("%s/%#v", method, wrong), func(t *testing.T) {
				messages := &captureMessages{}
				r := New(Config{}, Deps{Messages: messages, Users: mapUsersService{users: map[int64]domain.User{22: {ID: 22, AccessHash: 77}}}}, zaptest.NewLogger(t), clock.System)
				ctx := WithUserID(context.Background(), 11)
				var err error
				if method == "reply" {
					req := &tg.MessagesSendMessageRequest{Peer: &tg.InputPeerSelf{}, Message: "reply", RandomID: 91}
					reply := &tg.InputReplyToMessage{ReplyToMsgID: 7}
					reply.SetReplyToPeerID(wrong)
					req.SetReplyTo(reply)
					_, err = r.onMessagesSendMessage(ctx, req)
					if !tgerr.Is(err, "REPLY_MESSAGE_ID_INVALID") {
						t.Fatalf("reply err=%v", err)
					}
				} else {
					_, err = r.onMessagesForwardMessages(ctx, &tg.MessagesForwardMessagesRequest{FromPeer: wrong, ToPeer: &tg.InputPeerSelf{}, ID: []int{7}, RandomID: []int64{92}})
					// Empty from_peer is permitted only when an owned source can be inferred.
					want := "PEER_ID_INVALID"
					if _, ok := wrong.(*tg.InputPeerEmpty); ok {
						want = "MESSAGE_ID_INVALID"
					}
					if !tgerr.Is(err, want) {
						t.Fatalf("forward err=%v want=%s", err, want)
					}
				}
				if messages.sendReq.RandomID != 0 {
					t.Fatal("invalid credentials reached write service")
				}
			})
		}
	}
	for _, deps := range []Deps{{}, {Users: failingReadUsers{}}} {
		r := New(Config{}, deps, zaptest.NewLogger(t), clock.System)
		reply := &tg.InputReplyToMessage{ReplyToMsgID: 1}
		reply.SetReplyToPeerID(&tg.InputPeerUser{UserID: 22, AccessHash: 77})
		if _, err := r.messageReplyFromInput(context.Background(), 11, domain.Peer{Type: domain.PeerTypeUser, ID: 11}, reply); !tgerr.Is(err, "INTERNAL_SERVER_ERROR") {
			t.Fatalf("identity unavailable err=%v", err)
		}
	}
}
