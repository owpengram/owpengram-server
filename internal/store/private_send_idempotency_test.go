package store

import (
	"bytes"
	"testing"

	"telesrv/internal/domain"
)

func TestPrivateSendSnapshotRejectsInvalidExternalReply(t *testing.T) {
	message := domain.Message{ID: 7, UID: 8, RandomID: 9, OwnerUserID: 10, Pts: 11,
		ReplyTo: &domain.MessageReply{MessageID: 1, External: &domain.MessageReplyExternal{
			From: domain.MessageForward{From: domain.Peer{Type: domain.PeerTypeUser, ID: 10}, Date: 12}, Text: "source",
		}},
	}
	raw, err := EncodePrivateSendSnapshot(message)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DecodePrivateSendSnapshot(raw); err != nil || got.ReplyTo.External.Text != "source" {
		t.Fatalf("valid external replay: %+v %v", got, err)
	}
	for _, mutation := range [][2][]byte{
		{[]byte(`"Date":12`), []byte(`"Date":0`)},
		{[]byte(`"text":"source"`), []byte(`"text":"source","unexpected":true`)},
	} {
		corrupt := bytes.Replace(raw, mutation[0], mutation[1], 1)
		if bytes.Equal(corrupt, raw) {
			t.Fatalf("mutation did not match %s", mutation[0])
		}
		if _, err := DecodePrivateSendSnapshot(corrupt); err == nil {
			t.Fatal("corrupt external receipt accepted")
		}
	}
	message.ReplyTo.External.From.Date = 0
	if _, err := EncodePrivateSendSnapshot(message); err == nil {
		t.Fatal("invalid external receipt written")
	}
}

func TestPrivateSendSnapshotIsDeepAndVersioned(t *testing.T) {
	message := domain.Message{
		ID: 7, UID: 8, RandomID: 9, OwnerUserID: 10,
		Peer: domain.Peer{Type: domain.PeerTypeUser, ID: 11},
		From: domain.Peer{Type: domain.PeerTypeUser, ID: 10},
		Date: 12, Out: true, Body: "first", Pts: 13,
		Entities: []domain.MessageEntity{{Type: domain.MessageEntityBold, Length: 5}},
		Media:    &domain.MessageMedia{Kind: domain.MessageMediaKindContact, Contact: &domain.MessageContact{PhoneNumber: "+1", FirstName: "Alice"}},
	}
	raw, err := EncodePrivateSendSnapshot(message)
	if err != nil {
		t.Fatalf("encode private snapshot: %v", err)
	}
	message.Body = "mutated"
	message.Entities[0].Length = 1
	message.Media.Contact.FirstName = "Mutated"
	decoded, err := DecodePrivateSendSnapshot(raw)
	if err != nil {
		t.Fatalf("decode private snapshot: %v", err)
	}
	if decoded.Body != "first" || decoded.Entities[0].Length != 5 || decoded.Media.Contact.FirstName != "Alice" {
		t.Fatalf("decoded private snapshot = %+v, want immutable nested graph", decoded)
	}
	decoded.Media.Contact.FirstName = "Second mutation"
	again, err := DecodePrivateSendSnapshot(raw)
	if err != nil || again.Media.Contact.FirstName != "Alice" {
		t.Fatalf("second decode = %+v err=%v, want fresh graph", again, err)
	}
}

func TestChannelSendSnapshotRejectsEmptyLegacyValue(t *testing.T) {
	if _, err := DecodeChannelSendSnapshot([]byte(`{}`)); err == nil {
		t.Fatal("empty legacy channel snapshot decoded successfully")
	}
	message := domain.ChannelMessage{
		ChannelID: 21, ID: 22, RandomID: 23, SenderUserID: 24,
		From: domain.Peer{Type: domain.PeerTypeUser, ID: 24},
		Date: 25, Body: "channel", Pts: 26,
	}
	raw, err := EncodeChannelSendSnapshot(message)
	if err != nil {
		t.Fatalf("encode channel snapshot: %v", err)
	}
	decoded, err := DecodeChannelSendSnapshot(raw)
	if err != nil || decoded.ChannelID != message.ChannelID || decoded.ID != message.ID || decoded.RandomID != message.RandomID || decoded.SenderUserID != message.SenderUserID || decoded.Body != message.Body || decoded.Pts != message.Pts {
		t.Fatalf("decoded channel snapshot = %+v err=%v, want %+v", decoded, err, message)
	}
}
