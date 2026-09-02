package maintenance

import (
	"context"
	"time"
)

// OrphanedMediaRetentionStore deletes documents/photos that have been
// orphaned (no live message/profile-photo/sticker-set reference remains,
// tracked via media_references + orphaned_at) for at least the configured
// age, along with their underlying blob once no other file_blobs row on
// its backend still needs it. Never touches media that still has a live
// reference, regardless of age.
type OrphanedMediaRetentionStore interface {
	DeleteOrphanedOlderThan(ctx context.Context, cutoff time.Time, limit int) (int, error)
}

// WithOrphanedMediaRetention enables the storage retention sweep. maxAge is
// how long a document/photo must have been orphaned before it's actually
// deleted (not how old the media itself is) -- <=0 leaves the sweep
// disabled even if a store is provided, matching
// TELESRV_STORAGE_RETENTION_MODE=off/orphan-with-no-age being the safe
// default. Mutually exclusive with WithHardMediaRetention -- the resolved
// TELESRV_STORAGE_RETENTION_MODE is one of "off"/"orphan"/"hard", never
// both orphan and hard sweeps at once.
func (w *RetentionWorker) WithOrphanedMediaRetention(store OrphanedMediaRetentionStore, maxAge time.Duration) *RetentionWorker {
	w.orphanedMedia = store
	w.orphanedMediaMaxAge = maxAge
	return w
}

// HardMediaRetentionStore physically deletes a document/photo's blob bytes
// once the media itself (its upload/created time, not how long it has been
// orphaned) is older than the configured age, REGARDLESS of whether a live
// message/profile-photo/sticker-set still references it. Unlike
// OrphanedMediaRetentionStore, it must never delete the document/photo
// metadata row -- only the underlying file_blobs row(s) and, once confirming
// no other row still needs the same content-addressed object, the physical
// bytes. This keeps a message rendering its media placeholder (dimensions,
// mime type, filename) after the bytes are gone; a subsequent download
// resolves to LOCATION_INVALID instead of the message breaking outright.
type HardMediaRetentionStore interface {
	DeleteBlobBytesForMediaOlderThan(ctx context.Context, cutoff time.Time, limit int) (int, error)
}

// WithHardMediaRetention enables the aggressive storage retention sweep
// (TELESRV_STORAGE_RETENTION_MODE=hard). maxAge is how old the media itself
// must be (its created_at, not an orphaned_at grace period) before its blob
// bytes are purged -- <=0 leaves the sweep disabled even if a store is
// provided. Mutually exclusive with WithOrphanedMediaRetention -- call at
// most one of the two, matching the config's single 3-way retention mode.
func (w *RetentionWorker) WithHardMediaRetention(store HardMediaRetentionStore, maxAge time.Duration) *RetentionWorker {
	w.hardMedia = store
	w.hardMediaMaxAge = maxAge
	return w
}
