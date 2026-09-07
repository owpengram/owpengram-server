package postgres

import (
	"context"
	"testing"

	"telesrv/internal/domain"
)

// TestFirstContactMessageThenGetPeerDialogsReturnsUsableDialog reproduces the reported
// client symptom (Android + desktop, both stock-derived): a first-ever message from a
// sender with no prior dialog fires a notification, but the new dialog does not appear
// in the recipient's dialog list until app restart. The client's live-update path
// (updateInterfaceWithMessages) builds a dialog in-memory then confirms it via
// messages.getPeerDialogs (DialogStore.ListByPeers server-side). This test drives that
// exact server-side path end to end against real Postgres and dumps every field a stock
// client needs to accept and render the dialog, to catch anything subtly missing/wrong
// that unit tests against fakes wouldn't.
func TestFirstContactMessageThenGetPeerDialogsReturnsUsableDialog(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)

	users := NewUserStore(pool)
	sender, err := users.Create(ctx, domain.User{AccessHash: 31, Phone: "+1667" + suffix + "01", FirstName: "Sender"})
	if err != nil {
		t.Fatalf("create sender: %v", err)
	}
	recipient, err := users.Create(ctx, domain.User{AccessHash: 32, Phone: "+1667" + suffix + "02", FirstName: "Recipient"})
	if err != nil {
		t.Fatalf("create recipient: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1::bigint[])", []int64{sender.ID, recipient.ID})
	})

	messages := NewMessageStore(pool)
	var originAuthKeyID [8]byte
	originAuthKeyID[0] = 9
	sendRes, err := messages.SendPrivateText(ctx, domain.SendPrivateTextRequest{
		SenderUserID:    sender.ID,
		RecipientUserID: recipient.ID,
		RandomID:        555111,
		Message:         "hey, first message ever",
		Date:            1700005000,
		OriginAuthKeyID: originAuthKeyID,
		OriginSessionID: 41,
	})
	if err != nil {
		t.Fatalf("SendPrivateText: %v", err)
	}
	t.Logf("sender box: %+v", sendRes.SenderMessage)
	t.Logf("recipient box: %+v", sendRes.RecipientMessage)

	dialogs := NewDialogStore(pool)
	peer := domain.Peer{Type: domain.PeerTypeUser, ID: sender.ID}
	list, err := dialogs.ListByPeers(ctx, recipient.ID, []domain.Peer{peer})
	if err != nil {
		t.Fatalf("ListByPeers (recipient's view of sender): %v", err)
	}
	t.Logf("dialog list: %+v", list)
	if len(list.Dialogs) != 1 {
		t.Fatalf("dialogs = %d, want exactly 1 (the freshly-created dialog with sender)", len(list.Dialogs))
	}
	d := list.Dialogs[0]
	t.Logf("dialog: %+v", d)
	if d.Peer != peer {
		t.Fatalf("dialog peer = %+v, want %+v", d.Peer, peer)
	}
	if d.TopMessage == 0 {
		t.Fatalf("dialog.TopMessage = 0, want the recipient's box id for the new message")
	}
	if d.TopMessage != sendRes.RecipientMessage.ID {
		t.Fatalf("dialog.TopMessage = %d, want recipient box id %d", d.TopMessage, sendRes.RecipientMessage.ID)
	}
	if len(list.Messages) != 1 {
		t.Fatalf("messages returned = %d, want 1 (top message content, required for stock client to accept the dialog)", len(list.Messages))
	}
	msg := list.Messages[0]
	t.Logf("message: %+v", msg)
	if msg.ID != d.TopMessage {
		t.Fatalf("message.ID = %d, does not match dialog.TopMessage = %d -- stock client discards a dialog whose top message it can't resolve", msg.ID, d.TopMessage)
	}
	if len(list.Users) == 0 {
		t.Fatalf("users returned = 0, want at least the sender's User object (client needs it to render the dialog title/avatar)")
	}
	foundSender := false
	for _, u := range list.Users {
		if u.ID == sender.ID {
			foundSender = true
		}
	}
	if !foundSender {
		t.Fatalf("users = %+v, want sender %d present", list.Users, sender.ID)
	}
}
