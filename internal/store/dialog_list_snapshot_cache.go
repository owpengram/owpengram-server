package store

import (
	"context"

	"telesrv/internal/domain"
)

// DialogListSnapshotCacheKey identifies one all-built-in-folders owner header
// base. OwnerHash is the durable dialog_owner generation and is part of the
// shared cache key; shared channel generations are validated from Value.
type DialogListSnapshotCacheKey struct {
	UserID    int64
	OwnerHash int64
}

// DialogListSnapshotCacheValue is a domain-only materialized owner projection.
// It contains owner dialog facts and private top-message/peer payloads covered
// by dialog_owner plus the recorded channel_base dependency hash. Shared
// channel rows/top messages are hydrated through their channel-keyed caches so
// they are not duplicated once per member. Cloud drafts are owner-scoped and
// covered by dialog_owner, so they are materialized with Dialogs; presence,
// privacy, notify settings and TL values remain response-time overlays.
type DialogListSnapshotCacheValue struct {
	DependencyHash int64
	Dialogs        []domain.Dialog
	Messages       []domain.Message
	Users          []domain.User
	State          domain.UpdateState
	ArchiveSummary *domain.DialogArchiveSummary
}

// DialogListSnapshotCache is a rebuildable shared L2. Transport, decode and
// write failures are returned to the caller so production does not silently
// stampede PostgreSQL through an unversioned fallback.
type DialogListSnapshotCache interface {
	GetDialogListSnapshot(context.Context, DialogListSnapshotCacheKey) (DialogListSnapshotCacheValue, bool, error)
	PutDialogListSnapshot(context.Context, DialogListSnapshotCacheKey, DialogListSnapshotCacheValue) error
}
