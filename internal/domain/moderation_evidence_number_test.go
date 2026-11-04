package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCanonicalModerationEvidenceKeepsInt64Exact(t *testing.T) {
	raw := json.RawMessage(`{"media":{"photo":{"id":6926057846167314953}},"b":1,"a":2}`)
	out, err := CanonicalModerationEvidence(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "6926057846167314953") {
		t.Fatalf("id rounded: %s", out)
	}
	again, err := CanonicalModerationEvidence(out)
	if err != nil || string(again) != string(out) {
		t.Fatalf("not idempotent: %s vs %s (%v)", again, out, err)
	}
	if _, err := CanonicalModerationEvidence(json.RawMessage(`{"a":1} {"b":2}`)); err == nil {
		t.Fatal("trailing value accepted")
	}
}
