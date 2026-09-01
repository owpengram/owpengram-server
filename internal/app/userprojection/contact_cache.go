package userprojection

import (
	"container/list"
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

const (
	// DefaultContactProjectionCacheTTL is a safety bound for out-of-band writes.
	// Normal correctness relies on write-path invalidation, not natural expiry.
	DefaultContactProjectionCacheTTL = 24 * time.Hour

	// DefaultContactSnapshotMaxViewers covers the 10k online target plus bounded
	// reconnect overlap. Eviction is exact LRU; reaching the limit must never
	// clear every viewer snapshot at once.
	DefaultContactSnapshotMaxViewers = 16_384
	contactReversePairMaxEntries     = 262144
	contactProjectionPairMaxEntries  = 262144
	// One dense request must not monopolize the pair LRU or hold the global
	// cache lock while inserting and evicting hundreds of thousands of cells.
	// Larger results are still returned; they simply are not admitted per pair.
	contactProjectionDenseAdmissionMaxCells = contactProjectionPairMaxEntries / 16
)

type contactAccountSnapshot struct {
	// contacts and ordered are immutable after the snapshot is published in
	// CachedContactStore.contacts. Readers intentionally retain shallow copies.
	contacts map[int64]domain.Contact
	ordered  []domain.Contact
	hash     int64
	expireAt time.Time
}

type personalPhotoSnapshot struct {
	// refs is immutable after the snapshot is published in personalPhotos.
	refs     map[int64]domain.ProfilePhotoRef
	expireAt time.Time
}

type reverseContactKey struct {
	ownerUserID   int64
	contactUserID int64
}

type reverseContactSnapshot struct {
	// contact is an immutable cached clone. nil is the negative-cache value.
	contact  *domain.Contact
	expireAt time.Time
}

type reverseContactEntry struct {
	key      reverseContactKey
	snapshot reverseContactSnapshot
}

type contactProjectionKey struct {
	viewerUserID  int64
	contactUserID int64
}

// cachedContactProjectionOverlay is the viewer-owned part of a contact row.
// Base user data is loaded and cached independently, so retaining domain.User
// here would multiply a large, viewer-independent value across every pair.
// Values are immutable after publication; noteEntities is cloned on both sides
// of the cache boundary.
type cachedContactProjectionOverlay struct {
	firstName    string
	lastName     string
	phone        string
	note         string
	noteEntities []domain.MessageEntity
	mutual       bool
	closeFriend  bool
}

func newCachedContactProjectionOverlay(contact domain.Contact) *cachedContactProjectionOverlay {
	return &cachedContactProjectionOverlay{
		firstName:    contact.FirstName,
		lastName:     contact.LastName,
		phone:        contact.Phone,
		note:         contact.Note,
		noteEntities: append([]domain.MessageEntity(nil), contact.NoteEntities...),
		mutual:       contact.Mutual || contact.User.Mutual,
		closeFriend:  contact.CloseFriend || contact.User.CloseFriend,
	}
}

func (o *cachedContactProjectionOverlay) domainContact(contactUserID int64) domain.Contact {
	if o == nil {
		return domain.Contact{}
	}
	return domain.Contact{
		User:         domain.User{ID: contactUserID},
		FirstName:    o.firstName,
		LastName:     o.lastName,
		Phone:        o.phone,
		Note:         o.note,
		NoteEntities: append([]domain.MessageEntity(nil), o.noteEntities...),
		Mutual:       o.mutual,
		CloseFriend:  o.closeFriend,
	}
}

type contactProjectionSnapshot struct {
	// Positive values point at immutable cached clones; nil is negative. Keeping
	// the compact viewer-owned overlay outside the entry makes negative pairs
	// consume only two pointers plus their expiry and avoids duplicating a full
	// base User for every positive pair.
	contact       *cachedContactProjectionOverlay
	personalPhoto *domain.ProfilePhotoRef
	expireAt      time.Time
}

// contactProjectionLookup is a transient, caller-owned copy. It deliberately
// retains the old value+found shape so no mutable slice from a cached pointer is
// exposed after the cache lock is released.
type contactProjectionLookup struct {
	contact            domain.Contact
	contactFound       bool
	personalPhoto      domain.ProfilePhotoRef
	personalPhotoFound bool
}

type contactProjectionEntry struct {
	key      contactProjectionKey
	snapshot contactProjectionSnapshot
}

type contactSnapshotLoadResult struct {
	snap   contactAccountSnapshot
	stored bool
}

type reverseContactLoadResult struct {
	contacts map[int64]domain.Contact
	stored   bool
}

type personalPhotoSnapshotLoadResult struct {
	snap   personalPhotoSnapshot
	stored bool
}

type contactProjectionLoadResult struct {
	batch   domain.ContactProjectionBatch
	current bool
}

type contactCacheViewerFence struct {
	userID     int64
	generation uint64
}

type contactCacheFence struct {
	flushGeneration uint64
	viewers         []contactCacheViewerFence
}

// CachedContactStore wraps ContactStore with account-level read model snapshots.
//
// Contact data is low-churn and high-read: TDesktop repeatedly asks for the same
// viewer-scoped user projection while switching dialogs. Pair-level short TTL
// caching still lets every RPC plan new SQL for another pair; this cache loads a
// viewer's whole contact projection once, filters it in memory, and relies on
// contact write methods to invalidate the affected account snapshots.
type CachedContactStore struct {
	inner store.ContactStore
	ttl   time.Duration
	now   func() time.Time

	mu                 sync.RWMutex
	contacts           map[int64]contactAccountSnapshot
	contactLRU         *list.List
	contactElements    map[int64]*list.Element
	contactCap         int
	personalPhotos     map[int64]personalPhotoSnapshot
	personalPhotoLRU   *list.List
	personalElements   map[int64]*list.Element
	personalPhotoCap   int
	reverse            map[reverseContactKey]*list.Element
	reverseLRU         *list.List
	reverseByOwner     map[int64]map[int64]struct{}
	reverseCap         int
	projection         map[contactProjectionKey]*list.Element
	projectionLRU      *list.List
	projectionByViewer map[int64]map[int64]struct{}
	projectionByTarget map[int64]map[int64]struct{}
	projectionCap      int
	flushGeneration    uint64
	viewerGenerations  map[int64]uint64
	sf                 singleflight.Group
}

func NewCachedContactStore(inner store.ContactStore, ttl time.Duration) *CachedContactStore {
	return NewCachedContactStoreWithMaxViewers(inner, ttl, DefaultContactSnapshotMaxViewers)
}

func NewCachedContactStoreWithMaxViewers(inner store.ContactStore, ttl time.Duration, maxViewers int) *CachedContactStore {
	if inner == nil {
		return nil
	}
	if ttl <= 0 {
		ttl = DefaultContactProjectionCacheTTL
	}
	if maxViewers <= 0 {
		maxViewers = DefaultContactSnapshotMaxViewers
	}
	return &CachedContactStore{
		inner:              inner,
		ttl:                ttl,
		now:                time.Now,
		contacts:           make(map[int64]contactAccountSnapshot, 1024),
		contactLRU:         list.New(),
		contactElements:    make(map[int64]*list.Element, 1024),
		contactCap:         maxViewers,
		personalPhotos:     make(map[int64]personalPhotoSnapshot, 1024),
		personalPhotoLRU:   list.New(),
		personalElements:   make(map[int64]*list.Element, 1024),
		personalPhotoCap:   maxViewers,
		reverse:            make(map[reverseContactKey]*list.Element, 4096),
		reverseLRU:         list.New(),
		reverseByOwner:     make(map[int64]map[int64]struct{}, 1024),
		reverseCap:         contactReversePairMaxEntries,
		projection:         make(map[contactProjectionKey]*list.Element, 4096),
		projectionLRU:      list.New(),
		projectionByViewer: make(map[int64]map[int64]struct{}, 1024),
		projectionByTarget: make(map[int64]map[int64]struct{}, 1024),
		projectionCap:      contactProjectionPairMaxEntries,
		viewerGenerations:  make(map[int64]uint64, 1024),
	}
}

func (c *CachedContactStore) ListByUser(ctx context.Context, userID int64) (domain.ContactList, error) {
	if userID == 0 {
		return domain.ContactList{}, nil
	}
	snap, err := c.contactSnapshot(ctx, userID)
	if err != nil {
		return domain.ContactList{}, err
	}
	return domain.ContactList{Contacts: cloneCachedContacts(snap.ordered), Hash: snap.hash}, nil
}

func (c *CachedContactStore) Get(ctx context.Context, userID, contactUserID int64) (domain.Contact, bool, error) {
	if userID == 0 || contactUserID == 0 {
		return domain.Contact{}, false, nil
	}
	got, err := c.GetMany(ctx, userID, []int64{contactUserID})
	if err != nil {
		return domain.Contact{}, false, err
	}
	contact, ok := got[contactUserID]
	return contact, ok, nil
}

func (c *CachedContactStore) GetMany(ctx context.Context, userID int64, contactUserIDs []int64) (map[int64]domain.Contact, error) {
	out := make(map[int64]domain.Contact, len(contactUserIDs))
	if userID == 0 || len(contactUserIDs) == 0 {
		return out, nil
	}
	snap, err := c.contactSnapshot(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, ownerID := range contactUserIDs {
		if ownerID == 0 {
			continue
		}
		if contact, ok := snap.contacts[ownerID]; ok {
			out[ownerID] = cloneCachedContact(contact)
		}
	}
	return out, nil
}

func (c *CachedContactStore) GetReverseContacts(ctx context.Context, userID int64, ownerUserIDs []int64) (map[int64]domain.Contact, error) {
	out := make(map[int64]domain.Contact, len(ownerUserIDs))
	if userID == 0 || len(ownerUserIDs) == 0 {
		return out, nil
	}
	owners := dedupContactIDs(ownerUserIDs)
	if len(owners) == 0 {
		return out, nil
	}
	missing := make([]int64, 0, len(owners))
	now := c.now()
	for _, ownerID := range owners {
		// Reuse a full owner snapshot when another hot path already loaded it.
		// Do not cold-load one full list per owner: a large projection would turn
		// into N SQL queries.
		if snap, ok := c.lookupContactSnapshot(ownerID, now); ok {
			if contact, found := snap.contacts[userID]; found {
				out[ownerID] = cloneCachedContact(contact)
			}
			continue
		}
		if contact, found, cached := c.lookupReverseContact(ownerID, userID, now); cached {
			if found {
				out[ownerID] = contact
			}
			continue
		}
		missing = append(missing, ownerID)
	}
	if len(missing) == 0 {
		return out, nil
	}
	loaded, err := c.loadReverseContacts(ctx, userID, missing)
	if err != nil {
		return nil, err
	}
	for ownerID, contact := range loaded {
		if contact.User.ID != 0 {
			out[ownerID] = cloneCachedContact(contact)
		}
	}
	return out, nil
}

func (c *CachedContactStore) ContactProjectionForViewers(ctx context.Context, viewerUserIDs, contactUserIDs []int64) (domain.ContactProjectionBatch, error) {
	viewers := dedupContactIDs(viewerUserIDs)
	targets := dedupContactIDs(contactUserIDs)
	if len(viewers) == 0 || len(targets) == 0 {
		return domain.ContactProjectionBatch{
			Contacts:       map[int64]map[int64]domain.Contact{},
			PersonalPhotos: map[int64]map[int64]domain.ProfilePhotoRef{},
		}, nil
	}

	for {
		out := domain.ContactProjectionBatch{
			Contacts:       make(map[int64]map[int64]domain.Contact, len(viewers)),
			PersonalPhotos: make(map[int64]map[int64]domain.ProfilePhotoRef, len(viewers)),
		}
		readFence := c.captureCacheFenceSlices(viewers, targets)
		now := c.now()
		coldViewers := make(map[int64]struct{}, len(viewers))
		coldTargets := make(map[int64]struct{}, len(targets))
		for _, viewerID := range viewers {
			var contactSnap contactAccountSnapshot
			contactsWarm := false
			if snap, ok := c.lookupContactSnapshot(viewerID, now); ok {
				contactsWarm = true
				contactSnap = snap
				for _, targetID := range targets {
					if contact, found := snap.contacts[targetID]; found {
						putContactProjectionContact(&out, viewerID, targetID, contact)
					}
				}
			}
			personalPhotosWarm := false
			if snap, ok := c.lookupPersonalPhotoSnapshot(viewerID, now); ok {
				personalPhotosWarm = true
				for _, targetID := range targets {
					if ref, found := snap.refs[targetID]; found {
						putContactProjectionPersonalPhoto(&out, viewerID, targetID, ref)
					}
				}
			}
			for _, targetID := range targets {
				if contactsWarm && personalPhotosWarm {
					continue
				}
				if contactsWarm {
					if _, found := contactSnap.contacts[targetID]; !found {
						continue
					}
				}
				if snap, ok := c.lookupContactProjectionPair(viewerID, targetID, now); ok {
					if !contactsWarm && snap.contactFound {
						putContactProjectionContact(&out, viewerID, targetID, snap.contact)
					}
					if !personalPhotosWarm && snap.personalPhotoFound {
						putContactProjectionPersonalPhoto(&out, viewerID, targetID, snap.personalPhoto)
					}
					continue
				}
				coldViewers[viewerID] = struct{}{}
				coldTargets[targetID] = struct{}{}
			}
		}
		if !c.cacheFenceCurrent(readFence) {
			if err := ctx.Err(); err != nil {
				return domain.ContactProjectionBatch{}, err
			}
			continue
		}
		if len(coldViewers) == 0 || len(coldTargets) == 0 {
			return out, nil
		}
		cold := make([]int64, 0, len(coldViewers))
		for viewerID := range coldViewers {
			cold = append(cold, viewerID)
		}
		coldIDs := make([]int64, 0, len(coldTargets))
		for targetID := range coldTargets {
			coldIDs = append(coldIDs, targetID)
		}
		loaded, err := c.loadContactProjectionForViewers(ctx, cold, coldIDs)
		if err != nil {
			return domain.ContactProjectionBatch{}, err
		}
		if !c.cacheFenceCurrent(readFence) {
			if err := ctx.Err(); err != nil {
				return domain.ContactProjectionBatch{}, err
			}
			continue
		}
		mergeContactProjectionBatch(&out, loaded)
		return out, nil
	}
}

func (c *CachedContactStore) loadContactProjectionForViewers(ctx context.Context, viewerUserIDs, contactUserIDs []int64) (domain.ContactProjectionBatch, error) {
	viewers := append([]int64(nil), viewerUserIDs...)
	targets := append([]int64(nil), contactUserIDs...)
	sort.Slice(viewers, func(i, j int) bool { return viewers[i] < viewers[j] })
	sort.Slice(targets, func(i, j int) bool { return targets[i] < targets[j] })
	sfKey := fmt.Sprintf("contact-projection:%v:%v", viewers, targets)
	for {
		v, err, _ := c.sf.Do(sfKey, func() (any, error) {
			loadFence := c.captureCacheFenceSlices(viewers, targets)
			batch, err := c.inner.ContactProjectionForViewers(ctx, viewers, targets)
			if err != nil {
				return contactProjectionLoadResult{}, err
			}
			now := c.now()
			expireAt := now.Add(c.ttl)
			admitPairs := admitDenseContactProjectionPairs(len(viewers), len(targets))
			c.mu.Lock()
			current := c.cacheFenceCurrentLocked(loadFence)
			if current && admitPairs {
				for _, viewerID := range viewers {
					for _, targetID := range targets {
						contact, contactFound := batch.Contacts[viewerID][targetID]
						ref, personalPhotoFound := batch.PersonalPhotos[viewerID][targetID]
						c.storeContactProjectionPairLocked(
							contactProjectionKey{viewerUserID: viewerID, contactUserID: targetID},
							contact, contactFound, ref, personalPhotoFound, expireAt,
						)
					}
				}
			}
			c.mu.Unlock()
			return contactProjectionLoadResult{
				batch:   cloneContactProjectionBatch(batch),
				current: current,
			}, nil
		})
		if err != nil {
			return domain.ContactProjectionBatch{}, err
		}
		result := v.(contactProjectionLoadResult)
		if result.current {
			return result.batch, nil
		}
		if err := ctx.Err(); err != nil {
			return domain.ContactProjectionBatch{}, err
		}
	}
}

func admitDenseContactProjectionPairs(viewerCount, targetCount int) bool {
	if viewerCount <= 0 || targetCount <= 0 || targetCount > contactProjectionDenseAdmissionMaxCells {
		return false
	}
	// Division avoids overflowing int for attacker-controlled vector lengths.
	return viewerCount <= contactProjectionDenseAdmissionMaxCells/targetCount
}

// loadReverseContacts performs at most one batched cold-store read for all
// missing owner→viewer pairs, then caches both hits and misses. Privacy
// projection therefore stays memory-only after warm-up instead of repeating a
// reverse-contact SQL query on every large user vector.
func (c *CachedContactStore) loadReverseContacts(ctx context.Context, userID int64, ownerUserIDs []int64) (map[int64]domain.Contact, error) {
	owners := append([]int64(nil), ownerUserIDs...)
	sort.Slice(owners, func(i, j int) bool { return owners[i] < owners[j] })
	sfKey := fmt.Sprintf("contact-reverse:%d:%v", userID, owners)
	for {
		v, err, _ := c.sf.Do(sfKey, func() (any, error) {
			loadFence := c.captureCacheFenceSlices(owners, []int64{userID})
			contacts, err := c.inner.GetReverseContacts(ctx, userID, owners)
			if err != nil {
				return reverseContactLoadResult{}, err
			}
			now := c.now()
			expireAt := now.Add(c.ttl)
			c.mu.Lock()
			stored := c.cacheFenceCurrentLocked(loadFence)
			if stored {
				for _, ownerID := range owners {
					key := reverseContactKey{ownerUserID: ownerID, contactUserID: userID}
					contact, found := contacts[ownerID]
					c.storeReverseContactLocked(key, contact, found, expireAt)
				}
			}
			c.mu.Unlock()
			return reverseContactLoadResult{
				contacts: cloneCachedContactMap(contacts),
				stored:   stored,
			}, nil
		})
		if err != nil {
			return nil, err
		}
		result := v.(reverseContactLoadResult)
		if result.stored {
			return result.contacts, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
}

func (c *CachedContactStore) Upsert(ctx context.Context, userID int64, input domain.ContactInput) (domain.Contact, error) {
	contact, err := c.inner.Upsert(ctx, userID, input)
	if err == nil {
		// Published account snapshots are immutable. Invalidate instead of
		// modifying their inner maps/slices in place or publishing a mutation
		// payload whose cache-write order may differ from its DB commit order.
		c.InvalidateViewers(userID, input.ContactUserID)
	}
	return contact, err
}

func (c *CachedContactStore) UpsertMany(ctx context.Context, userID int64, inputs []domain.ContactInput) ([]domain.Contact, error) {
	contacts, err := c.inner.UpsertMany(ctx, userID, inputs)
	if err == nil {
		ids := make([]int64, 0, len(inputs))
		for _, input := range inputs {
			ids = append(ids, input.ContactUserID)
		}
		c.InvalidateViewers(append([]int64{userID}, ids...)...)
	}
	return contacts, err
}

func (c *CachedContactStore) UpdateNote(ctx context.Context, userID, contactUserID int64, note string, entities []domain.MessageEntity) (domain.Contact, bool, error) {
	contact, found, err := c.inner.UpdateNote(ctx, userID, contactUserID, note, entities)
	if err == nil {
		if found {
			c.InvalidateViewers(userID)
		}
	}
	return contact, found, err
}

func (c *CachedContactStore) SetCloseFriends(ctx context.Context, userID int64, contactUserIDs []int64) (domain.CloseFriendsEditResult, error) {
	res, err := c.inner.SetCloseFriends(ctx, userID, contactUserIDs)
	if err == nil {
		c.InvalidateViewers(userID)
	}
	return res, err
}

func (c *CachedContactStore) SetPersonalPhoto(ctx context.Context, userID, contactUserID int64, photoID int64, date int) (domain.Contact, bool, error) {
	contact, found, err := c.inner.SetPersonalPhoto(ctx, userID, contactUserID, photoID, date)
	if err == nil && found {
		// Do not perform a post-commit read followed by write-through: two
		// concurrent mutations can complete their cache writes in the opposite
		// order and reinsert a stale pair after a newer NOTIFY invalidation.
		c.InvalidateViewers(userID)
	}
	return contact, found, err
}

func (c *CachedContactStore) PersonalPhotos(ctx context.Context, userID int64, contactUserIDs []int64) (map[int64]domain.ProfilePhotoRef, error) {
	out := make(map[int64]domain.ProfilePhotoRef, len(contactUserIDs))
	if userID == 0 || len(contactUserIDs) == 0 {
		return out, nil
	}
	snap, err := c.personalPhotoSnapshot(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, ownerID := range contactUserIDs {
		if ownerID == 0 {
			continue
		}
		if ref, ok := snap.refs[ownerID]; ok {
			out[ownerID] = cloneCachedProfilePhotoRef(ref)
		}
	}
	return out, nil
}

func (c *CachedContactStore) Delete(ctx context.Context, userID int64, contactUserIDs []int64) (int, error) {
	count, err := c.inner.Delete(ctx, userID, contactUserIDs)
	if err == nil {
		c.InvalidateViewers(append([]int64{userID}, contactUserIDs...)...)
	}
	return count, err
}

func (c *CachedContactStore) Block(ctx context.Context, userID, blockedUserID int64, date int) (bool, error) {
	changed, err := c.inner.Block(ctx, userID, blockedUserID, date)
	if err == nil {
		c.InvalidateViewers(userID, blockedUserID)
	}
	return changed, err
}

func (c *CachedContactStore) Unblock(ctx context.Context, userID, blockedUserID int64) (bool, error) {
	changed, err := c.inner.Unblock(ctx, userID, blockedUserID)
	if err == nil {
		c.InvalidateViewers(userID, blockedUserID)
	}
	return changed, err
}

func (c *CachedContactStore) IsBlocked(ctx context.Context, userID, blockedUserID int64) (bool, error) {
	return c.inner.IsBlocked(ctx, userID, blockedUserID)
}

func (c *CachedContactStore) ListBlocked(ctx context.Context, userID int64, offset, limit int) (domain.BlockedContactList, error) {
	return c.inner.ListBlocked(ctx, userID, offset, limit)
}

func (c *CachedContactStore) contactSnapshot(ctx context.Context, userID int64) (contactAccountSnapshot, error) {
	for {
		if snap, ok := c.lookupContactSnapshot(userID, c.now()); ok {
			return snap, nil
		}
		v, err, _ := c.sf.Do(fmt.Sprintf("contact:%d", userID), func() (any, error) {
			now := c.now()
			if snap, ok := c.lookupContactSnapshot(userID, now); ok {
				return contactSnapshotLoadResult{snap: snap, stored: true}, nil
			}
			loadFence := c.captureCacheFence(userID)
			list, err := c.inner.ListByUser(ctx, userID)
			if err != nil {
				return contactSnapshotLoadResult{}, err
			}
			snap := buildContactAccountSnapshot(list, now.Add(c.ttl))
			c.mu.Lock()
			stored := c.cacheFenceCurrentLocked(loadFence)
			if stored {
				c.storeContactSnapshotLocked(userID, snap)
			}
			c.mu.Unlock()
			return contactSnapshotLoadResult{snap: snap, stored: stored}, nil
		})
		if err != nil {
			return contactAccountSnapshot{}, err
		}
		result := v.(contactSnapshotLoadResult)
		if result.stored {
			return result.snap, nil
		}
		if err := ctx.Err(); err != nil {
			return contactAccountSnapshot{}, err
		}
	}
}

func (c *CachedContactStore) lookupContactSnapshot(userID int64, now time.Time) (contactAccountSnapshot, bool) {
	c.mu.Lock()
	snap, ok := c.contacts[userID]
	if !ok {
		c.mu.Unlock()
		return contactAccountSnapshot{}, false
	}
	if !snap.expireAt.After(now) {
		c.advanceViewerGenerationLocked(userID)
		c.invalidateViewerLocked(userID)
		c.mu.Unlock()
		return contactAccountSnapshot{}, false
	}
	if element := c.contactElements[userID]; element != nil {
		c.contactLRU.MoveToFront(element)
	}
	c.mu.Unlock()
	return snap, true
}

func (c *CachedContactStore) storeContactSnapshotLocked(userID int64, snap contactAccountSnapshot) {
	if element := c.contactElements[userID]; element != nil {
		c.contacts[userID] = snap
		c.contactLRU.MoveToFront(element)
		return
	}
	c.contacts[userID] = snap
	c.contactElements[userID] = c.contactLRU.PushFront(userID)
	for c.contactLRU.Len() > c.contactCap {
		oldest := c.contactLRU.Back()
		if oldest == nil {
			break
		}
		oldestUserID := oldest.Value.(int64)
		delete(c.contacts, oldestUserID)
		delete(c.contactElements, oldestUserID)
		c.contactLRU.Remove(oldest)
	}
}

func (c *CachedContactStore) personalPhotoSnapshot(ctx context.Context, userID int64) (personalPhotoSnapshot, error) {
	for {
		if snap, ok := c.lookupPersonalPhotoSnapshot(userID, c.now()); ok {
			return snap, nil
		}
		v, err, _ := c.sf.Do(fmt.Sprintf("contact-photo:%d", userID), func() (any, error) {
			now := c.now()
			if snap, ok := c.lookupPersonalPhotoSnapshot(userID, now); ok {
				return personalPhotoSnapshotLoadResult{snap: snap, stored: true}, nil
			}
			loadFence := c.captureCacheFence(userID)
			contacts, err := c.contactSnapshot(ctx, userID)
			if err != nil {
				return personalPhotoSnapshotLoadResult{}, err
			}
			ids := make([]int64, 0, len(contacts.contacts))
			for id := range contacts.contacts {
				ids = append(ids, id)
			}
			refs := map[int64]domain.ProfilePhotoRef{}
			if len(ids) > 0 {
				refs, err = c.inner.PersonalPhotos(ctx, userID, ids)
				if err != nil {
					return personalPhotoSnapshotLoadResult{}, err
				}
			}
			snap := personalPhotoSnapshot{refs: cloneCachedProfilePhotoRefs(refs), expireAt: now.Add(c.ttl)}
			c.mu.Lock()
			stored := c.cacheFenceCurrentLocked(loadFence)
			if stored {
				c.storePersonalPhotoSnapshotLocked(userID, snap)
			}
			c.mu.Unlock()
			return personalPhotoSnapshotLoadResult{snap: snap, stored: stored}, nil
		})
		if err != nil {
			return personalPhotoSnapshot{}, err
		}
		result := v.(personalPhotoSnapshotLoadResult)
		if result.stored {
			return result.snap, nil
		}
		if err := ctx.Err(); err != nil {
			return personalPhotoSnapshot{}, err
		}
	}
}

func (c *CachedContactStore) lookupPersonalPhotoSnapshot(userID int64, now time.Time) (personalPhotoSnapshot, bool) {
	c.mu.Lock()
	snap, ok := c.personalPhotos[userID]
	if !ok {
		c.mu.Unlock()
		return personalPhotoSnapshot{}, false
	}
	if !snap.expireAt.After(now) {
		c.advanceViewerGenerationLocked(userID)
		c.invalidateViewerLocked(userID)
		c.mu.Unlock()
		return personalPhotoSnapshot{}, false
	}
	if element := c.personalElements[userID]; element != nil {
		c.personalPhotoLRU.MoveToFront(element)
	}
	c.mu.Unlock()
	return snap, true
}

func (c *CachedContactStore) storePersonalPhotoSnapshotLocked(userID int64, snap personalPhotoSnapshot) {
	if element := c.personalElements[userID]; element != nil {
		c.personalPhotos[userID] = snap
		c.personalPhotoLRU.MoveToFront(element)
		return
	}
	c.personalPhotos[userID] = snap
	c.personalElements[userID] = c.personalPhotoLRU.PushFront(userID)
	for c.personalPhotoLRU.Len() > c.personalPhotoCap {
		oldest := c.personalPhotoLRU.Back()
		if oldest == nil {
			break
		}
		oldestUserID := oldest.Value.(int64)
		delete(c.personalPhotos, oldestUserID)
		delete(c.personalElements, oldestUserID)
		c.personalPhotoLRU.Remove(oldest)
	}
}

func (c *CachedContactStore) lookupReverseContact(ownerUserID, contactUserID int64, now time.Time) (domain.Contact, bool, bool) {
	key := reverseContactKey{ownerUserID: ownerUserID, contactUserID: contactUserID}
	c.mu.Lock()
	element, ok := c.reverse[key]
	if !ok {
		c.mu.Unlock()
		return domain.Contact{}, false, false
	}
	entry := element.Value.(*reverseContactEntry)
	snap := entry.snapshot
	if !snap.expireAt.After(now) {
		c.removeReverseElementLocked(element)
		c.mu.Unlock()
		return domain.Contact{}, false, false
	}
	c.reverseLRU.MoveToFront(element)
	c.mu.Unlock()
	if snap.contact == nil {
		return domain.Contact{}, false, true
	}
	return cloneCachedContact(*snap.contact), true, true
}

func (c *CachedContactStore) storeReverseContactLocked(key reverseContactKey, contact domain.Contact, found bool, expireAt time.Time) {
	var cached *domain.Contact
	if found {
		clone := cloneCachedContact(contact)
		cached = &clone
	}
	snapshot := reverseContactSnapshot{contact: cached, expireAt: expireAt}
	if element, ok := c.reverse[key]; ok {
		entry := element.Value.(*reverseContactEntry)
		entry.snapshot = snapshot
		c.reverseLRU.MoveToFront(element)
		return
	}
	element := c.reverseLRU.PushFront(&reverseContactEntry{key: key, snapshot: snapshot})
	c.reverse[key] = element
	if c.reverseByOwner[key.ownerUserID] == nil {
		c.reverseByOwner[key.ownerUserID] = make(map[int64]struct{})
	}
	c.reverseByOwner[key.ownerUserID][key.contactUserID] = struct{}{}
	for c.reverseLRU.Len() > c.reverseCap {
		c.removeReverseElementLocked(c.reverseLRU.Back())
	}
}

func (c *CachedContactStore) removeReverseElementLocked(element *list.Element) {
	if element == nil {
		return
	}
	entry := element.Value.(*reverseContactEntry)
	delete(c.reverse, entry.key)
	if viewers := c.reverseByOwner[entry.key.ownerUserID]; viewers != nil {
		delete(viewers, entry.key.contactUserID)
		if len(viewers) == 0 {
			delete(c.reverseByOwner, entry.key.ownerUserID)
		}
	}
	c.reverseLRU.Remove(element)
}

func (c *CachedContactStore) lookupContactProjectionPair(viewerUserID, contactUserID int64, now time.Time) (contactProjectionLookup, bool) {
	key := contactProjectionKey{viewerUserID: viewerUserID, contactUserID: contactUserID}
	c.mu.Lock()
	element, ok := c.projection[key]
	if !ok {
		c.mu.Unlock()
		return contactProjectionLookup{}, false
	}
	entry := element.Value.(*contactProjectionEntry)
	snap := entry.snapshot
	if !snap.expireAt.After(now) {
		c.removeContactProjectionElementLocked(element)
		c.mu.Unlock()
		return contactProjectionLookup{}, false
	}
	c.projectionLRU.MoveToFront(element)
	c.mu.Unlock()
	result := contactProjectionLookup{}
	if snap.contact != nil {
		result.contact = snap.contact.domainContact(contactUserID)
		result.contactFound = true
	}
	if snap.personalPhoto != nil {
		result.personalPhoto = cloneCachedProfilePhotoRef(*snap.personalPhoto)
		result.personalPhotoFound = true
	}
	return result, true
}

func (c *CachedContactStore) storeContactProjectionPairLocked(
	key contactProjectionKey,
	contact domain.Contact,
	contactFound bool,
	personalPhoto domain.ProfilePhotoRef,
	personalPhotoFound bool,
	expireAt time.Time,
) {
	var cachedContact *cachedContactProjectionOverlay
	if contactFound {
		cachedContact = newCachedContactProjectionOverlay(contact)
	}
	var cachedPersonalPhoto *domain.ProfilePhotoRef
	if personalPhotoFound {
		clone := cloneCachedProfilePhotoRef(personalPhoto)
		cachedPersonalPhoto = &clone
	}
	snapshot := contactProjectionSnapshot{
		contact:       cachedContact,
		personalPhoto: cachedPersonalPhoto,
		expireAt:      expireAt,
	}
	if element, ok := c.projection[key]; ok {
		entry := element.Value.(*contactProjectionEntry)
		entry.snapshot = snapshot
		c.projectionLRU.MoveToFront(element)
		return
	}
	element := c.projectionLRU.PushFront(&contactProjectionEntry{key: key, snapshot: snapshot})
	c.projection[key] = element
	if c.projectionByViewer[key.viewerUserID] == nil {
		c.projectionByViewer[key.viewerUserID] = make(map[int64]struct{})
	}
	c.projectionByViewer[key.viewerUserID][key.contactUserID] = struct{}{}
	if c.projectionByTarget[key.contactUserID] == nil {
		c.projectionByTarget[key.contactUserID] = make(map[int64]struct{})
	}
	c.projectionByTarget[key.contactUserID][key.viewerUserID] = struct{}{}
	for c.projectionLRU.Len() > c.projectionCap {
		c.removeContactProjectionElementLocked(c.projectionLRU.Back())
	}
}

func (c *CachedContactStore) removeContactProjectionElementLocked(element *list.Element) {
	if element == nil {
		return
	}
	entry := element.Value.(*contactProjectionEntry)
	delete(c.projection, entry.key)
	if targets := c.projectionByViewer[entry.key.viewerUserID]; targets != nil {
		delete(targets, entry.key.contactUserID)
		if len(targets) == 0 {
			delete(c.projectionByViewer, entry.key.viewerUserID)
		}
	}
	if viewers := c.projectionByTarget[entry.key.contactUserID]; viewers != nil {
		delete(viewers, entry.key.viewerUserID)
		if len(viewers) == 0 {
			delete(c.projectionByTarget, entry.key.contactUserID)
		}
	}
	c.projectionLRU.Remove(element)
}

func (c *CachedContactStore) InvalidateViewers(ids ...int64) {
	if c == nil || len(ids) == 0 {
		return
	}
	c.mu.Lock()
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		c.advanceViewerGenerationLocked(id)
		c.invalidateViewerLocked(id)
	}
	c.mu.Unlock()
}

func (c *CachedContactStore) invalidateViewerLocked(id int64) {
	delete(c.contacts, id)
	if element := c.contactElements[id]; element != nil {
		delete(c.contactElements, id)
		c.contactLRU.Remove(element)
	}
	delete(c.personalPhotos, id)
	if element := c.personalElements[id]; element != nil {
		delete(c.personalElements, id)
		c.personalPhotoLRU.Remove(element)
	}
	for contactUserID := range c.reverseByOwner[id] {
		c.removeReverseKeyLocked(reverseContactKey{ownerUserID: id, contactUserID: contactUserID})
	}
	for contactUserID := range c.projectionByViewer[id] {
		c.removeContactProjectionKeyLocked(contactProjectionKey{viewerUserID: id, contactUserID: contactUserID})
	}
	for viewerUserID := range c.projectionByTarget[id] {
		c.removeContactProjectionKeyLocked(contactProjectionKey{viewerUserID: viewerUserID, contactUserID: id})
	}
}

func (c *CachedContactStore) removeReverseKeyLocked(key reverseContactKey) {
	if element, ok := c.reverse[key]; ok {
		c.removeReverseElementLocked(element)
	}
}

func (c *CachedContactStore) removeContactProjectionKeyLocked(key contactProjectionKey) {
	if element, ok := c.projection[key]; ok {
		c.removeContactProjectionElementLocked(element)
	}
}

func (c *CachedContactStore) FlushReadModelCache() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.flushGeneration++
	c.viewerGenerations = make(map[int64]uint64, 1024)
	c.contacts = make(map[int64]contactAccountSnapshot, 1024)
	c.contactElements = make(map[int64]*list.Element, 1024)
	c.contactLRU.Init()
	c.personalPhotos = make(map[int64]personalPhotoSnapshot, 1024)
	c.personalElements = make(map[int64]*list.Element, 1024)
	c.personalPhotoLRU.Init()
	c.reverse = make(map[reverseContactKey]*list.Element, 4096)
	c.reverseLRU.Init()
	c.reverseByOwner = make(map[int64]map[int64]struct{}, 1024)
	c.projection = make(map[contactProjectionKey]*list.Element, 4096)
	c.projectionLRU.Init()
	c.projectionByViewer = make(map[int64]map[int64]struct{}, 1024)
	c.projectionByTarget = make(map[int64]map[int64]struct{}, 1024)
	c.mu.Unlock()
}

func (c *CachedContactStore) captureCacheFence(userIDs ...int64) contactCacheFence {
	return c.captureCacheFenceSlices(userIDs, nil)
}

func (c *CachedContactStore) captureCacheFenceSlices(first, second []int64) contactCacheFence {
	c.mu.RLock()
	fence := contactCacheFence{
		flushGeneration: c.flushGeneration,
		viewers:         make([]contactCacheViewerFence, 0, len(first)+len(second)),
	}
	for _, userIDs := range [][]int64{first, second} {
		for _, userID := range userIDs {
			if userID == 0 {
				continue
			}
			fence.viewers = append(fence.viewers, contactCacheViewerFence{
				userID:     userID,
				generation: c.viewerGenerations[userID],
			})
		}
	}
	c.mu.RUnlock()
	return fence
}

func (c *CachedContactStore) cacheFenceCurrent(fence contactCacheFence) bool {
	c.mu.RLock()
	current := c.cacheFenceCurrentLocked(fence)
	c.mu.RUnlock()
	return current
}

func (c *CachedContactStore) cacheFenceCurrentLocked(fence contactCacheFence) bool {
	if c.flushGeneration != fence.flushGeneration {
		return false
	}
	for _, viewer := range fence.viewers {
		if c.viewerGenerations[viewer.userID] != viewer.generation {
			return false
		}
	}
	return true
}

func (c *CachedContactStore) advanceViewerGenerationLocked(userID int64) {
	c.viewerGenerations[userID]++
}

func buildContactAccountSnapshot(list domain.ContactList, expireAt time.Time) contactAccountSnapshot {
	contacts := make(map[int64]domain.Contact, len(list.Contacts))
	ordered := make([]domain.Contact, 0, len(list.Contacts))
	for _, contact := range list.Contacts {
		if contact.User.ID == 0 {
			continue
		}
		clone := cloneCachedContact(contact)
		contacts[clone.User.ID] = clone
		ordered = append(ordered, clone)
	}
	return contactAccountSnapshot{contacts: contacts, ordered: ordered, hash: list.Hash, expireAt: expireAt}
}

func cloneCachedContactMap(in map[int64]domain.Contact) map[int64]domain.Contact {
	out := make(map[int64]domain.Contact, len(in))
	for id, contact := range in {
		out[id] = cloneCachedContact(contact)
	}
	return out
}

func cloneContactProjectionBatch(in domain.ContactProjectionBatch) domain.ContactProjectionBatch {
	out := domain.ContactProjectionBatch{
		Contacts:       make(map[int64]map[int64]domain.Contact, len(in.Contacts)),
		PersonalPhotos: make(map[int64]map[int64]domain.ProfilePhotoRef, len(in.PersonalPhotos)),
	}
	mergeContactProjectionBatch(&out, in)
	return out
}

func mergeContactProjectionBatch(dst *domain.ContactProjectionBatch, src domain.ContactProjectionBatch) {
	for viewerID, contacts := range src.Contacts {
		for targetID, contact := range contacts {
			putContactProjectionContact(dst, viewerID, targetID, contact)
		}
	}
	for viewerID, refs := range src.PersonalPhotos {
		for targetID, ref := range refs {
			putContactProjectionPersonalPhoto(dst, viewerID, targetID, ref)
		}
	}
}

func putContactProjectionContact(batch *domain.ContactProjectionBatch, viewerID, targetID int64, contact domain.Contact) {
	if batch.Contacts == nil {
		batch.Contacts = map[int64]map[int64]domain.Contact{}
	}
	if batch.Contacts[viewerID] == nil {
		batch.Contacts[viewerID] = map[int64]domain.Contact{}
	}
	batch.Contacts[viewerID][targetID] = cloneCachedContact(contact)
}

func putContactProjectionPersonalPhoto(batch *domain.ContactProjectionBatch, viewerID, targetID int64, ref domain.ProfilePhotoRef) {
	if batch.PersonalPhotos == nil {
		batch.PersonalPhotos = map[int64]map[int64]domain.ProfilePhotoRef{}
	}
	if batch.PersonalPhotos[viewerID] == nil {
		batch.PersonalPhotos[viewerID] = map[int64]domain.ProfilePhotoRef{}
	}
	batch.PersonalPhotos[viewerID][targetID] = cloneCachedProfilePhotoRef(ref)
}

func dedupContactIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func cloneCachedContacts(in []domain.Contact) []domain.Contact {
	out := make([]domain.Contact, len(in))
	for i := range in {
		out[i] = cloneCachedContact(in[i])
	}
	return out
}

func cloneCachedContact(in domain.Contact) domain.Contact {
	in.User = cloneCachedUser(in.User)
	if in.NoteEntities != nil {
		in.NoteEntities = append([]domain.MessageEntity(nil), in.NoteEntities...)
	}
	return in
}

func cloneCachedUser(in domain.User) domain.User {
	if in.PhotoStripped != nil {
		in.PhotoStripped = append([]byte(nil), in.PhotoStripped...)
	}
	if in.ContactNoteEntities != nil {
		in.ContactNoteEntities = append([]domain.MessageEntity(nil), in.ContactNoteEntities...)
	}
	if in.RestrictionReasons != nil {
		in.RestrictionReasons = append([]domain.UserRestrictionReason(nil), in.RestrictionReasons...)
	}
	return in
}

func cloneCachedProfilePhotoRefs(in map[int64]domain.ProfilePhotoRef) map[int64]domain.ProfilePhotoRef {
	out := make(map[int64]domain.ProfilePhotoRef, len(in))
	for id, ref := range in {
		out[id] = cloneCachedProfilePhotoRef(ref)
	}
	return out
}

func cloneCachedProfilePhotoRef(in domain.ProfilePhotoRef) domain.ProfilePhotoRef {
	if in.Stripped != nil {
		in.Stripped = append([]byte(nil), in.Stripped...)
	}
	return in
}
