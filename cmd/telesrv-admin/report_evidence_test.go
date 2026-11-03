package main

import (
	"encoding/json"
	"testing"

	"telesrv/internal/domain"
)

func TestEvidenceToChatDrawsReportedMessages(t *testing.T) {
	channelPost := domain.ModerationReportItem{
		Kind: domain.ModerationItemMessage,
		Peer: domain.Peer{Type: domain.PeerTypeChannel, ID: 60},
		Evidence: json.RawMessage(`{"schema_version":1,"channel_id":60,"message_id":30,"sender_user_id":7,
			"from":{"Type":"channel","ID":60},"date":1700000000,"post":true,"body":"bad post",
			"media":{"kind":"photo","photo":{"id":9,"access_hash":1,"sizes":[{"kind":"size","type":"y","w":1280,"h":720}]}}}`),
	}
	got := evidenceToChat(channelPost)
	if got.Message == nil || got.Message.ID != 30 || got.Message.Text != "bad post" || !got.Message.Post ||
		got.Message.FromType != "channel" || got.Message.FromID != 60 || got.Message.Media == nil || got.Message.Media.Full != "photo:9:y" {
		t.Fatalf("channel post = %+v / %+v", got, got.Message)
	}

	private := domain.ModerationReportItem{
		Kind: domain.ModerationItemMessage,
		Peer: domain.Peer{Type: domain.PeerTypeUser, ID: 5},
		Evidence: json.RawMessage(`{"schema_version":1,"message_id":12,"from":{"Type":"user","ID":5},"date":1700000001,
			"body":"spam","reply_to":{"MessageID":11},"forward":{"From":{"Type":"user","ID":8},"FromName":"Bob"}}`),
	}
	got = evidenceToChat(private)
	if got.Message == nil || got.Message.FromID != 5 || got.Message.ReplyToID != 11 || got.Message.FwdFromName != "Bob" {
		t.Fatalf("private = %+v", got.Message)
	}

	reaction := domain.ModerationReportItem{
		Kind:     domain.ModerationItemReaction,
		Evidence: json.RawMessage(`{"schema_version":1,"message":{"message_id":3,"from":{"Type":"user","ID":2},"date":5,"body":"hi"},"reactor_user_id":9}`),
	}
	if got = evidenceToChat(reaction); got.Message == nil || got.Message.Text != "hi" {
		t.Fatalf("reaction = %+v", got.Message)
	}

	peer := domain.ModerationReportItem{Kind: domain.ModerationItemPeer, Evidence: json.RawMessage(`{"schema_version":1}`)}
	if got = evidenceToChat(peer); got.Message != nil {
		t.Fatalf("peer report drew a message: %+v", got.Message)
	}
}
