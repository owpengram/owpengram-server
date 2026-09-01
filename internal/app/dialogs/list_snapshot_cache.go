package dialogs

import (
	"context"
	"encoding/binary"
	"hash/fnv"
	"sync"
	"time"

	"telesrv/internal/app/readmodel"
	"telesrv/internal/domain"
	"telesrv/internal/readmodelcache"
)

const (
	dialogListSnapshotTTL        = 5 * time.Minute
	dialogListSnapshotMaxEntries = 10000
	dialogListSnapshotMaxHeaders = 1000000
	dialogListSnapshotLoadLimit  = 10000
)

type dialogListSnapshotKey struct {
	userID int64
}

type dialogListSnapshot struct {
	dialogs        []domain.Dialog
	messages       []domain.Message
	users          []domain.User
	hash           int64
	state          domain.UpdateState
	archive        *domain.DialogArchiveSummary
	channelIDs     []int64
	ownerHash      int64
	dependencyHash int64
}

type dialogListSnapshotCache struct {
	cache *readmodelcache.Cache[dialogListSnapshotKey, *dialogListSnapshot]

	indexMu     sync.Mutex
	channelKeys map[int64]map[dialogListSnapshotKey]struct{}
	keyChannels map[dialogListSnapshotKey][]int64
}

func newDialogListSnapshotCache(maxEntries int, maxHeaders int64, ttl time.Duration) *dialogListSnapshotCache {
	if maxEntries <= 0 {
		maxEntries = dialogListSnapshotMaxEntries
	}
	if maxHeaders <= 0 {
		maxHeaders = dialogListSnapshotMaxHeaders
	}
	if ttl <= 0 {
		ttl = dialogListSnapshotTTL
	}
	c := &dialogListSnapshotCache{
		channelKeys: make(map[int64]map[dialogListSnapshotKey]struct{}),
		keyChannels: make(map[dialogListSnapshotKey][]int64),
	}
	c.cache = readmodelcache.New[dialogListSnapshotKey, *dialogListSnapshot](readmodelcache.Config[dialogListSnapshotKey, *dialogListSnapshot]{
		MaxEntries: maxEntries,
		MaxWeight:  maxHeaders,
		Weight: func(snap *dialogListSnapshot) int64 {
			if snap == nil {
				return 1
			}
			// The historical knob is expressed in header-equivalent units. A
			// materialized message/channel is wider than an ordering header, so
			// charge conservative multiples and keep the old global bound useful.
			memberProjections := 0
			for _, dialog := range snap.dialogs {
				if dialog.ChannelMember != nil {
					memberProjections++
				}
			}
			weight := len(snap.dialogs) + memberProjections*2 + len(snap.messages)*4 + len(snap.users)*2
			if weight < 1 {
				return 1
			}
			return int64(weight)
		},
		TTL:      ttl,
		OnStore:  c.indexSnapshot,
		OnRemove: c.unindexSnapshot,
	})
	return c
}

func dialogSnapshotKey(userID int64, filter domain.DialogFilter) (dialogListSnapshotKey, bool) {
	if userID == 0 || filter.Folder != nil {
		return dialogListSnapshotKey{}, false
	}
	if filter.HasFolderID {
		if filter.FolderID != domain.DialogMainFolderID && filter.FolderID != domain.DialogArchiveFolderID {
			return dialogListSnapshotKey{}, false
		}
	}
	return dialogListSnapshotKey{userID: userID}, true
}

func (c *dialogListSnapshotCache) getOrLoad(ctx context.Context, key dialogListSnapshotKey, load func() (*dialogListSnapshot, error)) (*dialogListSnapshot, error) {
	if c == nil || c.cache == nil {
		return load()
	}
	return c.cache.GetOrLoad(ctx, key, load)
}

func (c *dialogListSnapshotCache) getOrLoadVersioned(
	ctx context.Context,
	key dialogListSnapshotKey,
	ownerHash int64,
	load func() (*dialogListSnapshot, error),
) (*dialogListSnapshot, error) {
	if c == nil || c.cache == nil {
		return load()
	}
	return c.cache.GetOrLoadVersioned(ctx, key, ownerHash, load)
}

func (c *dialogListSnapshotCache) invalidateOwner(userID int64) {
	if c == nil || c.cache == nil || userID == 0 {
		return
	}
	c.cache.InvalidateWhere(func(key dialogListSnapshotKey) bool { return key.userID == userID })
}

func (c *dialogListSnapshotCache) invalidateChannel(channelID int64) {
	if c == nil || c.cache == nil || channelID == 0 {
		return
	}
	c.indexMu.Lock()
	indexed := c.channelKeys[channelID]
	keys := make([]dialogListSnapshotKey, 0, len(indexed))
	for key := range indexed {
		keys = append(keys, key)
	}
	c.indexMu.Unlock()
	c.cache.Invalidate(keys...)
}

func (c *dialogListSnapshotCache) flush() {
	if c != nil && c.cache != nil {
		c.cache.Flush()
		c.indexMu.Lock()
		c.channelKeys = make(map[int64]map[dialogListSnapshotKey]struct{})
		c.keyChannels = make(map[dialogListSnapshotKey][]int64)
		c.indexMu.Unlock()
	}
}

func (c *dialogListSnapshotCache) indexSnapshot(key dialogListSnapshotKey, snap *dialogListSnapshot) {
	if c == nil {
		return
	}
	c.indexMu.Lock()
	defer c.indexMu.Unlock()
	c.unindexSnapshotLocked(key)
	if snap == nil || len(snap.channelIDs) == 0 {
		return
	}
	ids := append([]int64(nil), snap.channelIDs...)
	c.keyChannels[key] = ids
	for _, channelID := range ids {
		keys := c.channelKeys[channelID]
		if keys == nil {
			keys = make(map[dialogListSnapshotKey]struct{})
			c.channelKeys[channelID] = keys
		}
		keys[key] = struct{}{}
	}
}

func (c *dialogListSnapshotCache) unindexSnapshot(key dialogListSnapshotKey, _ *dialogListSnapshot) {
	if c == nil {
		return
	}
	c.indexMu.Lock()
	c.unindexSnapshotLocked(key)
	c.indexMu.Unlock()
}

func (c *dialogListSnapshotCache) unindexSnapshotLocked(key dialogListSnapshotKey) {
	for _, channelID := range c.keyChannels[key] {
		keys := c.channelKeys[channelID]
		delete(keys, key)
		if len(keys) == 0 {
			delete(c.channelKeys, channelID)
		}
	}
	delete(c.keyChannels, key)
}

func newDialogListSnapshot(list domain.DialogList) *dialogListSnapshot {
	channelIDs := make([]int64, 0, len(list.Dialogs))
	seen := make(map[int64]struct{}, len(list.Dialogs))
	for _, dialog := range list.Dialogs {
		if dialog.Peer.Type != domain.PeerTypeChannel || dialog.Peer.ID == 0 {
			continue
		}
		if _, ok := seen[dialog.Peer.ID]; ok {
			continue
		}
		seen[dialog.Peer.ID] = struct{}{}
		channelIDs = append(channelIDs, dialog.Peer.ID)
	}
	archive := cloneDialogArchiveSummary(list.ArchiveSummary)
	structuralHash := dialogOwnerSnapshotStructuralHash(list.Dialogs, list.Hash)
	return &dialogListSnapshot{
		dialogs:    cloneDialogSlice(list.Dialogs),
		messages:   cloneDialogMessages(list.Messages),
		users:      cloneDialogUsers(list.Users),
		hash:       dialogHashWithDrafts(structuralHash, list.Dialogs),
		state:      list.State,
		archive:    archive,
		channelIDs: channelIDs,
	}
}

func dialogListSnapshotPageHeaders(snap *dialogListSnapshot, filter domain.DialogFilter) domain.DialogList {
	if snap == nil {
		return domain.DialogList{}
	}
	dialogs := dialogListSnapshotVariant(snap.dialogs, filter)
	start := dialogSnapshotPageStart(dialogs, filter)
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	end := start + limit
	if end > len(dialogs) {
		end = len(dialogs)
	}
	if start > end {
		start = end
	}
	hash := readmodel.MixHashes(snap.hash, dialogSnapshotVariantIdentity(filter))
	if snap.ownerHash != 0 && snap.dependencyHash != 0 {
		hash = readmodel.MixHashes(hash, snap.ownerHash, snap.dependencyHash)
	}
	out := domain.DialogList{Count: len(dialogs), Hash: hash, State: snap.state}
	out.Dialogs = cloneDialogSlice(dialogs[start:end])
	payloadPeers := make([]domain.Peer, 0, len(out.Dialogs)+1)
	for _, dialog := range out.Dialogs {
		payloadPeers = append(payloadPeers, dialog.Peer)
	}
	if dialogSnapshotIncludesArchiveSummary(filter) && snap.archive != nil {
		if !filter.PinnedOnly || snap.archive.Pinned {
			summary := *snap.archive
			out.ArchiveSummary = &summary
			if summary.TopPeer.ID != 0 {
				payloadPeers = append(payloadPeers, summary.TopPeer)
			}
		}
	}
	appendDialogSnapshotPayload(snap, payloadPeers, &out)
	return out
}

func appendDialogSnapshotPayload(snap *dialogListSnapshot, peers []domain.Peer, out *domain.DialogList) {
	if snap == nil || out == nil || len(peers) == 0 {
		return
	}
	keep := make(map[domain.Peer]struct{}, len(peers))
	for _, peer := range peers {
		if peer.Type != "" && peer.ID != 0 {
			keep[peer] = struct{}{}
		}
	}
	for _, msg := range snap.messages {
		if _, ok := keep[msg.Peer]; ok {
			out.Messages = append(out.Messages, cloneMessageForDialogCache(msg))
		}
	}
	userIDs := make(map[int64]struct{}, len(keep))
	for peer := range keep {
		switch peer.Type {
		case domain.PeerTypeUser:
			userIDs[peer.ID] = struct{}{}
		}
	}
	for _, user := range snap.users {
		if _, ok := userIDs[user.ID]; ok {
			out.Users = append(out.Users, cloneDialogUser(user))
		}
	}
}

func dialogOwnerSnapshotStructuralHash(dialogs []domain.Dialog, provided int64) int64 {
	if provided != 0 {
		return provided
	}
	h := fnv.New64a()
	var buf [96]byte
	for _, dialog := range dialogs {
		clear(buf[:])
		binary.LittleEndian.PutUint64(buf[:8], uint64(dialog.Peer.ID))
		binary.LittleEndian.PutUint32(buf[8:12], uint32(dialog.FolderID))
		binary.LittleEndian.PutUint32(buf[12:16], uint32(dialog.TopMessage))
		binary.LittleEndian.PutUint32(buf[16:20], uint32(dialog.TopMessageDate))
		binary.LittleEndian.PutUint32(buf[20:24], uint32(dialog.ReadInboxMaxID))
		binary.LittleEndian.PutUint32(buf[24:28], uint32(dialog.ReadOutboxMaxID))
		binary.LittleEndian.PutUint32(buf[28:32], uint32(dialog.UnreadCount))
		binary.LittleEndian.PutUint32(buf[32:36], uint32(dialog.UnreadMentions))
		binary.LittleEndian.PutUint32(buf[36:40], uint32(dialog.UnreadReactions))
		binary.LittleEndian.PutUint32(buf[40:44], uint32(dialog.PinnedOrder))
		if dialog.Pinned {
			buf[44] = 1
		} else {
			buf[44] = 0
		}
		if dialog.UnreadMark {
			buf[45] = 1
		} else {
			buf[45] = 0
		}
		if dialog.PeerSettingsBarHidden {
			buf[46] = 1
		} else {
			buf[46] = 0
		}
		buf[47] = byte(len(dialog.Peer.Type))
		binary.LittleEndian.PutUint32(buf[48:52], uint32(dialog.HistoryClearAnchorID))
		binary.LittleEndian.PutUint32(buf[52:56], uint32(dialog.HistoryClearAnchorDate))
		binary.LittleEndian.PutUint32(buf[56:60], uint32(dialog.TTLPeriod))
		binary.LittleEndian.PutUint32(buf[60:64], uint32(dialog.Pts))
		if dialog.ChannelLeft {
			buf[64] = 1
		}
		if dialog.HasScheduled {
			buf[65] = 1
		}
		if dialog.ViewForumAsMessages {
			buf[66] = 1
		}
		if dialog.TopMessageMentioned {
			buf[67] = 1
		}
		if dialog.TopMessageMediaUnread {
			buf[68] = 1
		}
		if dialog.TopMessageUnreadProjected {
			buf[69] = 1
		}
		if dialog.DefaultSendAs != nil {
			binary.LittleEndian.PutUint64(buf[72:80], uint64(dialog.DefaultSendAs.ID))
			buf[80] = byte(len(dialog.DefaultSendAs.Type))
		}
		_, _ = h.Write(buf[:])
		_, _ = h.Write([]byte(dialog.Peer.Type))
		_, _ = h.Write([]byte(dialog.ThemeEmoticon))
		if dialog.DefaultSendAs != nil {
			_, _ = h.Write([]byte(dialog.DefaultSendAs.Type))
		}
	}
	sum := int64(h.Sum64() & 0x7fffffffffffffff)
	if sum == 0 {
		return 1
	}
	return sum
}

func dialogListSnapshotVariant(dialogs []domain.Dialog, filter domain.DialogFilter) []domain.Dialog {
	folderID := domain.DialogMainFolderID
	if filter.HasFolderID {
		folderID = filter.FolderID
	}
	out := make([]domain.Dialog, 0, len(dialogs))
	for _, dialog := range dialogs {
		if dialog.FolderID != folderID || filter.PinnedOnly && !dialog.Pinned || filter.ExcludePinned && dialog.Pinned {
			continue
		}
		out = append(out, dialog)
	}
	return out
}

func dialogSnapshotVariantIdentity(filter domain.DialogFilter) int64 {
	folderID := domain.DialogMainFolderID
	if filter.HasFolderID {
		folderID = filter.FolderID
	}
	identity := int64(folderID + 1)
	if filter.PinnedOnly {
		identity |= 1 << 8
	}
	if filter.ExcludePinned {
		identity |= 1 << 9
	}
	return identity
}

func dialogSnapshotIncludesArchiveSummary(filter domain.DialogFilter) bool {
	if filter.HasFolderID && filter.FolderID != domain.DialogMainFolderID {
		return false
	}
	if filter.ExcludePinned {
		return false
	}
	return filter.OffsetID == 0 && filter.OffsetDate == 0 && !filter.HasOffsetPeer
}

func dialogSnapshotPageStart(dialogs []domain.Dialog, filter domain.DialogFilter) int {
	if filter.OffsetID == 0 && filter.OffsetDate == 0 && !filter.HasOffsetPeer {
		return 0
	}
	if filter.HasOffsetPeer {
		for i, dialog := range dialogs {
			if dialog.Peer == filter.OffsetPeer &&
				(filter.OffsetID == 0 || dialog.TopMessage == filter.OffsetID) &&
				(filter.OffsetDate == 0 || dialog.TopMessageDate == filter.OffsetDate) {
				return i + 1
			}
		}
	}
	for i, dialog := range dialogs {
		if dialogAfterSnapshotOffset(dialog, filter) {
			return i
		}
	}
	return len(dialogs)
}

func dialogAfterSnapshotOffset(dialog domain.Dialog, filter domain.DialogFilter) bool {
	if filter.OffsetDate > 0 {
		if dialog.TopMessageDate != filter.OffsetDate {
			return dialog.TopMessageDate < filter.OffsetDate
		}
		if filter.OffsetID <= 0 {
			return false
		}
	}
	if filter.OffsetID > 0 {
		if dialog.TopMessage != filter.OffsetID {
			return dialog.TopMessage < filter.OffsetID
		}
		if filter.HasOffsetPeer {
			return dialog.Peer.ID < filter.OffsetPeer.ID
		}
		return false
	}
	return filter.HasOffsetPeer && dialog.Peer != filter.OffsetPeer
}
