package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestModerationReportMediaServesHeldFilesOnly(t *testing.T) {
	jpeg := append([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}, make([]byte, 64)...)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/moderation/reports/2":
			_, _ = w.Write([]byte(`{"ID":2,"Items":[],"MediaHolds":[{"ItemIndex":0,"Kind":"photo","StorageKey":"photo:6926057846167314953:m"}]}`))
		case "/v1/private-media":
			if r.URL.Query().Get("key") != "photo:6926057846167314953:m" {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(jpeg)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	srv, err := newServer(uiConfig{SessionKey: []byte("01234567890123456789012345678901"), AdminAPIURL: api.URL}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	get := func(key string) *httptest.ResponseRecorder {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /r/{id}/media", srv.handleModerationReportMediaAPI)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/r/2/media?key="+key, nil))
		return rec
	}
	if rec := get("photo:6926057846167314953:m"); rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("held photo = %d %q %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	if rec := get("photo:1:m"); rec.Code != http.StatusForbidden {
		t.Fatalf("unheld photo = %d", rec.Code)
	}
}

func TestResolveHeldKeyRecoversRoundedIDs(t *testing.T) {
	held := []string{"photo:6926057846167314953:m", "photo:6926057846167314953:x", "doc:6926057846167314953"}
	cases := map[string]string{
		"photo:6926057846167315000:m": "photo:6926057846167314953:m",
		"photo:6926057846167315000:x": "photo:6926057846167314953:x",
		"doc:6926057846167315000":     "doc:6926057846167314953",
		"photo:6926057846167314953:m": "photo:6926057846167314953:m",
		"photo:6926057846167315000:y": "photo:6926057846167315000:y", // no such size held
		"photo:1:m":                   "photo:1:m",
	}
	for in, want := range cases {
		if got := resolveHeldKey(in, held); got != want {
			t.Errorf("resolveHeldKey(%q) = %q, want %q", in, got, want)
		}
	}
}
