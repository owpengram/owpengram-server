package files

import "testing"

func TestParseMessageBoxRefKeyRoundTrip(t *testing.T) {
	owner, box, ok := parseMessageBoxRefKey("user:42:box:7")
	if !ok || owner != 42 || box != 7 {
		t.Fatalf("parse = (%d, %d, %v), want (42, 7, true)", owner, box, ok)
	}
	malformed := []string{"", "garbage", "user:1:box:", "user:1:box:2extra", "user:0:box:1", "user:1:box:0", "channel:1:msg:2"}
	for _, key := range malformed {
		if _, _, ok := parseMessageBoxRefKey(key); ok {
			t.Fatalf("parseMessageBoxRefKey(%q) ok=true, want false", key)
		}
	}
}

func TestParseChannelMessageRefKeyRoundTrip(t *testing.T) {
	channel, msg, ok := parseChannelMessageRefKey("channel:99:msg:3")
	if !ok || channel != 99 || msg != 3 {
		t.Fatalf("parse = (%d, %d, %v), want (99, 3, true)", channel, msg, ok)
	}
	malformed := []string{"", "garbage", "channel:1:msg:", "channel:1:msg:2extra", "channel:0:msg:1", "channel:1:msg:0", "user:1:box:2"}
	for _, key := range malformed {
		if _, _, ok := parseChannelMessageRefKey(key); ok {
			t.Fatalf("parseChannelMessageRefKey(%q) ok=true, want false", key)
		}
	}
}
