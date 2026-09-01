package store

import "context"

// BoxIDAllocator 分配用户视角 message box id。box_id 允许空洞，但不能回退。
type BoxIDAllocator interface {
	NextBoxID(ctx context.Context, userID int64) (int, error)
	// NextBoxIDs allocates one monotonic id for every distinct user in one
	// backend batch. Implementations reject invalid users before allocation and
	// must not turn this operation into a per-user network loop.
	NextBoxIDs(ctx context.Context, userIDs []int64) (map[int64]int, error)
	CurrentBoxID(ctx context.Context, userID int64) (int, error)
}

// DistributedBoxIDAllocator marks an allocator whose per-owner reservations
// remain atomic across processes and are safe before a PostgreSQL transaction.
// The private-send microbatch path requires this capability; local and test
// allocators stay on the single-command transaction path.
type DistributedBoxIDAllocator interface {
	BoxIDAllocator
	DistributedBoxIDAllocation()
}

// CounterSource 用于 Redis 计数器冷启动时从 PostgreSQL durable log 恢复当前值。
type CounterSource interface {
	Current(ctx context.Context, userID int64) (int, error)
	CurrentBatch(ctx context.Context, userIDs []int64) (map[int64]int, error)
}
