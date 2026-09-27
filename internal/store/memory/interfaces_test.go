package memory

import "telesrv/internal/store"

// CodeStore, EphemeralMessageStore and EphemeralPushBroker predate this
// package's Redis-replacement work and never carried an explicit interface
// assertion. Every other type added to replace internal/store/redisstore/
// already asserts against its interface right next to its definition (see
// active_channel_ids_page.go, dialog_list_snapshot.go, user_cache.go,
// ratelimit.go, id_allocator.go, bot_callback.go, inline_registry.go) --
// this file exists only to close that one remaining gap, not to duplicate
// those.
var (
	_ store.CodeStore             = (*CodeStore)(nil)
	_ store.EphemeralMessageStore = (*EphemeralMessageStore)(nil)
	_ store.EphemeralPushBroker   = (*EphemeralMessageStore)(nil)
)
