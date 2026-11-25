package rpc

import (
	"testing"

	"github.com/owpengram/owpengram-td/tg"

	"telesrv/internal/domain"
)

func TestTGSWithoutThumbsGetsPlaceholder(t *testing.T) {
	doc, ok := tgDocument(domain.Document{ID: 1, AccessHash: 2, MimeType: "application/x-tgsticker"}).(*tg.Document)
	if !ok || len(doc.Thumbs) != 1 {
		t.Fatalf("thumbs = %+v", doc)
	}
	if p, ok := doc.Thumbs[0].(*tg.PhotoPathSize); !ok || p.Type != "j" {
		t.Fatalf("placeholder = %#v", doc.Thumbs[0])
	}
	plain, _ := tgDocument(domain.Document{ID: 1, AccessHash: 2, MimeType: "application/zip"}).(*tg.Document)
	if len(plain.Thumbs) != 0 {
		t.Fatal("non-sticker file got a thumbnail")
	}
}

func TestAnimatedCustomEmojiProjectedAsAnimatedSticker(t *testing.T) {
	attrsOf := func(doc tg.DocumentClass) []tg.DocumentAttributeClass { return doc.(*tg.Document).Attributes }
	fileName := func(attrs []tg.DocumentAttributeClass) string {
		for _, a := range attrs {
			if f, ok := a.(*tg.DocumentAttributeFilename); ok {
				return f.FileName
			}
		}
		return ""
	}
	// An uploaded custom emoji keeps its own name in storage but reaches
	// clients as AnimatedSticker.tgs (Web K renders it as Lottie only then).
	emoji := tgDocument(domain.Document{ID: 1, AccessHash: 2, MimeType: "application/x-tgsticker", Attributes: []domain.DocumentAttribute{
		{Kind: domain.DocAttrFilename, FileName: "007_x.tgs"},
		{Kind: domain.DocAttrImageSize, W: 512, H: 512},
		{Kind: domain.DocAttrCustomEmoji, Alt: "💎", StickerSetID: 7, StickerSetAccessHash: 8},
	}})
	if got := fileName(attrsOf(emoji)); got != "AnimatedSticker.tgs" {
		t.Fatalf("custom emoji file name = %q", got)
	}
	// No filename at all: one is added.
	sticker := tgDocument(domain.Document{ID: 3, AccessHash: 4, MimeType: "application/x-tgsticker", Attributes: []domain.DocumentAttribute{
		{Kind: domain.DocAttrSticker, Alt: "😀"},
	}})
	if got := fileName(attrsOf(sticker)); got != "AnimatedSticker.tgs" {
		t.Fatalf("sticker without name = %q", got)
	}
	// A .tgs sent as a plain file and other documents keep their names.
	plainTGS := tgDocument(domain.Document{ID: 5, AccessHash: 6, MimeType: "application/x-tgsticker", Attributes: []domain.DocumentAttribute{
		{Kind: domain.DocAttrFilename, FileName: "my.tgs"},
	}})
	if got := fileName(attrsOf(plainTGS)); got != "my.tgs" {
		t.Fatalf("plain tgs file name = %q", got)
	}
	webp := tgDocument(domain.Document{ID: 7, AccessHash: 8, MimeType: "image/webp", Attributes: []domain.DocumentAttribute{
		{Kind: domain.DocAttrFilename, FileName: "s.webp"}, {Kind: domain.DocAttrSticker},
	}})
	if got := fileName(attrsOf(webp)); got != "s.webp" {
		t.Fatalf("webp sticker file name = %q", got)
	}
}
