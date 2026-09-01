package store

import "context"

// ActiveChannelIDsPageKey identifies one immutable page of the durable
// owner-scoped active membership read model. Generation is the exact
// channel_active_memberships hash, or the documented missing-generation
// sentinel used before an owner has any membership row.
type ActiveChannelIDsPageKey struct {
	UserID         int64
	Generation     int64
	AfterChannelID int64
	Limit          int
}

// ActiveChannelIDsPageCache is a rebuildable shared L2. Redis errors are
// returned so callers cannot silently turn an outage into a PostgreSQL
// stampede.
type ActiveChannelIDsPageCache interface {
	GetActiveChannelIDsPage(context.Context, ActiveChannelIDsPageKey) ([]int64, bool, error)
	PutActiveChannelIDsPage(context.Context, ActiveChannelIDsPageKey, []int64) error
}

// ActiveChannelIDsPageLoader is the bounded authoritative cold source used
// only after a shared-cache miss.
type ActiveChannelIDsPageLoader interface {
	ListActiveChannelIDsForUser(context.Context, int64, int64, int) ([]int64, error)
}
