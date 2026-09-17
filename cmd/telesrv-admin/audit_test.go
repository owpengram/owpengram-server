package main

import "testing"

func TestParseAuditLogLimit(t *testing.T) {
	if got, err := parseAuditLogLimit("", 200); err != nil || got != 200 {
		t.Fatalf("empty limit = %d, %v, want 200, nil", got, err)
	}
	if got, err := parseAuditLogLimit("50", 200); err != nil || got != 50 {
		t.Fatalf("limit=50 = %d, %v, want 50, nil", got, err)
	}
	if got, err := parseAuditLogLimit("9999", 200); err != nil || got != 200 {
		t.Fatalf("limit above max = %d, %v, want capped at 200, nil", got, err)
	}
	if _, err := parseAuditLogLimit("0", 200); err == nil {
		t.Fatal("limit=0 accepted, want rejection")
	}
	if _, err := parseAuditLogLimit("-5", 200); err == nil {
		t.Fatal("negative limit accepted, want rejection")
	}
	if _, err := parseAuditLogLimit("not-a-number", 200); err == nil {
		t.Fatal("non-numeric limit accepted, want rejection")
	}
}
