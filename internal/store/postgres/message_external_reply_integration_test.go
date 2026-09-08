package postgres

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"telesrv/internal/domain"
	"telesrv/internal/store"
)

func externalReplyUsers(t *testing.T, pool *pgxpool.Pool) (domain.User, domain.User) {
	t.Helper()
	ctx := context.Background()
	users := NewUserStore(pool)
	suffix := randomSuffix(t)
	a := createTestUser(t, ctx, users, "+1813"+suffix+"01", "ExternalA", "")
	b := createTestUser(t, ctx, users, "+1813"+suffix+"02", "ExternalB", "")
	t.Logf("external reply fixture users: %d %d", a.ID, b.ID)
	t.Cleanup(func() {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		defer tx.Rollback(ctx)
		// Delete the owned projections before users: from_user_id is RESTRICT,
		// so user cascades alone depend on the database's trigger order.
		for _, query := range []string{
			"DELETE FROM dispatch_outbox WHERE target_user_id=ANY($1::bigint[])",
			"DELETE FROM user_update_events WHERE user_id=ANY($1::bigint[])",
			"DELETE FROM message_boxes WHERE owner_user_id=ANY($1::bigint[])",
			"DELETE FROM private_messages WHERE sender_user_id=ANY($1::bigint[])",
			"DELETE FROM dialogs WHERE user_id=ANY($1::bigint[])",
			"DELETE FROM users WHERE id=ANY($1::bigint[])",
		} {
			if _, err := tx.Exec(ctx, query, []int64{a.ID, b.ID}); err != nil {
				t.Error(err)
				return
			}
		}
		if err := tx.Commit(ctx); err != nil {
			t.Error(err)
		}
	})
	return a, b
}

func TestPrivateExternalReplyAllReadPathsAndReplay(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	a, b := externalReplyUsers(t, pool)
	messages := newTestMessageStore(pool)
	source, err := messages.SendPrivateText(ctx, domain.SendPrivateTextRequest{SenderUserID: a.ID, RecipientUserID: a.ID, RandomID: 1, Message: "a🌕 quote", Date: 1700000000, Entities: []domain.MessageEntity{{Type: domain.MessageEntityBold, Offset: 4, Length: 5}}, Media: &domain.MessageMedia{Kind: domain.MessageMediaKindContact, Contact: &domain.MessageContact{FirstName: "source", PhoneNumber: "123"}}})
	if err != nil {
		t.Fatal(err)
	}
	collision, err := messages.SendPrivateText(ctx, domain.SendPrivateTextRequest{SenderUserID: b.ID, RecipientUserID: b.ID, RandomID: 1, Message: "unrelated same ID", Date: 1700000000})
	if err != nil {
		t.Fatal(err)
	}
	if collision.SenderMessage.ID != source.SenderMessage.ID {
		t.Fatal("fixture must exercise owner-local ID collision")
	}
	req := domain.SendPrivateTextRequest{SenderUserID: a.ID, RecipientUserID: b.ID, RandomID: 2, Message: "external reply", Date: 1700000001, ReplyTo: &domain.MessageReply{Peer: domain.Peer{Type: domain.PeerTypeUser, ID: a.ID}, MessageID: source.SenderMessage.ID, QuoteText: "quote", QuoteOffset: 4}}
	res, err := messages.SendPrivateText(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	want, err := domain.NewMessageReplyExternal(source.SenderMessage)
	if err != nil {
		t.Fatal(err)
	}
	assertMessage := func(label string, m domain.Message) {
		t.Helper()
		if m.ReplyTo == nil || !reflect.DeepEqual(m.ReplyTo.External, want) || m.ReplyTo.QuoteText != "quote" || m.ReplyTo.QuoteOffset != 4 {
			t.Fatalf("%s lost snapshot: %+v", label, m.ReplyTo)
		}
		expectedID := source.SenderMessage.ID
		if m.OwnerUserID == b.ID {
			expectedID = 0
		}
		if m.ReplyTo.MessageID != expectedID {
			t.Fatalf("%s source ID=%d want=%d", label, m.ReplyTo.MessageID, expectedID)
		}
	}
	assertMessage("sender", res.SenderMessage)
	assertMessage("recipient", res.RecipientMessage)
	// Edit and delete the source using the normal transaction paths. Neither may
	// change the snapshot of a reply that has already committed.
	if _, err := messages.EditMessage(ctx, domain.EditMessageRequest{OwnerUserID: a.ID, Peer: source.SenderMessage.Peer, ID: source.SenderMessage.ID, Message: "changed source", EditDate: 1700000002}); err != nil {
		t.Fatal(err)
	}
	if _, err := messages.DeleteMessages(ctx, domain.DeleteMessagesRequest{OwnerUserID: a.ID, IDs: []int{source.SenderMessage.ID}, Date: 1700000003}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []domain.Message{res.SenderMessage, res.RecipientMessage} {
		freshStore := newTestMessageStore(pool)
		got, found, err := freshStore.GetByUID(ctx, m.OwnerUserID, m.UID)
		if err != nil || !found {
			t.Fatalf("get uid: %v", err)
		}
		assertMessage("uid", got)
		list, err := freshStore.GetByIDs(ctx, m.OwnerUserID, []int{m.ID})
		if err != nil || len(list.Messages) != 1 {
			t.Fatalf("get ids: %v", err)
		}
		assertMessage("ids", list.Messages[0])
		list, err = freshStore.ListByUser(ctx, m.OwnerUserID, domain.MessageFilter{HasPeer: true, Peer: m.Peer, Limit: 10})
		if err != nil || len(list.Messages) != 1 {
			t.Fatalf("history: %v", err)
		}
		assertMessage("history", list.Messages[0])
		dialogs, err := NewDialogStore(pool).ListByUser(ctx, m.OwnerUserID, domain.DialogFilter{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		found = false
		for _, top := range dialogs.Messages {
			if top.UID == m.UID {
				assertMessage("dialog", top)
				found = true
			}
		}
		if !found {
			t.Fatal("dialog missing reply")
		}
		events := NewUpdateEventStore(pool)
		diff, err := events.ListAfter(ctx, m.OwnerUserID, m.Pts-1, 1)
		if err != nil || len(diff) != 1 {
			t.Fatalf("difference: %v", err)
		}
		assertMessage("difference", diff[0].Message)
		batch, err := events.BatchByCursor(ctx, []store.EventCursor{{UserID: m.OwnerUserID, Pts: m.Pts}})
		if err != nil || len(batch) != 1 {
			t.Fatalf("egress batch: %v", err)
		}
		assertMessage("egress", batch[0].Message)
	}
	// A preflight miss can race a prior commit. The lock-protected duplicate
	// lookup must win over revalidation of the now-deleted reply source.
	req.IdempotencyPreflighted = true
	replay, err := messages.SendPrivateText(ctx, req)
	if err != nil || !replay.Duplicate || replay.SenderMessage.ID != res.SenderMessage.ID {
		t.Fatalf("post-preflight replay: %+v err=%v", replay, err)
	}
	assertMessage("replay", replay.SenderMessage)
	req.RandomID = 3
	if _, err := messages.SendPrivateText(ctx, req); !errors.Is(err, domain.ErrReplyMessageIDInvalid) {
		t.Fatalf("fresh deleted reply: %v", err)
	}
	// Corruption must fail all reconstruction boundaries, never silently erase
	// the reply. The isolated test repairs its deliberately corrupted row.
	encoded, err := domain.EncodeMessageReplyExternal(want)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE message_boxes SET reply_external='{"unexpected":true}' WHERE private_message_id=$1`, res.SenderMessage.UID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := messages.GetByUID(ctx, b.ID, res.SenderMessage.UID); err == nil {
		t.Fatal("bad snapshot accepted by GetByUID")
	}
	if _, err := messages.GetByIDs(ctx, b.ID, []int{res.RecipientMessage.ID}); err == nil {
		t.Fatal("bad snapshot accepted by GetByIDs")
	}
	if _, err := NewUpdateEventStore(pool).BatchByCursor(ctx, []store.EventCursor{{UserID: b.ID, Pts: res.RecipientMessage.Pts}}); err == nil {
		t.Fatal("bad snapshot accepted by Egress")
	}
	if _, err := pool.Exec(ctx, `UPDATE message_boxes SET reply_external=$2::jsonb WHERE private_message_id=$1`, res.SenderMessage.UID, encoded); err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "migrations", "20260908000002_private_external_reply.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot discard durable external reply") {
		t.Fatalf("down migration lost snapshots: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	// Once the sender deletes its output box, only the immutable receipt can
	// acknowledge a lost-response retry. Validate its nested external payload.
	if _, err := messages.DeleteMessages(ctx, domain.DeleteMessagesRequest{OwnerUserID: a.ID, IDs: []int{res.SenderMessage.ID}, Date: 1700000004}); err != nil {
		t.Fatal(err)
	}
	req.RandomID = 2
	replayed, err := messages.SendPrivateText(ctx, req)
	if err != nil || !replayed.Duplicate {
		t.Fatalf("replay after output delete: %v", err)
	}
	assertMessage("deleted-output replay", replayed.SenderMessage)
	var receipt []byte
	if err := pool.QueryRow(ctx, "SELECT sender_snapshot FROM private_messages WHERE sender_user_id=$1 AND id=$2", a.ID, res.SenderMessage.UID).Scan(&receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE private_messages SET sender_snapshot=jsonb_set(sender_snapshot,'{message,ReplyTo,External,from,Date}','0') WHERE sender_user_id=$1 AND id=$2`, a.ID, res.SenderMessage.UID); err != nil {
		t.Fatal(err)
	}
	if _, err := messages.SendPrivateText(ctx, req); err == nil {
		t.Fatal("corrupt deleted-output receipt replayed")
	}
	if _, err := pool.Exec(ctx, "UPDATE private_messages SET sender_snapshot=$3::jsonb WHERE sender_user_id=$1 AND id=$2", a.ID, res.SenderMessage.UID, receipt); err != nil {
		t.Fatal(err)
	}
}

func TestPrivateExternalReplyProtectedPairAndQuoteRejectAtomically(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	a, b := externalReplyUsers(t, pool)
	messages := newTestMessageStore(pool)
	source, err := messages.SendPrivateText(ctx, domain.SendPrivateTextRequest{SenderUserID: b.ID, RecipientUserID: a.ID, RandomID: 1, Message: "source 🌕 quote", Date: 1700000000})
	if err != nil {
		t.Fatal(err)
	}
	req := domain.SendPrivateTextRequest{SenderUserID: a.ID, RecipientUserID: a.ID, RandomID: 2, Message: "saved external", Date: 1700000001, ReplyTo: &domain.MessageReply{MessageID: source.RecipientMessage.ID, Peer: domain.Peer{Type: domain.PeerTypeUser, ID: b.ID}}}
	first, err := messages.SendPrivateText(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if first.SenderMessage.ReplyTo == nil || first.SenderMessage.ReplyTo.External == nil || first.SenderMessage.ReplyTo.MessageID != source.RecipientMessage.ID {
		t.Fatal("private-to-Saved source missing")
	}
	saved, err := messages.ListSavedDialogs(ctx, a.ID, domain.SavedDialogsFilter{Limit: 10})
	if err != nil || len(saved.Messages) != 1 || !reflect.DeepEqual(saved.Messages[0].ReplyTo.External, first.SenderMessage.ReplyTo.External) {
		t.Fatalf("Saved dialog snapshot: %+v %v", saved, err)
	}
	if _, err := messages.TogglePrivateNoForwards(ctx, domain.TogglePrivateNoForwardsRequest{ActorUserID: a.ID, PeerUserID: b.ID, Enabled: true, RandomID: 3, Date: 1700000002}); err != nil {
		t.Fatal(err)
	}
	counts := func() [3]int64 {
		t.Helper()
		var out [3]int64
		err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM private_messages WHERE sender_user_id=ANY($1::bigint[])),(SELECT count(*) FROM user_update_events WHERE user_id=ANY($1::bigint[])),(SELECT count(*) FROM dispatch_outbox WHERE target_user_id=ANY($1::bigint[]))`, []int64{a.ID, b.ID}).Scan(&out[0], &out[1], &out[2])
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := counts()
	req.RandomID = 4
	if _, err := messages.SendPrivateText(ctx, req); !errors.Is(err, domain.ErrChatForwardsRestricted) {
		t.Fatalf("pair protection=%v", err)
	}
	if counts() != before {
		t.Fatal("rejected reply mutated durable state")
	}
	req.RandomID = 2
	req.IdempotencyPreflighted = true
	if replay, err := messages.SendPrivateText(ctx, req); err != nil || !replay.Duplicate {
		t.Fatalf("exact replay after protection=%v", err)
	}
	if counts() != before {
		t.Fatal("replay mutated state")
	}
	// Message protection still permits an ordinary same-dialog reply.
	same := domain.SendPrivateTextRequest{SenderUserID: a.ID, RecipientUserID: b.ID, RandomID: 5, Message: "same-dialog", Date: 1700000003, ReplyTo: &domain.MessageReply{MessageID: source.RecipientMessage.ID}}
	if reply, err := messages.SendPrivateText(ctx, same); err != nil || reply.SenderMessage.ReplyTo.External != nil {
		t.Fatalf("same-dialog=%v", err)
	}
	if _, err := messages.TogglePrivateNoForwards(ctx, domain.TogglePrivateNoForwardsRequest{ActorUserID: a.ID, PeerUserID: b.ID, Enabled: false, RandomID: 6, Date: 1700000004}); err != nil {
		t.Fatal(err)
	}
	req.RandomID = 7
	req.ReplyTo = domain.CloneMessageReply(req.ReplyTo)
	req.ReplyTo.QuoteText = "invented"
	before = counts()
	if _, err := messages.SendPrivateText(ctx, req); !errors.Is(err, domain.ErrQuoteTextInvalid) {
		t.Fatalf("invalid quote=%v", err)
	}
	if counts() != before {
		t.Fatal("invalid quote mutated durable state")
	}
}

type replyBeginGate struct {
	*pgxpool.Pool
	entered, release chan struct{}
}

func (g *replyBeginGate) Begin(ctx context.Context) (pgx.Tx, error) {
	close(g.entered)
	select {
	case <-g.release:
		return g.Pool.Begin(ctx)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestPrivateExternalReplySourceDeletedBeforeTransaction(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, b := externalReplyUsers(t, pool)
	messages := newTestMessageStore(pool)
	source, err := messages.SendPrivateText(ctx, domain.SendPrivateTextRequest{SenderUserID: a.ID, RecipientUserID: a.ID, RandomID: 1, Message: "source", Date: 1700000000})
	if err != nil {
		t.Fatal(err)
	}
	gate := &replyBeginGate{Pool: pool, entered: make(chan struct{}), release: make(chan struct{})}
	blocked := newTestMessageStore(gate, WithMessageAllocators(testAllocatorsFor(pool).boxIDs))
	done := make(chan error, 1)
	go func() {
		_, err := blocked.SendPrivateText(ctx, domain.SendPrivateTextRequest{SenderUserID: a.ID, RecipientUserID: b.ID, RandomID: 2, Message: "race", Date: 1700000001, ReplyTo: &domain.MessageReply{MessageID: source.SenderMessage.ID, Peer: source.SenderMessage.Peer}})
		done <- err
	}()
	select {
	case <-gate.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := messages.DeleteMessages(ctx, domain.DeleteMessagesRequest{OwnerUserID: a.ID, IDs: []int{source.SenderMessage.ID}}); err != nil {
		close(gate.release)
		t.Fatal(err)
	}
	close(gate.release)
	if err := <-done; !errors.Is(err, domain.ErrReplyMessageIDInvalid) {
		t.Fatalf("source deleted before BEGIN must reject: %v", err)
	}
}
