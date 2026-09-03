package domain

import "time"

// MediaKind identifies which table a media_references row points into.
type MediaKind string

const (
	MediaKindDocument MediaKind = "document"
	MediaKindPhoto    MediaKind = "photo"
)

// MediaRefKind identifies why a document/photo is still considered "live" --
// each distinct kind of place that can hold a durable pointer to media gets
// its own ref_kind, so removing one kind of reference (e.g. a deleted
// message) doesn't accidentally drop a still-live reference of another kind
// (e.g. the same document also set as someone's profile photo).
type MediaRefKind string

const (
	MediaRefKindMessageBox     MediaRefKind = "message_box"
	MediaRefKindChannelMessage MediaRefKind = "channel_message"
	MediaRefKindProfilePhoto   MediaRefKind = "profile_photo"
	MediaRefKindStickerSet     MediaRefKind = "sticker_set"
	MediaRefKindGift           MediaRefKind = "gift"
)

// MediaReference is one live pointer to a document/photo. GC (the storage
// retention sweep) only considers a document/photo eligible for deletion
// once every reference to it has been removed.
type MediaReference struct {
	Kind    MediaKind
	MediaID int64
	RefKind MediaRefKind
	RefKey  string
}

// OrphanCandidate is a document/photo whose last reference has been removed
// (orphaned_at is set) and is old enough to be considered for the storage
// retention sweep.
type OrphanCandidate struct {
	Kind       MediaKind
	MediaID    int64
	Backend    MediaBackend
	ObjectKey  string
	Size       int64
	OrphanedAt int64 // unix seconds
}

// EvictionCandidate is one document/photo still owning file_blobs bytes,
// used by the active-eviction sweep (TELESRV_STORAGE_EVICTION_ENABLE) to pick
// the oldest media overall across both tables once total physical storage
// exceeds TELESRV_STORAGE_MAX_TOTAL_BYTES.
type EvictionCandidate struct {
	Kind      MediaKind
	MediaID   int64
	CreatedAt time.Time
}

// MediaRefTarget identifies one document/photo embedded in a message's media
// snapshot.
type MediaRefTarget struct {
	Kind MediaKind
	ID   int64
}

// ExtractMediaRefTargets returns every document/photo id embedded in a
// message's media snapshot (including nested ones -- a live photo's video
// document, a webpage preview's photo), deduplicated. Used to register/drop
// media_references rows when a message carrying this media is
// created/edited/deleted.
func ExtractMediaRefTargets(media *MessageMedia) []MediaRefTarget {
	if media == nil {
		return nil
	}
	seen := make(map[MediaRefTarget]struct{}, 2)
	var out []MediaRefTarget
	add := func(kind MediaKind, id int64) {
		if id == 0 {
			return
		}
		t := MediaRefTarget{Kind: kind, ID: id}
		if _, ok := seen[t]; ok {
			return
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	if media.Photo != nil {
		add(MediaKindPhoto, media.Photo.ID)
	}
	if media.Document != nil {
		add(MediaKindDocument, media.Document.ID)
	}
	if media.LivePhotoVideo != nil {
		add(MediaKindDocument, media.LivePhotoVideo.ID)
	}
	if media.WebPage != nil && media.WebPage.Photo != nil {
		add(MediaKindPhoto, media.WebPage.Photo.ID)
	}
	return out
}
