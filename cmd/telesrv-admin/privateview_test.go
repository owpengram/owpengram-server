package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestViewGrantIsBoundToSessionAndTarget(t *testing.T) {
	srv, err := newServer(uiConfig{SessionKey: []byte("01234567890123456789012345678901")}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	claims := sessionClaims{Actor: "alice", Nonce: "nonce-1", UserID: 7}
	grant, err := srv.signViewGrant(viewGrant{Actor: "alice", Nonce: "nonce-1", Kind: "user", ID: 42, Exp: time.Now().Add(time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}

	call := func(c sessionClaims, path, grant string) int {
		mux := http.NewServeMux()
		mux.Handle("GET /x/{id}", srv.requireViewGrant("user", func(w http.ResponseWriter, _ *http.Request, _ viewGrant) {
			w.WriteHeader(http.StatusOK)
		}))
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set(viewGrantHeader, grant)
		req = req.WithContext(context.WithValue(req.Context(), claimsKey{}, c))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := call(claims, "/x/42", grant); got != http.StatusOK {
		t.Fatalf("own grant on its target = %d, want 200", got)
	}
	if got := call(claims, "/x/43", grant); got != http.StatusForbidden {
		t.Fatalf("grant on another account = %d, want 403", got)
	}
	other := claims
	other.Nonce = "nonce-2"
	if got := call(other, "/x/42", grant); got != http.StatusForbidden {
		t.Fatalf("grant replayed from another session = %d, want 403", got)
	}
	if got := call(claims, "/x/42", grant+"x"); got != http.StatusForbidden {
		t.Fatalf("tampered grant = %d, want 403", got)
	}
	expired, _ := srv.signViewGrant(viewGrant{Actor: "alice", Nonce: "nonce-1", Kind: "user", ID: 42, Exp: time.Now().Add(-time.Second).Unix()})
	if got := call(claims, "/x/42", expired); got != http.StatusForbidden {
		t.Fatalf("expired grant = %d, want 403", got)
	}
}

func TestUnlockLimiterLocksAfterRepeatedMisses(t *testing.T) {
	l := &unlockLimiter{fails: map[string]int{}, until: map[string]time.Time{}}
	for i := 0; i < viewUnlockMaxFails; i++ {
		if l.locked("bob") {
			t.Fatalf("locked after %d misses", i)
		}
		l.record("bob", false)
	}
	if !l.locked("bob") {
		t.Fatal("not locked after the miss limit")
	}
	if l.locked("carol") {
		t.Fatal("one operator's misses locked another")
	}
}

func TestChatMediaFromJSON(t *testing.T) {
	photo := []byte(`{"kind":"photo","photo":{"id":9,"access_hash":1,"sizes":[
		{"kind":"stripped","type":"i","bytes":"AQI="},
		{"kind":"size","type":"s","w":90,"h":60},
		{"kind":"size","type":"m","w":320,"h":213},
		{"kind":"progressive","type":"y","w":1280,"h":853}]}}`)
	m, svc := chatMediaFromJSON(photo)
	if svc != "" || m == nil || m.Kind != "photo" || m.Thumb != "photo:9:m" || m.Full != "photo:9:y" || m.W != 1280 {
		t.Fatalf("photo = %+v %q", m, svc)
	}

	voice := []byte(`{"kind":"document","voice":true,"document":{"id":5,"access_hash":1,"mime_type":"audio/ogg","size":1000,
		"attributes":[{"kind":"audio","voice":true,"audio_duration":7}]}}`)
	m, _ = chatMediaFromJSON(voice)
	if m == nil || m.Kind != "voice" || m.Full != "doc:5" || m.Duration != 7 {
		t.Fatalf("voice = %+v", m)
	}

	sticker := []byte(`{"kind":"document","document":{"id":6,"access_hash":1,"mime_type":"application/x-tgsticker",
		"attributes":[{"kind":"sticker","alt":"😀"}],"thumbs":[{"kind":"size","type":"m","w":128,"h":128}]}}`)
	m, _ = chatMediaFromJSON(sticker)
	if m == nil || m.Kind != "sticker" || !m.Animated || m.Thumb != "doc:6:m" {
		t.Fatalf("sticker = %+v", m)
	}

	m, svc = chatMediaFromJSON([]byte(`{"kind":"service","service_action":{"kind":"pin_message"}}`))
	if m != nil || svc != "pin_message" {
		t.Fatalf("service = %+v %q", m, svc)
	}
	if m, svc = chatMediaFromJSON([]byte(`{}`)); m != nil || svc != "" {
		t.Fatalf("empty = %+v %q", m, svc)
	}
}

func TestPrivateMediaKeyPattern(t *testing.T) {
	for _, ok := range []string{"photo:1:y", "doc:12", "doc:12:m"} {
		if !privateMediaKeyPattern.MatchString(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "doc:", "photo:1", "upload:1:2", "doc:1:../x", "enc:5", "doc:1:m:extra"} {
		if privateMediaKeyPattern.MatchString(bad) {
			t.Errorf("%q allowed", bad)
		}
	}
}
