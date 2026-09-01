package userprojection

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
	"unsafe"

	"telesrv/internal/domain"
	"telesrv/internal/store"
	"telesrv/internal/store/memory"
)

type blockingFirstListContactStore struct {
	store.ContactStore
	started chan struct{}
	release chan struct{}
	first   domain.ContactList

	mu        sync.Mutex
	firstUsed bool
	listCalls int
}

type blockingFirstPersonalPhotoStore struct {
	store.ContactStore
	started chan struct{}
	release chan struct{}
	first   map[int64]domain.ProfilePhotoRef

	mu        sync.Mutex
	firstUsed bool
}

type stalePersonalPhotoWritebackContextKey struct{}

// stalePersonalPhotoWritebackStore deterministically models an older mutation
// that commits first but returns to the cache wrapper after a newer mutation.
// The old implementation performed a post-commit PersonalPhotos read and could
// publish this captured old value after the newer mutation had completed.
type stalePersonalPhotoWritebackStore struct {
	store.ContactStore
	started chan struct{}
	release chan struct{}

	mu             sync.Mutex
	staleReadCalls int
}

func (s *stalePersonalPhotoWritebackStore) SetPersonalPhoto(ctx context.Context, userID, contactUserID int64, photoID int64, date int) (domain.Contact, bool, error) {
	contact, found, err := s.ContactStore.SetPersonalPhoto(ctx, userID, contactUserID, photoID, date)
	if err != nil || !found || ctx.Value(stalePersonalPhotoWritebackContextKey{}) != true {
		return contact, found, err
	}
	close(s.started)
	select {
	case <-s.release:
	case <-ctx.Done():
		return domain.Contact{}, false, ctx.Err()
	}
	return contact, found, nil
}

func (s *stalePersonalPhotoWritebackStore) PersonalPhotos(ctx context.Context, userID int64, contactUserIDs []int64) (map[int64]domain.ProfilePhotoRef, error) {
	if len(contactUserIDs) > 0 && ctx.Value(stalePersonalPhotoWritebackContextKey{}) == true {
		s.mu.Lock()
		s.staleReadCalls++
		s.mu.Unlock()
		return map[int64]domain.ProfilePhotoRef{
			contactUserIDs[0]: {PhotoID: 9001, Personal: true},
		}, nil
	}
	return s.ContactStore.PersonalPhotos(ctx, userID, contactUserIDs)
}

func (s *stalePersonalPhotoWritebackStore) staleReads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.staleReadCalls
}

func (s *blockingFirstListContactStore) ListByUser(ctx context.Context, userID int64) (domain.ContactList, error) {
	s.mu.Lock()
	s.listCalls++
	if !s.firstUsed {
		s.firstUsed = true
		s.mu.Unlock()
		close(s.started)
		select {
		case <-s.release:
		case <-ctx.Done():
			return domain.ContactList{}, ctx.Err()
		}
		return domain.ContactList{Contacts: cloneCachedContacts(s.first.Contacts), Hash: s.first.Hash}, nil
	}
	s.mu.Unlock()
	return s.ContactStore.ListByUser(ctx, userID)
}

func (s *blockingFirstListContactStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listCalls
}

func (s *blockingFirstPersonalPhotoStore) PersonalPhotos(ctx context.Context, userID int64, contactUserIDs []int64) (map[int64]domain.ProfilePhotoRef, error) {
	s.mu.Lock()
	if !s.firstUsed {
		s.firstUsed = true
		first := cloneCachedProfilePhotoRefs(s.first)
		s.mu.Unlock()
		close(s.started)
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return first, nil
	}
	s.mu.Unlock()
	return s.ContactStore.PersonalPhotos(ctx, userID, contactUserIDs)
}

func (s *blockingFirstPersonalPhotoStore) SetPersonalPhoto(ctx context.Context, userID, contactUserID int64, photoID int64, date int) (domain.Contact, bool, error) {
	return s.ContactStore.SetPersonalPhoto(ctx, userID, contactUserID, photoID, date)
}

func waitForCacheTestSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cache test signal")
	}
}

type countingContactStore struct {
	store.ContactStore
	listCalls           int
	getManyCalls        int
	reverseCalls        int
	projectionCalls     int
	personalPhotoCalls  int
	setPersonalPhotoHit int
}

func (s *countingContactStore) ListByUser(ctx context.Context, userID int64) (domain.ContactList, error) {
	s.listCalls++
	return s.ContactStore.ListByUser(ctx, userID)
}

func (s *countingContactStore) GetMany(ctx context.Context, userID int64, contactUserIDs []int64) (map[int64]domain.Contact, error) {
	s.getManyCalls++
	return s.ContactStore.GetMany(ctx, userID, contactUserIDs)
}

func (s *countingContactStore) GetReverseContacts(ctx context.Context, userID int64, ownerUserIDs []int64) (map[int64]domain.Contact, error) {
	s.reverseCalls++
	return s.ContactStore.GetReverseContacts(ctx, userID, ownerUserIDs)
}

func (s *countingContactStore) ContactProjectionForViewers(ctx context.Context, viewerUserIDs, contactUserIDs []int64) (domain.ContactProjectionBatch, error) {
	s.projectionCalls++
	return s.ContactStore.ContactProjectionForViewers(ctx, viewerUserIDs, contactUserIDs)
}

func (s *countingContactStore) PersonalPhotos(ctx context.Context, userID int64, contactUserIDs []int64) (map[int64]domain.ProfilePhotoRef, error) {
	s.personalPhotoCalls++
	return s.ContactStore.PersonalPhotos(ctx, userID, contactUserIDs)
}

func (s *countingContactStore) SetPersonalPhoto(ctx context.Context, userID, contactUserID int64, photoID int64, date int) (domain.Contact, bool, error) {
	s.setPersonalPhotoHit++
	return s.ContactStore.SetPersonalPhoto(ctx, userID, contactUserID, photoID, date)
}

func TestCachedContactStoreCachesProjectionReads(t *testing.T) {
	ctx := context.Background()
	base := memory.NewContactStore()
	if _, err := base.Upsert(ctx, 1, domain.ContactInput{
		ContactUserID: 2,
		FirstName:     "Alice",
		Phone:         "111",
		Note:          "private note",
		NoteEntities:  []domain.MessageEntity{{Type: domain.MessageEntityBold, Offset: 0, Length: 7}},
	}); err != nil {
		t.Fatalf("upsert contact: %v", err)
	}
	counting := &countingContactStore{ContactStore: base}
	cached := NewCachedContactStore(counting, 0)

	first, err := cached.GetMany(ctx, 1, []int64{2, 3})
	if err != nil {
		t.Fatalf("get many first: %v", err)
	}
	if first[2].FirstName != "Alice" || first[2].Note != "private note" || len(first[2].NoteEntities) != 1 {
		t.Fatalf("first contact = %+v, want Alice with private note", first[2])
	}
	first[2].NoteEntities[0].Length = 99
	second, err := cached.GetMany(ctx, 1, []int64{2, 3})
	if err != nil {
		t.Fatalf("get many second: %v", err)
	}
	if second[2].FirstName != "Alice" || second[2].Note != "private note" || len(second[2].NoteEntities) != 1 || second[2].NoteEntities[0].Length != 7 {
		t.Fatalf("second contact = %+v, want isolated cached Alice note", second[2])
	}
	if counting.listCalls != 1 {
		t.Fatalf("ListByUser calls = %d, want 1 account snapshot load", counting.listCalls)
	}
	if counting.getManyCalls != 0 {
		t.Fatalf("GetMany calls = %d, want 0 with account snapshot", counting.getManyCalls)
	}

	reverse, err := cached.GetReverseContacts(ctx, 2, []int64{1})
	if err != nil {
		t.Fatalf("get reverse: %v", err)
	}
	if reverse[1].FirstName != "Alice" {
		t.Fatalf("reverse contact = %+v, want Alice", reverse[1])
	}
	if counting.reverseCalls != 0 {
		t.Fatalf("GetReverseContacts calls = %d, want 0 from shared contact cache", counting.reverseCalls)
	}
}

func TestCachedContactStoreContactSnapshotLRUEvictsOnlyOldestViewer(t *testing.T) {
	ctx := context.Background()
	base := memory.NewContactStore()
	for viewerID := int64(1); viewerID <= 3; viewerID++ {
		if _, err := base.Upsert(ctx, viewerID, domain.ContactInput{
			ContactUserID: 100 + viewerID,
			FirstName:     fmt.Sprintf("viewer-%d", viewerID),
		}); err != nil {
			t.Fatalf("seed viewer %d: %v", viewerID, err)
		}
	}
	counting := &countingContactStore{ContactStore: base}
	cached := NewCachedContactStoreWithMaxViewers(counting, time.Hour, 2)

	for _, viewerID := range []int64{1, 2, 1, 3} {
		if _, err := cached.ListByUser(ctx, viewerID); err != nil {
			t.Fatalf("list viewer %d: %v", viewerID, err)
		}
	}
	if counting.listCalls != 3 {
		t.Fatalf("ListByUser calls = %d, want 3 before evicted viewer is read", counting.listCalls)
	}
	cached.mu.RLock()
	_, hasOne := cached.contacts[1]
	_, hasTwo := cached.contacts[2]
	_, hasThree := cached.contacts[3]
	contactEntries := cached.contactLRU.Len()
	cached.mu.RUnlock()
	if !hasOne || hasTwo || !hasThree || contactEntries != 2 {
		t.Fatalf("contact LRU state = one:%v two:%v three:%v len:%d, want one+three only", hasOne, hasTwo, hasThree, contactEntries)
	}

	if _, err := cached.ListByUser(ctx, 1); err != nil {
		t.Fatalf("list retained viewer 1: %v", err)
	}
	if counting.listCalls != 3 {
		t.Fatalf("retained viewer caused cold load: calls=%d, want 3", counting.listCalls)
	}
	if _, err := cached.ListByUser(ctx, 2); err != nil {
		t.Fatalf("list evicted viewer 2: %v", err)
	}
	if counting.listCalls != 4 {
		t.Fatalf("evicted viewer did not cold load exactly once: calls=%d, want 4", counting.listCalls)
	}
}

func TestCachedContactStoreContactAndPersonalPhotoLRUsAreIndependent(t *testing.T) {
	cached := NewCachedContactStoreWithMaxViewers(memory.NewContactStore(), time.Hour, 2)
	expireAt := time.Now().Add(time.Hour)
	contactSnap := func(userID int64) contactAccountSnapshot {
		return buildContactAccountSnapshot(domain.ContactList{Contacts: []domain.Contact{{
			User: domain.User{ID: 100 + userID},
		}}}, expireAt)
	}
	photoSnap := func(userID int64) personalPhotoSnapshot {
		return personalPhotoSnapshot{
			refs:     map[int64]domain.ProfilePhotoRef{100 + userID: {PhotoID: 9000 + userID}},
			expireAt: expireAt,
		}
	}

	cached.mu.Lock()
	cached.storeContactSnapshotLocked(1, contactSnap(1))
	cached.storeContactSnapshotLocked(2, contactSnap(2))
	cached.storePersonalPhotoSnapshotLocked(1, photoSnap(1))
	cached.storePersonalPhotoSnapshotLocked(2, photoSnap(2))
	cached.mu.Unlock()
	if _, ok := cached.lookupContactSnapshot(1, time.Now()); !ok {
		t.Fatal("contact viewer 1 missing before LRU touch")
	}
	cached.mu.Lock()
	cached.storeContactSnapshotLocked(3, contactSnap(3))
	cached.mu.Unlock()

	cached.mu.RLock()
	_, contactOne := cached.contacts[1]
	_, contactTwo := cached.contacts[2]
	_, contactThree := cached.contacts[3]
	_, photoOne := cached.personalPhotos[1]
	_, photoTwo := cached.personalPhotos[2]
	cached.mu.RUnlock()
	if !contactOne || contactTwo || !contactThree {
		t.Fatalf("contact LRU = one:%v two:%v three:%v, want one+three", contactOne, contactTwo, contactThree)
	}
	if !photoOne || !photoTwo {
		t.Fatalf("contact eviction crossed into personal-photo LRU: one:%v two:%v", photoOne, photoTwo)
	}

	if _, ok := cached.lookupPersonalPhotoSnapshot(2, time.Now()); !ok {
		t.Fatal("personal-photo viewer 2 missing before LRU touch")
	}
	cached.mu.Lock()
	cached.storePersonalPhotoSnapshotLocked(3, photoSnap(3))
	cached.mu.Unlock()
	cached.mu.RLock()
	_, photoOne = cached.personalPhotos[1]
	_, photoTwo = cached.personalPhotos[2]
	_, photoThree := cached.personalPhotos[3]
	_, contactOne = cached.contacts[1]
	_, contactThree = cached.contacts[3]
	cached.mu.RUnlock()
	if photoOne || !photoTwo || !photoThree {
		t.Fatalf("personal-photo LRU = one:%v two:%v three:%v, want two+three", photoOne, photoTwo, photoThree)
	}
	if !contactOne || !contactThree {
		t.Fatalf("personal-photo eviction crossed into contact LRU: one:%v three:%v", contactOne, contactThree)
	}

	cached.InvalidateViewers(3)
	cached.mu.RLock()
	_, contactThree = cached.contacts[3]
	_, photoThree = cached.personalPhotos[3]
	_, contactElement := cached.contactElements[3]
	_, photoElement := cached.personalElements[3]
	cached.mu.RUnlock()
	if contactThree || photoThree || contactElement || photoElement {
		t.Fatalf("viewer invalidation left LRU state: contact=%v photo=%v contactElement=%v photoElement=%v",
			contactThree, photoThree, contactElement, photoElement)
	}
}

func TestCachedContactStoreUnrelatedViewerInvalidationDoesNotRejectRefill(t *testing.T) {
	ctx := context.Background()
	base := memory.NewContactStore()
	if _, err := base.Upsert(ctx, 2, domain.ContactInput{ContactUserID: 20, FirstName: "current"}); err != nil {
		t.Fatalf("seed current contact: %v", err)
	}
	blocking := &blockingFirstListContactStore{
		ContactStore: base,
		started:      make(chan struct{}),
		release:      make(chan struct{}),
		first: domain.ContactList{Contacts: []domain.Contact{{
			User:      domain.User{ID: 20},
			FirstName: "captured",
		}}},
	}
	cached := NewCachedContactStore(blocking, time.Hour)

	type readResult struct {
		contacts map[int64]domain.Contact
		err      error
	}
	resultCh := make(chan readResult, 1)
	go func() {
		contacts, err := cached.GetMany(ctx, 2, []int64{20})
		resultCh <- readResult{contacts: contacts, err: err}
	}()
	waitForCacheTestSignal(t, blocking.started)
	cached.InvalidateViewers(1)
	close(blocking.release)

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("contact read: %v", result.err)
		}
		if got := result.contacts[20].FirstName; got != "captured" {
			t.Fatalf("unrelated invalidation rejected captured refill: got %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for contact read")
	}
	if calls := blocking.callCount(); calls != 1 {
		t.Fatalf("ListByUser calls = %d, want 1 after unrelated invalidation", calls)
	}
}

func TestCachedContactStoreContactProjectionForViewersUsesViewerOwnedPairCache(t *testing.T) {
	ctx := context.Background()
	base := memory.NewContactStore()
	if _, err := base.Upsert(ctx, 1, domain.ContactInput{ContactUserID: 2, FirstName: "Alice"}); err != nil {
		t.Fatalf("seed viewer 1 contact: %v", err)
	}
	if _, _, err := base.SetPersonalPhoto(ctx, 1, 2, 9101, 100); err != nil {
		t.Fatalf("seed viewer 1 personal photo: %v", err)
	}
	if _, err := base.Upsert(ctx, 3, domain.ContactInput{ContactUserID: 2, FirstName: "Bob"}); err != nil {
		t.Fatalf("seed viewer 3 contact: %v", err)
	}
	if _, _, err := base.SetPersonalPhoto(ctx, 3, 2, 9103, 100); err != nil {
		t.Fatalf("seed viewer 3 personal photo: %v", err)
	}
	counting := &countingContactStore{ContactStore: base}
	cached := NewCachedContactStore(counting, 0)

	if _, err := cached.GetMany(ctx, 1, []int64{2}); err != nil {
		t.Fatalf("prime viewer 1 contacts: %v", err)
	}
	if _, err := cached.PersonalPhotos(ctx, 1, []int64{2}); err != nil {
		t.Fatalf("prime viewer 1 photos: %v", err)
	}

	first, err := cached.ContactProjectionForViewers(ctx, []int64{1, 3}, []int64{2})
	if err != nil {
		t.Fatalf("first projection: %v", err)
	}
	if first.Contacts[1][2].FirstName != "Alice" || first.PersonalPhotos[1][2].PhotoID != 9101 {
		t.Fatalf("viewer 1 projection = %+v %+v, want warm Alice/9101", first.Contacts[1][2], first.PersonalPhotos[1][2])
	}
	if first.Contacts[3][2].FirstName != "Bob" || first.PersonalPhotos[3][2].PhotoID != 9103 {
		t.Fatalf("viewer 3 projection = %+v %+v, want cold Bob/9103", first.Contacts[3][2], first.PersonalPhotos[3][2])
	}
	if counting.projectionCalls != 1 {
		t.Fatalf("projection calls after first = %d, want 1", counting.projectionCalls)
	}

	second, err := cached.ContactProjectionForViewers(ctx, []int64{3}, []int64{2})
	if err != nil {
		t.Fatalf("second projection: %v", err)
	}
	if second.Contacts[3][2].FirstName != "Bob" || second.PersonalPhotos[3][2].PhotoID != 9103 {
		t.Fatalf("cached viewer 3 projection = %+v %+v, want Bob/9103", second.Contacts[3][2], second.PersonalPhotos[3][2])
	}
	if counting.projectionCalls != 1 {
		t.Fatalf("projection calls after cached read = %d, want 1", counting.projectionCalls)
	}

	cached.InvalidateViewers(3)
	if _, err := cached.ContactProjectionForViewers(ctx, []int64{3}, []int64{2}); err != nil {
		t.Fatalf("projection after invalidation: %v", err)
	}
	if counting.projectionCalls != 2 {
		t.Fatalf("projection calls after invalidation = %d, want 2", counting.projectionCalls)
	}
}

func TestCachedContactStorePairSnapshotsAreCompact(t *testing.T) {
	pointerSize := unsafe.Sizeof(uintptr(0))
	timeSize := unsafe.Sizeof(time.Time{})
	if got, max := unsafe.Sizeof(reverseContactSnapshot{}), timeSize+2*pointerSize; got > max {
		t.Fatalf("reverseContactSnapshot size = %d, want <= %d (one value pointer plus expiry)", got, max)
	}
	if got, max := unsafe.Sizeof(contactProjectionSnapshot{}), timeSize+3*pointerSize; got > max {
		t.Fatalf("contactProjectionSnapshot size = %d, want <= %d (two value pointers plus expiry)", got, max)
	}
	if got, large := unsafe.Sizeof(reverseContactSnapshot{}), unsafe.Sizeof(domain.Contact{}); got >= large {
		t.Fatalf("reverseContactSnapshot size = %d, must not embed %d-byte domain.Contact", got, large)
	}
	if got, large := unsafe.Sizeof(contactProjectionSnapshot{}), unsafe.Sizeof(domain.Contact{}); got >= large {
		t.Fatalf("contactProjectionSnapshot size = %d, must not embed %d-byte domain.Contact", got, large)
	}
	if got, max := unsafe.Sizeof(cachedContactProjectionOverlay{}), uintptr(128); got > max {
		t.Fatalf("cachedContactProjectionOverlay size = %d, want <= %d bytes", got, max)
	}
	if got, large := unsafe.Sizeof(cachedContactProjectionOverlay{}), unsafe.Sizeof(domain.Contact{}); got >= large {
		t.Fatalf("cachedContactProjectionOverlay size = %d, must be smaller than %d-byte domain.Contact", got, large)
	}
}

func TestCachedContactStorePairSnapshotsUseNilForNegativeAndClonePositiveValues(t *testing.T) {
	cached := NewCachedContactStore(memory.NewContactStore(), time.Hour)
	now := time.Unix(1000, 0)
	expireAt := now.Add(time.Hour)
	contact := domain.Contact{
		User: domain.User{
			ID: 2, AccessHash: 2002, Phone: "global-phone", FirstName: "Global", LastName: "User",
			Username: "global_user", Mutual: true, PhotoStripped: []byte{1, 2, 3},
		},
		FirstName: "Local",
		LastName:  "Name",
		Phone:     "known-phone",
		Note:      "private note",
		NoteEntities: []domain.MessageEntity{{
			Type: domain.MessageEntityBold, Offset: 0, Length: 3,
		}},
		CloseFriend: true,
	}
	photo := domain.ProfilePhotoRef{PhotoID: 9001, Stripped: []byte{4, 5, 6}, Personal: true}
	positiveReverseKey := reverseContactKey{ownerUserID: 1, contactUserID: 2}
	negativeReverseKey := reverseContactKey{ownerUserID: 3, contactUserID: 2}
	positiveProjectionKey := contactProjectionKey{viewerUserID: 1, contactUserID: 2}
	negativeProjectionKey := contactProjectionKey{viewerUserID: 1, contactUserID: 99}

	cached.mu.Lock()
	cached.storeReverseContactLocked(positiveReverseKey, contact, true, expireAt)
	cached.storeReverseContactLocked(negativeReverseKey, contact, false, expireAt)
	cached.storeContactProjectionPairLocked(positiveProjectionKey, contact, true, photo, true, expireAt)
	cached.storeContactProjectionPairLocked(negativeProjectionKey, contact, false, photo, false, expireAt)
	positiveReverse := cached.reverse[positiveReverseKey].Value.(*reverseContactEntry).snapshot
	negativeReverse := cached.reverse[negativeReverseKey].Value.(*reverseContactEntry).snapshot
	positiveProjection := cached.projection[positiveProjectionKey].Value.(*contactProjectionEntry).snapshot
	negativeProjection := cached.projection[negativeProjectionKey].Value.(*contactProjectionEntry).snapshot
	cached.mu.Unlock()

	if positiveReverse.contact == nil || positiveProjection.contact == nil || positiveProjection.personalPhoto == nil {
		t.Fatalf("positive snapshots lost values: reverse=%+v projection=%+v", positiveReverse, positiveProjection)
	}
	if negativeReverse.contact != nil || negativeProjection.contact != nil || negativeProjection.personalPhoto != nil {
		t.Fatalf("negative snapshots retained value allocations: reverse=%+v projection=%+v", negativeReverse, negativeProjection)
	}

	// Publication clones inputs; subsequent caller mutation cannot alter cache.
	contact.User.PhotoStripped[0] = 10
	contact.NoteEntities[0].Length = 10
	photo.Stripped[0] = 10

	reverse, found, hit := cached.lookupReverseContact(1, 2, now)
	if !hit || !found || reverse.User.PhotoStripped[0] != 1 || reverse.NoteEntities[0].Length != 3 {
		t.Fatalf("positive reverse lookup = %+v found=%v hit=%v", reverse, found, hit)
	}
	reverse.User.PhotoStripped[0] = 11
	reverse.NoteEntities[0].Length = 11
	reverseAgain, found, hit := cached.lookupReverseContact(1, 2, now)
	if !hit || !found || reverseAgain.User.PhotoStripped[0] != 1 || reverseAgain.NoteEntities[0].Length != 3 {
		t.Fatalf("reverse lookup shared mutable slices: %+v found=%v hit=%v", reverseAgain, found, hit)
	}
	if _, found, hit := cached.lookupReverseContact(3, 2, now); !hit || found {
		t.Fatalf("negative reverse lookup found=%v hit=%v, want false/true", found, hit)
	}

	pair, hit := cached.lookupContactProjectionPair(1, 2, now)
	if !hit || !pair.contactFound || !pair.personalPhotoFound || pair.personalPhoto.Stripped[0] != 4 {
		t.Fatalf("positive projection lookup = %+v hit=%v", pair, hit)
	}
	if !reflect.DeepEqual(pair.contact.User, domain.User{ID: 2}) {
		t.Fatalf("projection pair retained base user data: %+v", pair.contact.User)
	}
	if pair.contact.FirstName != "Local" || pair.contact.LastName != "Name" || pair.contact.Phone != "known-phone" ||
		pair.contact.Note != "private note" || !pair.contact.Mutual || !pair.contact.CloseFriend {
		t.Fatalf("projection pair lost viewer-owned overlay: %+v", pair.contact)
	}
	pair.contact.NoteEntities[0].Length = 12
	pair.personalPhoto.Stripped[0] = 12
	pairAgain, hit := cached.lookupContactProjectionPair(1, 2, now)
	if !hit || !reflect.DeepEqual(pairAgain.contact.User, domain.User{ID: 2}) || pairAgain.contact.NoteEntities[0].Length != 3 || pairAgain.personalPhoto.Stripped[0] != 4 {
		t.Fatalf("projection lookup shared mutable slices: %+v hit=%v", pairAgain, hit)
	}
	negative, hit := cached.lookupContactProjectionPair(1, 99, now)
	if !hit || negative.contactFound || negative.personalPhotoFound {
		t.Fatalf("negative projection lookup = %+v hit=%v, want cached miss", negative, hit)
	}
}

func TestCachedContactStoreLargeDenseProjectionDoesNotPollutePairCache(t *testing.T) {
	if !admitDenseContactProjectionPairs(1, contactProjectionDenseAdmissionMaxCells) {
		t.Fatal("admission rejected the documented cell limit")
	}
	if admitDenseContactProjectionPairs(1, contactProjectionDenseAdmissionMaxCells+1) {
		t.Fatal("admission accepted a batch above the documented cell limit")
	}

	counting := &countingContactStore{ContactStore: memory.NewContactStore()}
	cached := NewCachedContactStore(counting, time.Hour)
	seedKey := contactProjectionKey{viewerUserID: 1, contactUserID: 2}
	cached.mu.Lock()
	cached.storeContactProjectionPairLocked(
		seedKey,
		domain.Contact{User: domain.User{ID: 2}, FirstName: "seed"}, true,
		domain.ProfilePhotoRef{}, false,
		cached.now().Add(time.Hour),
	)
	cached.mu.Unlock()

	viewers := []int64{1001, 1002}
	targets := make([]int64, contactProjectionDenseAdmissionMaxCells/len(viewers)+1)
	for i := range targets {
		targets[i] = int64(100000 + i)
	}
	for call := 1; call <= 2; call++ {
		got, err := cached.ContactProjectionForViewers(context.Background(), viewers, targets)
		if err != nil {
			t.Fatalf("large dense projection call %d: %v", call, err)
		}
		if len(got.Contacts) != 0 || len(got.PersonalPhotos) != 0 {
			t.Fatalf("large empty projection call %d = %+v", call, got)
		}
		cached.mu.Lock()
		_, seedPresent := cached.projection[seedKey]
		pairCount := len(cached.projection)
		lruCount := cached.projectionLRU.Len()
		cached.mu.Unlock()
		if !seedPresent || pairCount != 1 || lruCount != 1 {
			t.Fatalf("large dense load polluted pair cache: seed=%v pairs=%d lru=%d", seedPresent, pairCount, lruCount)
		}
	}
	if counting.projectionCalls != 2 {
		t.Fatalf("projection calls = %d, want 2 because oversized results are returned but not admitted", counting.projectionCalls)
	}
}

func TestCachedContactStoreContactProjectionSkipsColdReadForKnownNonContact(t *testing.T) {
	ctx := context.Background()
	base := memory.NewContactStore()
	if _, err := base.Upsert(ctx, 1, domain.ContactInput{ContactUserID: 2, FirstName: "Alice"}); err != nil {
		t.Fatalf("seed contact: %v", err)
	}
	counting := &countingContactStore{ContactStore: base}
	cached := NewCachedContactStore(counting, 0)

	if _, err := cached.GetMany(ctx, 1, []int64{99}); err != nil {
		t.Fatalf("prime viewer contact snapshot: %v", err)
	}
	got, err := cached.ContactProjectionForViewers(ctx, []int64{1}, []int64{99})
	if err != nil {
		t.Fatalf("projection: %v", err)
	}
	if len(got.Contacts[1]) != 0 || len(got.PersonalPhotos[1]) != 0 {
		t.Fatalf("known non-contact projection = %+v", got)
	}
	if counting.projectionCalls != 0 {
		t.Fatalf("projection calls = %d, want 0 for known non-contact", counting.projectionCalls)
	}
	if counting.personalPhotoCalls != 0 {
		t.Fatalf("personal photo calls = %d, want 0 for known non-contact", counting.personalPhotoCalls)
	}
}

func TestCachedContactStoreCachesLargeReverseContactBatch(t *testing.T) {
	ctx := context.Background()
	base := memory.NewContactStore()
	owners := make([]int64, 32)
	for i := range owners {
		owners[i] = int64(i + 1)
		if i%2 == 0 {
			if _, err := base.Upsert(ctx, owners[i], domain.ContactInput{
				ContactUserID: 9001,
				FirstName:     "Viewer",
			}); err != nil {
				t.Fatalf("seed owner %d: %v", owners[i], err)
			}
		}
	}
	counting := &countingContactStore{ContactStore: base}
	cached := NewCachedContactStore(counting, 0)

	first, err := cached.GetReverseContacts(ctx, 9001, owners)
	if err != nil {
		t.Fatalf("first reverse lookup: %v", err)
	}
	if len(first) != 16 || counting.reverseCalls != 1 || counting.listCalls != 0 {
		t.Fatalf("first reverse hits=%d reverseCalls=%d listCalls=%d, want 16/1/0", len(first), counting.reverseCalls, counting.listCalls)
	}
	second, err := cached.GetReverseContacts(ctx, 9001, owners)
	if err != nil {
		t.Fatalf("second reverse lookup: %v", err)
	}
	if len(second) != 16 || counting.reverseCalls != 1 || counting.listCalls != 0 {
		t.Fatalf("cached reverse hits=%d reverseCalls=%d listCalls=%d, want 16/1/0", len(second), counting.reverseCalls, counting.listCalls)
	}

	cached.InvalidateViewers(owners[0])
	third, err := cached.GetReverseContacts(ctx, 9001, owners)
	if err != nil {
		t.Fatalf("reverse lookup after owner invalidation: %v", err)
	}
	if len(third) != 16 || counting.reverseCalls != 2 {
		t.Fatalf("invalidated reverse hits=%d reverseCalls=%d, want 16/2", len(third), counting.reverseCalls)
	}
}

func TestCachedContactStoreReversePairsUsePerEntryLRU(t *testing.T) {
	ctx := context.Background()
	base := memory.NewContactStore()
	for ownerID := int64(1); ownerID <= 3; ownerID++ {
		if _, err := base.Upsert(ctx, ownerID, domain.ContactInput{
			ContactUserID: 9001,
			FirstName:     "Viewer",
		}); err != nil {
			t.Fatalf("seed owner %d: %v", ownerID, err)
		}
	}
	counting := &countingContactStore{ContactStore: base}
	cached := NewCachedContactStore(counting, 0)
	cached.reverseCap = 2

	for _, ownerID := range []int64{1, 2, 1, 3, 1, 2} {
		got, err := cached.GetReverseContacts(ctx, 9001, []int64{ownerID})
		if err != nil {
			t.Fatalf("reverse owner %d: %v", ownerID, err)
		}
		if _, ok := got[ownerID]; !ok {
			t.Fatalf("reverse owner %d missing", ownerID)
		}
	}
	if counting.reverseCalls != 4 {
		t.Fatalf("reverse calls = %d, want 4 (owner 1 touched, owner 2 evicted only)", counting.reverseCalls)
	}
	if len(cached.reverse) != 2 || cached.reverseLRU.Len() != 2 {
		t.Fatalf("reverse cache map/list = %d/%d, want 2/2", len(cached.reverse), cached.reverseLRU.Len())
	}
}

func TestCachedContactStoreInvalidatesAccountSnapshotAfterMutation(t *testing.T) {
	ctx := context.Background()
	base := memory.NewContactStore()
	if _, err := base.Upsert(ctx, 1, domain.ContactInput{ContactUserID: 2, FirstName: "Alice"}); err != nil {
		t.Fatalf("upsert contact: %v", err)
	}
	counting := &countingContactStore{ContactStore: base}
	cached := NewCachedContactStore(counting, 0)

	first, err := cached.GetMany(ctx, 1, []int64{2})
	if err != nil {
		t.Fatalf("get first: %v", err)
	}
	if first[2].FirstName != "Alice" {
		t.Fatalf("first = %+v, want Alice", first[2])
	}
	if _, err := cached.Upsert(ctx, 1, domain.ContactInput{ContactUserID: 2, FirstName: "Alicia"}); err != nil {
		t.Fatalf("upsert through cache: %v", err)
	}
	second, err := cached.GetMany(ctx, 1, []int64{2})
	if err != nil {
		t.Fatalf("get second: %v", err)
	}
	if second[2].FirstName != "Alicia" {
		t.Fatalf("second = %+v, want Alicia after invalidation", second[2])
	}
	if counting.listCalls != 2 {
		t.Fatalf("ListByUser calls = %d, want 2 after safe invalidation and reload", counting.listCalls)
	}
}

func TestCachedContactStorePublishedSnapshotsStayImmutableDuringMutations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(context.Context, *CachedContactStore) error
	}{
		{
			name: "upsert",
			mutate: func(ctx context.Context, cached *CachedContactStore) error {
				_, err := cached.Upsert(ctx, 1, domain.ContactInput{ContactUserID: 2, FirstName: "After"})
				return err
			},
		},
		{
			name: "delete",
			mutate: func(ctx context.Context, cached *CachedContactStore) error {
				_, err := cached.Delete(ctx, 1, []int64{2})
				return err
			},
		},
		{
			name: "close_friends",
			mutate: func(ctx context.Context, cached *CachedContactStore) error {
				_, err := cached.SetCloseFriends(ctx, 1, []int64{2})
				return err
			},
		},
		{
			name: "personal_photo",
			mutate: func(ctx context.Context, cached *CachedContactStore) error {
				_, found, err := cached.SetPersonalPhoto(ctx, 1, 2, 9002, 101)
				if err == nil && !found {
					return fmt.Errorf("contact not found")
				}
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			base := memory.NewContactStore()
			if _, err := base.Upsert(ctx, 1, domain.ContactInput{ContactUserID: 2, FirstName: "Before"}); err != nil {
				t.Fatalf("seed contact: %v", err)
			}
			if _, found, err := base.SetPersonalPhoto(ctx, 1, 2, 9001, 100); err != nil || !found {
				t.Fatalf("seed personal photo: %v found=%v", err, found)
			}
			cached := NewCachedContactStore(base, 0)
			if _, err := cached.GetMany(ctx, 1, []int64{2}); err != nil {
				t.Fatalf("warm contacts: %v", err)
			}
			if _, err := cached.PersonalPhotos(ctx, 1, []int64{2}); err != nil {
				t.Fatalf("warm personal photos: %v", err)
			}

			cached.mu.RLock()
			contactSnap, contactsWarm := cached.contacts[1]
			photoSnap, photosWarm := cached.personalPhotos[1]
			cached.mu.RUnlock()
			if !contactsWarm || !photosWarm {
				t.Fatal("snapshots were not warm before mutation")
			}

			started := make(chan struct{})
			stop := make(chan struct{})
			done := make(chan struct{})
			go func() {
				defer close(done)
				signaled := false
				for {
					contact := contactSnap.contacts[2]
					for i := range contactSnap.ordered {
						_ = contactSnap.ordered[i].User.ID
					}
					ref := photoSnap.refs[2]
					_, _ = contact.FirstName, ref.PhotoID
					if !signaled {
						close(started)
						signaled = true
					}
					select {
					case <-stop:
						return
					default:
					}
				}
			}()
			waitForCacheTestSignal(t, started)
			if err := tc.mutate(ctx, cached); err != nil {
				close(stop)
				<-done
				t.Fatalf("mutation: %v", err)
			}
			close(stop)
			<-done

			// A snapshot obtained before invalidation remains a valid immutable
			// value for an in-flight reader; only the outer cache entry is removed.
			if got := contactSnap.contacts[2]; got.FirstName != "Before" || got.CloseFriend {
				t.Fatalf("published contact snapshot mutated in place: %+v", got)
			}
			if got := photoSnap.refs[2]; got.PhotoID != 9001 {
				t.Fatalf("published photo snapshot mutated in place: %+v", got)
			}
			cached.mu.RLock()
			_, contactsWarm = cached.contacts[1]
			_, photosWarm = cached.personalPhotos[1]
			cached.mu.RUnlock()
			if contactsWarm || photosWarm {
				t.Fatalf("mutation left stale snapshots published: contacts=%v photos=%v", contactsWarm, photosWarm)
			}
		})
	}
}

func TestCachedContactStoreExternalInvalidationAndFlush(t *testing.T) {
	ctx := context.Background()
	base := memory.NewContactStore()
	if _, err := base.Upsert(ctx, 1, domain.ContactInput{ContactUserID: 2, FirstName: "Alice"}); err != nil {
		t.Fatalf("upsert contact: %v", err)
	}
	counting := &countingContactStore{ContactStore: base}
	cached := NewCachedContactStore(counting, 0)

	if _, err := cached.GetMany(ctx, 1, []int64{2}); err != nil {
		t.Fatalf("prime get: %v", err)
	}
	if _, err := base.Upsert(ctx, 1, domain.ContactInput{ContactUserID: 2, FirstName: "Alicia"}); err != nil {
		t.Fatalf("direct upsert: %v", err)
	}
	cached.InvalidateViewers(1)
	got, err := cached.GetMany(ctx, 1, []int64{2})
	if err != nil {
		t.Fatalf("get after external invalidation: %v", err)
	}
	if got[2].FirstName != "Alicia" {
		t.Fatalf("after invalidation = %+v, want Alicia", got[2])
	}

	if _, err := base.Upsert(ctx, 1, domain.ContactInput{ContactUserID: 2, FirstName: "Ally"}); err != nil {
		t.Fatalf("direct upsert 2: %v", err)
	}
	cached.FlushReadModelCache()
	got, err = cached.GetMany(ctx, 1, []int64{2})
	if err != nil {
		t.Fatalf("get after flush: %v", err)
	}
	if got[2].FirstName != "Ally" {
		t.Fatalf("after flush = %+v, want Ally", got[2])
	}
	if counting.listCalls != 3 {
		t.Fatalf("ListByUser calls = %d, want 3 after prime+invalidate+flush", counting.listCalls)
	}
}

func TestCachedContactStoreDoesNotRefillStaleSnapshotAfterInvalidation(t *testing.T) {
	ctx := context.Background()
	base := memory.NewContactStore()
	if _, err := base.Upsert(ctx, 1, domain.ContactInput{ContactUserID: 2, FirstName: "Alice"}); err != nil {
		t.Fatalf("seed contact: %v", err)
	}
	first, err := base.ListByUser(ctx, 1)
	if err != nil {
		t.Fatalf("snapshot first contact list: %v", err)
	}
	blocking := &blockingFirstListContactStore{
		ContactStore: base,
		started:      make(chan struct{}),
		release:      make(chan struct{}),
		first:        first,
	}
	cached := NewCachedContactStore(blocking, 0)

	type readResult struct {
		contacts map[int64]domain.Contact
		err      error
	}
	resultCh := make(chan readResult, 1)
	go func() {
		got, err := cached.GetMany(ctx, 1, []int64{2})
		resultCh <- readResult{contacts: got, err: err}
	}()
	waitForCacheTestSignal(t, blocking.started)

	if _, err := base.Upsert(ctx, 1, domain.ContactInput{ContactUserID: 2, FirstName: "Alicia"}); err != nil {
		t.Fatalf("update contact while first load is blocked: %v", err)
	}
	cached.InvalidateViewers(1)
	close(blocking.release)

	var result readResult
	select {
	case result = <-resultCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for contact read")
	}
	if result.err != nil {
		t.Fatalf("contact read: %v", result.err)
	}
	if result.contacts[2].FirstName != "Alicia" {
		t.Fatalf("contact after concurrent invalidation = %+v, want Alicia", result.contacts[2])
	}

	cachedHit, err := cached.GetMany(ctx, 1, []int64{2})
	if err != nil {
		t.Fatalf("cached hit after stale load retry: %v", err)
	}
	if cachedHit[2].FirstName != "Alicia" {
		t.Fatalf("cached value after stale load retry = %+v, want Alicia", cachedHit[2])
	}
	if calls := blocking.callCount(); calls != 2 {
		t.Fatalf("ListByUser calls = %d, want stale load plus exact-viewer retry", calls)
	}
}

func TestCachedContactStoreFlushRejectsEveryInFlightRefill(t *testing.T) {
	ctx := context.Background()
	base := memory.NewContactStore()
	if _, err := base.Upsert(ctx, 1, domain.ContactInput{ContactUserID: 2, FirstName: "Alice"}); err != nil {
		t.Fatalf("seed contact: %v", err)
	}
	first, err := base.ListByUser(ctx, 1)
	if err != nil {
		t.Fatalf("snapshot first contact list: %v", err)
	}
	blocking := &blockingFirstListContactStore{
		ContactStore: base,
		started:      make(chan struct{}),
		release:      make(chan struct{}),
		first:        first,
	}
	cached := NewCachedContactStore(blocking, time.Hour)

	type readResult struct {
		contacts map[int64]domain.Contact
		err      error
	}
	resultCh := make(chan readResult, 1)
	go func() {
		contacts, err := cached.GetMany(ctx, 1, []int64{2})
		resultCh <- readResult{contacts: contacts, err: err}
	}()
	waitForCacheTestSignal(t, blocking.started)
	if _, err := base.Upsert(ctx, 1, domain.ContactInput{ContactUserID: 2, FirstName: "Alicia"}); err != nil {
		t.Fatalf("update contact while first load is blocked: %v", err)
	}
	cached.FlushReadModelCache()
	close(blocking.release)

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("contact read: %v", result.err)
		}
		if got := result.contacts[2].FirstName; got != "Alicia" {
			t.Fatalf("flush allowed stale refill: got %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for contact read")
	}
	if calls := blocking.callCount(); calls != 2 {
		t.Fatalf("ListByUser calls = %d, want stale load plus post-flush retry", calls)
	}
}

func TestCachedContactStoreInvalidatesPersonalPhoto(t *testing.T) {
	ctx := context.Background()
	base := memory.NewContactStore()
	if _, err := base.Upsert(ctx, 1, domain.ContactInput{ContactUserID: 2, FirstName: "Alice"}); err != nil {
		t.Fatalf("upsert contact: %v", err)
	}
	if _, found, err := base.SetPersonalPhoto(ctx, 1, 2, 9001, 100); err != nil || !found {
		t.Fatalf("set personal photo: %v found=%v", err, found)
	}
	counting := &countingContactStore{ContactStore: base}
	cached := NewCachedContactStore(counting, 0)

	first, err := cached.PersonalPhotos(ctx, 1, []int64{2})
	if err != nil {
		t.Fatalf("personal photos first: %v", err)
	}
	if first[2].PhotoID != 9001 || !first[2].Personal {
		t.Fatalf("first personal photo = %+v, want 9001", first[2])
	}
	second, err := cached.PersonalPhotos(ctx, 1, []int64{2})
	if err != nil {
		t.Fatalf("personal photos second: %v", err)
	}
	if second[2].PhotoID != 9001 {
		t.Fatalf("second personal photo = %+v, want 9001", second[2])
	}
	if counting.listCalls != 1 {
		t.Fatalf("ListByUser calls = %d, want 1 personal-photo account snapshot load", counting.listCalls)
	}
	if counting.personalPhotoCalls != 1 {
		t.Fatalf("PersonalPhotos calls = %d, want 1", counting.personalPhotoCalls)
	}

	if _, found, err := cached.SetPersonalPhoto(ctx, 1, 2, 9002, 101); err != nil || !found {
		t.Fatalf("cached set personal photo: %v found=%v", err, found)
	}
	third, err := cached.PersonalPhotos(ctx, 1, []int64{2})
	if err != nil {
		t.Fatalf("personal photos third: %v", err)
	}
	if third[2].PhotoID != 9002 {
		t.Fatalf("third personal photo = %+v, want 9002 after invalidation", third[2])
	}
	if counting.personalPhotoCalls != 2 {
		t.Fatalf("PersonalPhotos calls after invalidation = %d, want 2", counting.personalPhotoCalls)
	}
	if counting.listCalls != 2 {
		t.Fatalf("ListByUser calls after mutation = %d, want 2 after safe invalidation and reload", counting.listCalls)
	}
	if counting.setPersonalPhotoHit != 1 {
		t.Fatalf("SetPersonalPhoto calls = %d, want 1", counting.setPersonalPhotoHit)
	}
}

func TestCachedContactStoreDoesNotRefillStalePersonalPhotoAfterInvalidation(t *testing.T) {
	ctx := context.Background()
	base := memory.NewContactStore()
	if _, err := base.Upsert(ctx, 1, domain.ContactInput{ContactUserID: 2, FirstName: "Alice"}); err != nil {
		t.Fatalf("upsert contact: %v", err)
	}
	if _, found, err := base.SetPersonalPhoto(ctx, 1, 2, 9001, 100); err != nil || !found {
		t.Fatalf("seed personal photo: %v found=%v", err, found)
	}
	first, err := base.PersonalPhotos(ctx, 1, []int64{2})
	if err != nil {
		t.Fatalf("snapshot first personal photo: %v", err)
	}
	blocking := &blockingFirstPersonalPhotoStore{
		ContactStore: base,
		started:      make(chan struct{}),
		release:      make(chan struct{}),
		first:        first,
	}
	cached := NewCachedContactStore(blocking, 0)

	type readResult struct {
		refs map[int64]domain.ProfilePhotoRef
		err  error
	}
	resultCh := make(chan readResult, 1)
	go func() {
		refs, err := cached.PersonalPhotos(ctx, 1, []int64{2})
		resultCh <- readResult{refs: refs, err: err}
	}()
	waitForCacheTestSignal(t, blocking.started)

	if _, found, err := cached.SetPersonalPhoto(ctx, 1, 2, 9002, 101); err != nil || !found {
		t.Fatalf("update personal photo while first load is blocked: %v found=%v", err, found)
	}
	close(blocking.release)

	var result readResult
	select {
	case result = <-resultCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for personal photo read")
	}
	if result.err != nil {
		t.Fatalf("personal photo read: %v", result.err)
	}
	if result.refs[2].PhotoID != 9002 {
		t.Fatalf("personal photo after concurrent invalidation = %+v, want 9002", result.refs[2])
	}
}

func TestCachedContactStoreOlderPersonalPhotoMutationCannotReinsertStalePair(t *testing.T) {
	ctx := context.Background()
	base := memory.NewContactStore()
	if _, err := base.Upsert(ctx, 1, domain.ContactInput{ContactUserID: 2, FirstName: "Alice"}); err != nil {
		t.Fatalf("seed contact: %v", err)
	}
	if _, found, err := base.SetPersonalPhoto(ctx, 1, 2, 9000, 99); err != nil || !found {
		t.Fatalf("seed personal photo: %v found=%v", err, found)
	}
	inner := &stalePersonalPhotoWritebackStore{
		ContactStore: base,
		started:      make(chan struct{}),
		release:      make(chan struct{}),
	}
	cached := NewCachedContactStore(inner, 0)
	if refs, err := cached.PersonalPhotos(ctx, 1, []int64{2}); err != nil || refs[2].PhotoID != 9000 {
		t.Fatalf("warm personal photo = %+v err=%v, want 9000", refs[2], err)
	}

	olderCtx := context.WithValue(ctx, stalePersonalPhotoWritebackContextKey{}, true)
	type setResult struct {
		found bool
		err   error
	}
	olderResult := make(chan setResult, 1)
	go func() {
		_, found, err := cached.SetPersonalPhoto(olderCtx, 1, 2, 9001, 100)
		olderResult <- setResult{found: found, err: err}
	}()
	waitForCacheTestSignal(t, inner.started)

	// The newer DB commit completes and invalidates the warm snapshot first.
	if _, found, err := cached.SetPersonalPhoto(ctx, 1, 2, 9002, 101); err != nil || !found {
		t.Fatalf("newer personal photo mutation: %v found=%v", err, found)
	}
	close(inner.release)
	select {
	case result := <-olderResult:
		if result.err != nil || !result.found {
			t.Fatalf("older personal photo mutation: %v found=%v", result.err, result.found)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for older personal photo mutation")
	}

	if calls := inner.staleReads(); calls != 0 {
		t.Fatalf("post-commit stale PersonalPhotos reads = %d, want 0", calls)
	}
	cached.mu.RLock()
	_, contactsWarm := cached.contacts[1]
	_, photosWarm := cached.personalPhotos[1]
	_, pairWarm := cached.projection[contactProjectionKey{viewerUserID: 1, contactUserID: 2}]
	cached.mu.RUnlock()
	if contactsWarm || photosWarm || pairWarm {
		t.Fatalf("older mutation reinserted stale cache state: contacts=%v photos=%v pair=%v", contactsWarm, photosWarm, pairWarm)
	}
	refs, err := cached.PersonalPhotos(ctx, 1, []int64{2})
	if err != nil {
		t.Fatalf("reload current personal photo: %v", err)
	}
	if got := refs[2].PhotoID; got != 9002 {
		t.Fatalf("personal photo after out-of-order completions = %d, want 9002", got)
	}
}
