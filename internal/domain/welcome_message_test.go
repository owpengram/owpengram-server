package domain

import (
	"errors"
	"testing"
)

func TestWelcomeMessageContentAndFingerprint(t *testing.T) {
	peer := Peer{Type: PeerTypeChannel, ID: 42}
	content := WelcomeMessageContent{Message: "Welcome 👋"}
	if err := content.Validate(); err != nil {
		t.Fatalf("valid content: %v", err)
	}
	first, err := WelcomeCreateFingerprint(peer, 7, 99, content)
	if err != nil {
		t.Fatal(err)
	}
	second, err := WelcomeCreateFingerprint(peer, 7, 99, content)
	if err != nil || first != second || first == ([32]byte{}) {
		t.Fatalf("deterministic fingerprint = %x/%x err=%v", first, second, err)
	}
	changed, err := WelcomeCreateFingerprint(peer, 7, 99, WelcomeMessageContent{Message: "Different"})
	if err != nil || changed == first {
		t.Fatalf("changed fingerprint = %x err=%v", changed, err)
	}
	if err := (WelcomeMessageContent{Message: "text", InvertMedia: true}).Validate(); !errors.Is(err, ErrWelcomeMessageInvalid) {
		t.Fatalf("invert without media err = %v", err)
	}
}

func TestWelcomeMessageEditFields(t *testing.T) {
	current := WelcomeMessageContent{Message: "before", NoForwards: true}
	updated, err := (WelcomeMessageEditFields{
		SetMessage: true, Message: "after", SetEntities: true,
	}).Apply(current)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Message != "after" || !updated.NoForwards || len(updated.Entities) != 0 {
		t.Fatalf("updated content = %+v", updated)
	}
	if _, err := (WelcomeMessageEditFields{}).Apply(current); !errors.Is(err, ErrWelcomeMessageInvalid) {
		t.Fatalf("empty edit err = %v", err)
	}
}

func TestNextWelcomeRevision(t *testing.T) {
	if next, err := NextWelcomeRevision(InitialWelcomeRevision); err != nil || next != 2 {
		t.Fatalf("next revision = %d,%v", next, err)
	}
	if _, err := NextWelcomeRevision(0); !errors.Is(err, ErrWelcomeMessageRevisionOverflow) {
		t.Fatalf("zero revision err = %v", err)
	}
}
