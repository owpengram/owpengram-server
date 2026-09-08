package domain

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestExternalReplySnapshotIsolationAndInvalidPayload(t *testing.T) {
	source := Message{From: Peer{Type: PeerTypeUser, ID: 42}, Date: 1700000000, Body: "🌕 quote", Media: &MessageMedia{Kind: MessageMediaKindPhoto, Photo: &Photo{ID: 7, FileReference: []byte{1, 2, 3}}}}
	x, err := NewMessageReplyExternal(source)
	if err != nil {
		t.Fatal(err)
	}
	source.Media.Photo.FileReference[0] = 9
	if x.Media.Photo.FileReference[0] != 1 {
		t.Fatal("source can mutate persisted snapshot")
	}
	r := &MessageReply{External: x}
	if err := ValidateMessageReplyBounds(r); err != nil {
		t.Fatal("snapshot-only recipient header rejected", err)
	}
	copy := CloneMessageReply(r)
	copy.External.Media.Photo.FileReference[0] = 8
	if x.Media.Photo.FileReference[0] != 1 {
		t.Fatal("owner clone aliases media snapshot")
	}
	b, err := EncodeMessageReplyExternal(x)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecodeMessageReplyExternal(b)
	if err != nil || !reflect.DeepEqual(restored, x) {
		t.Fatalf("snapshot roundtrip: %v", err)
	}
	for _, bad := range []string{"null", "[]", `{"from":{}}`, string(b) + ` {}`, strings.Replace(string(b), `"text":`, `"unrecognized":1,"text":`, 1), strings.Repeat("x", MaxMessageReplyExternalBytes+1)} {
		if _, err := DecodeMessageReplyExternal([]byte(bad)); err == nil {
			t.Fatalf("invalid external snapshot accepted: %.50s", bad)
		}
	}
}

func TestExternalReplyQuoteUsesUTF16AndExactSubstring(t *testing.T) {
	for _, tc := range []struct {
		text, quote string
		offset      int
		valid       bool
	}{
		{"a🌕 quote", "quote", 4, true}, {"a🌕 quote", "quote", 3, false}, {"a🌕 quote", "🌕", 1, true}, {"a🌕 quote", "🌕", 2, false}, {"source", "invented", 0, false}, {"source", "source", 0, true}, {"source", "", 0, true}, {"source", "", 1, false},
	} {
		err := ValidateExternalReplyQuote(&MessageReply{QuoteText: tc.quote, QuoteOffset: tc.offset}, tc.text)
		if (err == nil) != tc.valid || (err != nil && !errors.Is(err, ErrQuoteTextInvalid)) {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
	text := strings.Repeat("🌕", 3000) + "quote"
	if err := ValidateExternalReplyQuote(&MessageReply{QuoteText: "quote", QuoteOffset: 6000}, text); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMessageReplyBounds(&MessageReply{MessageID: 1, QuoteOffset: 6000}); err != nil {
		t.Fatal(err)
	}
}

func TestExternalReplyDocumentNestedSlicesRemainIndependent(t *testing.T) {
	source := Message{From: Peer{Type: PeerTypeUser, ID: 42}, Date: 1700000000,
		Media: &MessageMedia{Kind: MessageMediaKindDocument, Document: &Document{
			ID: 7, FileReference: []byte{1, 2, 3}, MimeType: "audio/ogg",
			Attributes: []DocumentAttribute{{Kind: DocAttrAudio, Voice: true, Waveform: []byte{4, 5, 6}}},
		}},
	}
	x, err := NewMessageReplyExternal(source)
	if err != nil {
		t.Fatal(err)
	}
	source.Media.Document.FileReference[0] = 9
	source.Media.Document.Attributes[0].Waveform[0] = 9
	copy := CloneMessageReply(&MessageReply{External: x})
	copy.External.Media.Document.Attributes[0].Waveform[1] = 9
	if !reflect.DeepEqual(x.Media.Document.FileReference, []byte{1, 2, 3}) || !reflect.DeepEqual(x.Media.Document.Attributes[0].Waveform, []byte{4, 5, 6}) {
		t.Fatal("source or another owner mutated the document snapshot")
	}
	raw, err := EncodeMessageReplyExternal(x)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeMessageReplyExternal(raw)
	if err != nil || !reflect.DeepEqual(got, x) {
		t.Fatalf("document snapshot roundtrip: %+v %v", got, err)
	}
}
