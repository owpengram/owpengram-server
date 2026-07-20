package rpc

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"testing"
	"time"
)

func TestExportAuthTokenRoundTrip(t *testing.T) {
	const userID = int64(1780243201)
	token := signExportAuthToken(userID)
	got, err := verifyExportAuthToken(token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got != userID {
		t.Fatalf("got userID %d, want %d", got, userID)
	}
}

func TestExportAuthTokenRejectsTampering(t *testing.T) {
	token := signExportAuthToken(1780243201)
	token[0] ^= 0xFF
	if _, err := verifyExportAuthToken(token); err == nil {
		t.Fatal("expected tampered token to fail verification")
	}
}

func TestExportAuthTokenRejectsWrongLength(t *testing.T) {
	if _, err := verifyExportAuthToken([]byte{1, 2, 3}); err == nil {
		t.Fatal("expected short token to fail verification")
	}
}

func TestExportAuthTokenRejectsExpired(t *testing.T) {
	// Build a token with an already-past expiry, signed the same way
	// signExportAuthToken does, to exercise the expiry check specifically
	// (as opposed to the HMAC check).
	payload := make([]byte, 16, 48)
	binary.BigEndian.PutUint64(payload[0:8], uint64(1780243201))
	binary.BigEndian.PutUint64(payload[8:16], uint64(time.Now().Add(-time.Minute).Unix()))
	mac := hmac.New(sha256.New, exportAuthSecret)
	mac.Write(payload)
	token := mac.Sum(payload)

	if _, err := verifyExportAuthToken(token); err == nil {
		t.Fatal("expected expired token to fail verification")
	}
}
