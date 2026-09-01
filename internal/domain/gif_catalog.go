package domain

import (
	"errors"
	"time"
)

var (
	// ErrGifCatalogUnavailable is returned by the admin GIF-catalog write path
	// when no store.GifCatalogStore was configured (files.WithGifCatalog).
	ErrGifCatalogUnavailable = errors.New("gif catalog is not configured")
	// ErrGifCatalogFileInvalid is returned when an uploaded file isn't a GIF
	// or MP4, or exceeds MaxGifCatalogUploadSize.
	ErrGifCatalogFileInvalid = errors.New("gif catalog file invalid")
	// ErrGifCatalogEntryInvalid is returned for a title that fails validation
	// or a document_id that doesn't resolve to an uploaded document.
	ErrGifCatalogEntryInvalid = errors.New("gif catalog entry invalid")
	// ErrGifCatalogEntryNotFound is returned by an update/delete against an id
	// that doesn't exist.
	ErrGifCatalogEntryNotFound = errors.New("gif catalog entry not found")
	// ErrGifCatalogFull is returned when a create would push the catalog past
	// MaxGifCatalogEntries.
	ErrGifCatalogFull = errors.New("gif catalog is full")
)

const (
	// MaxGifCatalogTitleLen bounds an admin-entered catalog entry title.
	MaxGifCatalogTitleLen = 128
	// MaxGifCatalogEntries caps how many entries @gif serves in one inline
	// response -- mirrors MaxBotInlineResults, the TL-level cap the client
	// itself enforces per messages.getInlineBotResults response.
	MaxGifCatalogEntries = MaxBotInlineResults
	// MaxGifCatalogUploadSize bounds one admin-uploaded or seed-imported
	// catalog file (the raw GIF/MP4 before transcoding, not the smaller MP4
	// files.Service.AdminUploadGifMaterial produces from it).
	//
	// Deliberately its own constant, not MaxBotInlineWebSize: that bounds
	// content a *client* submits as an inline result, an unrelated
	// constraint. This one instead matches
	// files.gifTranscodeMaxInputBytes -- the actual ceiling the ffmpeg
	// transcoder accepts -- so a real download (unoptimized meme/reaction
	// GIFs routinely run 20-50MB, frame-by-frame GIF encoding is notoriously
	// space-inefficient) doesn't get rejected here before ever reaching a
	// step that could actually handle it.
	MaxGifCatalogUploadSize = 50 << 20
)

// GifCatalogCategories are the valid values for GifCatalogEntry.Category --
// exactly the titles internal/seed/catalog/emoji_groups.json uses, so a
// category tap in the client's GIF picker (see files.ClassifyGifCategory and
// bots.rankGifCatalogEntries) maps onto them with no translation step.
var GifCatalogCategories = []string{
	"Love", "Approval", "Disapproval", "Cheers", "Laughter",
	"Astonishment", "Sadness", "Anger", "Neutral", "Doubt", "Silly",
}

// ValidGifCatalogCategory reports whether category is "" (uncategorized) or
// one of GifCatalogCategories.
func ValidGifCatalogCategory(category string) bool {
	if category == "" {
		return true
	}
	for _, c := range GifCatalogCategories {
		if c == category {
			return true
		}
	}
	return false
}

// GifCatalogEntry is one admin-curated GIF served by the built-in @gif inline
// bot (see rpc.ServiceBotInlineResults) for the client's GIF picker
// trending/search panel. DocumentID references an already-uploaded document
// (see files.Service.AdminUploadGifMaterial) -- the catalog only tracks which
// documents are featured and in what order, it does not own the media itself.
type GifCatalogEntry struct {
	ID         int64
	Title      string
	DocumentID int64
	Enabled    bool
	SortOrder  int
	CreatedBy  string
	// Category is one of GifCatalogCategories, or "" if uncategorized. Set
	// manually via the admin panel or in bulk via files.ClassifyGifCategory.
	Category string
	// SourceFilename is set only for entries files.Service.SeedGifs imported
	// from the data/gifs/ drop directory -- empty for anything created through
	// the admin panel. It exists purely so a restart can tell "this file was
	// already imported" from "this is a new file" without re-transcoding
	// every GIF on every startup; it carries no meaning once imported.
	SourceFilename string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
