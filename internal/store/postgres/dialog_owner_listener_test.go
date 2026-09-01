package postgres

import (
	"testing"

	"telesrv/internal/domain"
)

type dialogOwnerListenerCache struct {
	owners []int64
}

func (*dialogOwnerListenerCache) InvalidateDialog(int64, domain.Peer) {}
func (*dialogOwnerListenerCache) FlushReadModelCache()                {}
func (c *dialogOwnerListenerCache) InvalidateDialogOwner(ownerUserID int64) {
	c.owners = append(c.owners, ownerUserID)
}

func TestReadModelListenerInvalidatesExactDialogOwner(t *testing.T) {
	cache := &dialogOwnerListenerCache{}
	listener := NewReadModelChangeListener("", ReadModelCacheSet{Dialogs: cache}, nil)
	listener.handlePayload(`{"model":"dialog_owner","owner_user_id":1001,"peer_type":"user","peer_id":1001,"version":2,"hash":44}`)
	if len(cache.owners) != 1 || cache.owners[0] != 1001 {
		t.Fatalf("invalidated owners = %v, want [1001]", cache.owners)
	}
}
