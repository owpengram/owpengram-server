package domain

import (
	"errors"
	"time"
)

// ErrGiftPackNotFound is returned when no uploaded pack carries the
// requested id.
var ErrGiftPackNotFound = errors.New("gift pack not found")

// MaxGiftPackArchiveBytes bounds one uploaded pack archive. The per-file
// ceilings inside it are tighter still (see internal/app/giftpack and
// StarGift animation limits); this only stops an absurd upload before it
// reaches the store.
const MaxGiftPackArchiveBytes = 32 << 20

// GiftPack is one uploaded gift-pack archive in the operator's library.
// Nothing is imported into the catalog by uploading -- a pack sits here,
// previewable, until an operator imports the whole thing or a single gift
// out of it.
type GiftPack struct {
	// PackID is the pack name reduced to a URL slug; re-uploading a pack
	// with the same name replaces the stored archive rather than adding a
	// second copy of it.
	PackID      string
	Name        string
	Author      string
	Description string
	FileName    string
	GiftCount   int
	SizeBytes   int
	// Manifest is the archive's raw pack.json, kept alongside the archive so
	// listing and previewing a pack never has to unzip megabytes.
	Manifest []byte
	// Archive is the raw .zip. Empty in list results, which read metadata
	// only.
	Archive    []byte
	UploadedBy string
	UploadedAt time.Time
}
