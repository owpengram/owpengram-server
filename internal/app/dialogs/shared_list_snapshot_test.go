package dialogs

import (
	"context"
	"errors"
	"testing"

	"telesrv/internal/app/readmodel"
	"telesrv/internal/domain"
	"telesrv/internal/store"
	"telesrv/internal/store/memory"
)

type fakeSharedDialogListSnapshotCache struct {
	value    store.DialogListSnapshotCacheValue
	found    bool
	getErr   error
	putErr   error
	getCalls int
	putCalls int
	putKey   store.DialogListSnapshotCacheKey
	putValue store.DialogListSnapshotCacheValue
}

func (f *fakeSharedDialogListSnapshotCache) GetDialogListSnapshot(
	_ context.Context,
	_ store.DialogListSnapshotCacheKey,
) (store.DialogListSnapshotCacheValue, bool, error) {
	f.getCalls++
	return f.value, f.found, f.getErr
}

func (f *fakeSharedDialogListSnapshotCache) PutDialogListSnapshot(
	_ context.Context,
	key store.DialogListSnapshotCacheKey,
	value store.DialogListSnapshotCacheValue,
) error {
	f.putCalls++
	f.putKey = key
	f.putValue = value
	return f.putErr
}

func TestSharedDialogListSnapshotHitAvoidsAuthoritativeHeaderScan(t *testing.T) {
	const ownerID int64 = 1001
	peer := domain.Peer{Type: domain.PeerTypeUser, ID: 1002}
	versions := &fakeDialogReadModelVersions{hashes: map[store.ReadModelKey]int64{
		{Model: readmodel.ModelDialogOwner, OwnerUserID: ownerID, PeerType: domain.PeerTypeUser, PeerID: ownerID}: 11,
	}}
	shared := &fakeSharedDialogListSnapshotCache{
		found: true,
		value: store.DialogListSnapshotCacheValue{
			DependencyHash: readmodel.MixHashes(11),
			Dialogs:        []domain.Dialog{{Peer: peer, TopMessage: 7, TopMessageDate: 70}},
		},
	}
	authoritative := &snapshotDialogStore{DialogStore: memory.NewDialogStore()}
	service := NewService(authoritative).Configure(
		WithReadModelVersions(versions),
		WithSharedDialogListSnapshotCache(shared),
	)

	snap, err := service.loadDialogListSnapshot(context.Background(), dialogListSnapshotKey{userID: ownerID})
	if err != nil {
		t.Fatalf("load shared snapshot: %v", err)
	}
	if authoritative.snapshotCalls != 0 || shared.getCalls != 1 || shared.putCalls != 0 {
		t.Fatalf("calls header/get/put = %d/%d/%d, want 0/1/0",
			authoritative.snapshotCalls, shared.getCalls, shared.putCalls)
	}
	if snap == nil || len(snap.dialogs) != 1 || snap.dialogs[0].Peer != peer {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestPrivateDialogPeerIDsUsesVersionedSharedOwnerSnapshot(t *testing.T) {
	const ownerID int64 = 1001
	newer := domain.Peer{Type: domain.PeerTypeUser, ID: 1004}
	older := domain.Peer{Type: domain.PeerTypeUser, ID: 1003}
	versions := &fakeDialogReadModelVersions{hashes: map[store.ReadModelKey]int64{
		{Model: readmodel.ModelDialogOwner, OwnerUserID: ownerID, PeerType: domain.PeerTypeUser, PeerID: ownerID}: 41,
	}}
	shared := &fakeSharedDialogListSnapshotCache{found: true, value: store.DialogListSnapshotCacheValue{
		DependencyHash: readmodel.MixHashes(41),
		Dialogs: []domain.Dialog{
			{Peer: domain.Peer{Type: domain.PeerTypeUser, ID: ownerID}, TopMessageDate: 999},
			{Peer: domain.Peer{Type: domain.PeerTypeChannel, ID: 2001}, TopMessageDate: 998},
			{Peer: older, TopMessage: 9, TopMessageDate: 10},
			{Peer: domain.Peer{Type: domain.PeerTypeUser, ID: 1002}, TopMessage: 1, TopMessageDate: 20},
			{Peer: newer, TopMessage: 3, TopMessageDate: 20},
		},
	}}
	authoritative := &snapshotDialogStore{DialogStore: memory.NewDialogStore()}
	service := NewService(authoritative).Configure(
		WithReadModelVersions(versions),
		WithSharedDialogListSnapshotCache(shared),
	)

	ids, err := service.PrivateDialogPeerIDs(context.Background(), ownerID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != newer.ID || ids[1] != 1002 {
		t.Fatalf("private peer ids = %v, want [%d 1002]", ids, newer.ID)
	}
	if authoritative.privatePeerCalls != 0 || shared.getCalls != 1 {
		t.Fatalf("authoritative/shared calls = %d/%d, want 0/1", authoritative.privatePeerCalls, shared.getCalls)
	}
}

func TestPrivateDialogPeerIDsCacheMissUsesStableNarrowStoreRead(t *testing.T) {
	ctx := context.Background()
	const ownerID int64 = 1001
	peer := domain.Peer{Type: domain.PeerTypeUser, ID: 1002}
	base := memory.NewDialogStore()
	if err := base.SaveList(ctx, ownerID, domain.DialogList{Dialogs: []domain.Dialog{{
		Peer: peer, TopMessage: 7, TopMessageDate: 70,
	}}}); err != nil {
		t.Fatal(err)
	}
	versions := &fakeDialogReadModelVersions{hashes: map[store.ReadModelKey]int64{
		{Model: readmodel.ModelDialogOwner, OwnerUserID: ownerID, PeerType: domain.PeerTypeUser, PeerID: ownerID}: 42,
	}}
	shared := &fakeSharedDialogListSnapshotCache{}
	authoritative := &snapshotDialogStore{DialogStore: base}
	service := NewService(authoritative).Configure(
		WithReadModelVersions(versions),
		WithSharedDialogListSnapshotCache(shared),
	)

	ids, err := service.PrivateDialogPeerIDs(ctx, ownerID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != peer.ID || authoritative.privatePeerCalls != 1 || shared.getCalls != 1 {
		t.Fatalf("ids/calls = %v/%d/%d, want [%d]/1/1", ids, authoritative.privatePeerCalls, shared.getCalls, peer.ID)
	}
}

func TestSharedDialogListSnapshotHitServesMaterializedPageWithoutPeerHydration(t *testing.T) {
	const ownerID int64 = 1001
	peer := domain.Peer{Type: domain.PeerTypeUser, ID: 1002}
	versions := &fakeDialogReadModelVersions{hashes: map[store.ReadModelKey]int64{
		{Model: readmodel.ModelDialogOwner, OwnerUserID: ownerID, PeerType: domain.PeerTypeUser, PeerID: ownerID}: 12,
	}}
	shared := &fakeSharedDialogListSnapshotCache{
		found: true,
		value: store.DialogListSnapshotCacheValue{
			DependencyHash: readmodel.MixHashes(12),
			Dialogs: []domain.Dialog{{
				Peer: peer, TopMessage: 7, TopMessageDate: 70,
				Draft: &domain.DialogDraft{Peer: peer, Date: 71, Message: "materialized draft"},
			}},
			Messages: []domain.Message{{ID: 7, Peer: peer, From: peer, Date: 70, Body: "materialized"}},
			Users:    []domain.User{{ID: peer.ID, FirstName: "cached"}},
		},
	}
	authoritative := &snapshotDialogStore{DialogStore: memory.NewDialogStore()}
	service := NewService(authoritative).Configure(
		WithReadModelVersions(versions),
		WithSharedDialogListSnapshotCache(shared),
	)

	page, err := service.GetDialogs(context.Background(), ownerID, domain.DialogFilter{Limit: 100})
	if err != nil {
		t.Fatalf("get materialized shared page: %v", err)
	}
	if authoritative.snapshotCalls != 0 || authoritative.listByPeersCalls != 0 || authoritative.listDraftsCalls != 0 {
		t.Fatalf("authoritative snapshot/peer/draft calls = %d/%d/%d, want 0/0/0",
			authoritative.snapshotCalls, authoritative.listByPeersCalls, authoritative.listDraftsCalls)
	}
	if len(page.Dialogs) != 1 || len(page.Messages) != 1 || page.Messages[0].Body != "materialized" ||
		len(page.Users) != 1 || page.Users[0].ID != peer.ID || page.Dialogs[0].Draft == nil ||
		page.Dialogs[0].Draft.Message != "materialized draft" {
		t.Fatalf("materialized page = dialogs:%+v messages:%+v users:%+v", page.Dialogs, page.Messages, page.Users)
	}
}

func TestSharedDialogListSnapshotDependencyMismatchRebuildsAndPublishes(t *testing.T) {
	const ownerID int64 = 1001
	peer := domain.Peer{Type: domain.PeerTypeUser, ID: 1002}
	versions := &fakeDialogReadModelVersions{hashes: map[store.ReadModelKey]int64{
		{Model: readmodel.ModelDialogOwner, OwnerUserID: ownerID, PeerType: domain.PeerTypeUser, PeerID: ownerID}: 31,
	}}
	shared := &fakeSharedDialogListSnapshotCache{
		found: true,
		value: store.DialogListSnapshotCacheValue{
			DependencyHash: 999,
			Dialogs:        []domain.Dialog{{Peer: peer, TopMessage: 1}},
		},
	}
	authoritative := &snapshotDialogStore{DialogStore: memory.NewDialogStore(), list: domain.DialogList{
		Dialogs: []domain.Dialog{{Peer: peer, TopMessage: 8, TopMessageDate: 80}}, Count: 1,
	}}
	service := NewService(authoritative).Configure(
		WithReadModelVersions(versions),
		WithSharedDialogListSnapshotCache(shared),
	)

	snap, err := service.loadDialogListSnapshot(context.Background(), dialogListSnapshotKey{userID: ownerID})
	if err != nil {
		t.Fatalf("rebuild shared snapshot: %v", err)
	}
	if authoritative.snapshotCalls != 1 || shared.putCalls != 1 {
		t.Fatalf("calls header/put = %d/%d, want 1/1", authoritative.snapshotCalls, shared.putCalls)
	}
	if shared.putKey.OwnerHash != 31 || shared.putValue.DependencyHash != readmodel.MixHashes(31) {
		t.Fatalf("published key/value = %+v/%+v", shared.putKey, shared.putValue)
	}
	if snap == nil || len(snap.dialogs) != 1 || snap.dialogs[0].TopMessage != 8 {
		t.Fatalf("rebuilt snapshot = %+v", snap)
	}
}

func TestSharedDialogListSnapshotValidatesSharedChannelGeneration(t *testing.T) {
	const ownerID int64 = 1001
	peer := domain.Peer{Type: domain.PeerTypeChannel, ID: 2001}
	versions := &fakeDialogReadModelVersions{hashes: map[store.ReadModelKey]int64{
		{Model: readmodel.ModelDialogOwner, OwnerUserID: ownerID, PeerType: domain.PeerTypeUser, PeerID: ownerID}: 61,
		{Model: readmodel.ModelChannelBase, PeerType: peer.Type, PeerID: peer.ID}:                                 71,
	}}
	shared := &fakeSharedDialogListSnapshotCache{
		found: true,
		value: store.DialogListSnapshotCacheValue{
			DependencyHash: readmodel.MixHashes(61, 70),
			Dialogs:        []domain.Dialog{{Peer: peer, TopMessage: 1}},
		},
	}
	authoritative := &snapshotDialogStore{DialogStore: memory.NewDialogStore(), list: domain.DialogList{
		Dialogs: []domain.Dialog{{Peer: peer, TopMessage: 2}}, Count: 1,
	}}
	service := NewService(authoritative).Configure(
		WithReadModelVersions(versions),
		WithSharedDialogListSnapshotCache(shared),
	)

	_, err := service.loadDialogListSnapshot(context.Background(), dialogListSnapshotKey{userID: ownerID})
	if err != nil {
		t.Fatalf("rebuild after channel generation change: %v", err)
	}
	if authoritative.snapshotCalls != 1 || shared.putCalls != 1 ||
		shared.putValue.DependencyHash != readmodel.MixHashes(61, 71) {
		t.Fatalf("calls/header dependency = %d/%d/%d, want 1/1/%d",
			authoritative.snapshotCalls, shared.putCalls, shared.putValue.DependencyHash,
			readmodel.MixHashes(61, 71))
	}
}

func TestSharedDialogListSnapshotRedisErrorFailsClosed(t *testing.T) {
	const ownerID int64 = 1001
	versions := &fakeDialogReadModelVersions{hashes: map[store.ReadModelKey]int64{
		{Model: readmodel.ModelDialogOwner, OwnerUserID: ownerID, PeerType: domain.PeerTypeUser, PeerID: ownerID}: 51,
	}}
	shared := &fakeSharedDialogListSnapshotCache{getErr: errors.New("redis unavailable")}
	authoritative := &snapshotDialogStore{DialogStore: memory.NewDialogStore()}
	service := NewService(authoritative).Configure(
		WithReadModelVersions(versions),
		WithSharedDialogListSnapshotCache(shared),
	)

	_, err := service.loadDialogListSnapshot(context.Background(), dialogListSnapshotKey{userID: ownerID})
	if err == nil || authoritative.snapshotCalls != 0 || shared.putCalls != 0 {
		t.Fatalf("err=%v header_calls=%d put_calls=%d, want fail-closed before PostgreSQL scan",
			err, authoritative.snapshotCalls, shared.putCalls)
	}
}

func TestGetDialogsL1RejectsOldOwnerGenerationBeforeHydration(t *testing.T) {
	const ownerID int64 = 1001
	peer := domain.Peer{Type: domain.PeerTypeUser, ID: 1002}
	ownerKey := store.ReadModelKey{
		Model: readmodel.ModelDialogOwner, OwnerUserID: ownerID,
		PeerType: domain.PeerTypeUser, PeerID: ownerID,
	}
	versions := &fakeDialogReadModelVersions{hashes: map[store.ReadModelKey]int64{ownerKey: 11}}
	shared := &fakeSharedDialogListSnapshotCache{}
	authoritative := &snapshotDialogStore{DialogStore: memory.NewDialogStore(), list: domain.DialogList{
		Dialogs: []domain.Dialog{{Peer: peer, TopMessage: 7, TopMessageDate: 70}},
		Users:   []domain.User{{ID: peer.ID}},
		Count:   1,
	}}
	service := NewService(authoritative).Configure(
		WithReadModelVersions(versions),
		WithSharedDialogListSnapshotCache(shared),
	)
	filter := domain.DialogFilter{ExcludePinned: true, Limit: 100}
	first, err := service.GetDialogs(context.Background(), ownerID, filter)
	if err != nil || len(first.Dialogs) != 1 {
		t.Fatalf("first GetDialogs = dialogs:%d err:%v", len(first.Dialogs), err)
	}

	authoritative.list = domain.DialogList{}
	versions.hashes[ownerKey] = 12
	second, err := service.GetDialogs(context.Background(), ownerID, filter)
	if err != nil {
		t.Fatalf("GetDialogs after owner generation advance: %v", err)
	}
	if len(second.Dialogs) != 0 || authoritative.snapshotCalls != 2 {
		t.Fatalf("second dialogs/snapshot calls = %d/%d, want 0/2", len(second.Dialogs), authoritative.snapshotCalls)
	}
}

func TestGetDialogsRetriesWhenOwnerGenerationChangesDuringHydration(t *testing.T) {
	const ownerID int64 = 1001
	peer := domain.Peer{Type: domain.PeerTypeUser, ID: 1002}
	ownerKey := store.ReadModelKey{
		Model: readmodel.ModelDialogOwner, OwnerUserID: ownerID,
		PeerType: domain.PeerTypeUser, PeerID: ownerID,
	}
	versions := &fakeDialogReadModelVersions{hashes: map[store.ReadModelKey]int64{ownerKey: 21}}
	authoritative := &snapshotDialogStore{DialogStore: memory.NewDialogStore(), list: domain.DialogList{
		Dialogs: []domain.Dialog{{Peer: peer, TopMessage: 7, TopMessageDate: 70}},
		Users:   []domain.User{{ID: peer.ID}},
		Count:   1,
	}}
	authoritative.onListByPeers = func() {
		authoritative.list = domain.DialogList{}
		versions.hashes[ownerKey] = 22
	}
	service := NewService(authoritative).Configure(
		WithReadModelVersions(versions),
		WithSharedDialogListSnapshotCache(&fakeSharedDialogListSnapshotCache{}),
	)

	list, err := service.GetDialogs(context.Background(), ownerID, domain.DialogFilter{ExcludePinned: true, Limit: 100})
	if err != nil {
		t.Fatalf("GetDialogs across owner generation change: %v", err)
	}
	if len(list.Dialogs) != 0 || authoritative.snapshotCalls != 2 {
		t.Fatalf("dialogs/snapshot calls = %d/%d, want stable empty generation and 2 loads", len(list.Dialogs), authoritative.snapshotCalls)
	}
}

func TestGetDialogsRetriesWhenDraftChangesDuringOwnerSnapshot(t *testing.T) {
	ctx := context.Background()
	const ownerID int64 = 1001
	peer := domain.Peer{Type: domain.PeerTypeUser, ID: 1002}
	ownerKey := store.ReadModelKey{
		Model: readmodel.ModelDialogOwner, OwnerUserID: ownerID,
		PeerType: domain.PeerTypeUser, PeerID: ownerID,
	}
	versions := &fakeDialogReadModelVersions{hashes: map[store.ReadModelKey]int64{ownerKey: 31}}
	base := memory.NewDialogStore()
	if err := base.SaveDraft(ctx, ownerID, domain.DialogDraft{Peer: peer, Date: 1, Message: "old"}); err != nil {
		t.Fatal(err)
	}
	authoritative := &snapshotDialogStore{DialogStore: base, list: domain.DialogList{
		Dialogs: []domain.Dialog{{Peer: peer, TopMessage: 7, TopMessageDate: 70}},
		Users:   []domain.User{{ID: peer.ID}},
		Count:   1,
	}}
	authoritative.onListDrafts = func() {
		if err := base.SaveDraft(ctx, ownerID, domain.DialogDraft{Peer: peer, Date: 2, Message: "new"}); err != nil {
			t.Fatal(err)
		}
		versions.hashes[ownerKey] = 32
	}
	service := NewService(authoritative).Configure(
		WithReadModelVersions(versions),
		WithSharedDialogListSnapshotCache(&fakeSharedDialogListSnapshotCache{}),
	)

	list, err := service.GetDialogs(ctx, ownerID, domain.DialogFilter{ExcludePinned: true, Limit: 100})
	if err != nil {
		t.Fatalf("GetDialogs across draft generation change: %v", err)
	}
	if len(list.Dialogs) != 1 || list.Dialogs[0].Draft == nil || list.Dialogs[0].Draft.Message != "new" {
		t.Fatalf("stable draft snapshot = %+v", list.Dialogs)
	}
	if authoritative.snapshotCalls != 2 || authoritative.listDraftsCalls != 2 {
		t.Fatalf("snapshot/draft loads = %d/%d, want 2/2 after generation retry",
			authoritative.snapshotCalls, authoritative.listDraftsCalls)
	}
}

func TestOwnerBaseSnapshotDerivesBuiltInFolderVariantsOnce(t *testing.T) {
	const ownerID int64 = 1001
	mainPinned := domain.Peer{Type: domain.PeerTypeUser, ID: 2001}
	mainRegular := domain.Peer{Type: domain.PeerTypeUser, ID: 2002}
	archived := domain.Peer{Type: domain.PeerTypeUser, ID: 2003}
	ownerKey := store.ReadModelKey{
		Model: readmodel.ModelDialogOwner, OwnerUserID: ownerID,
		PeerType: domain.PeerTypeUser, PeerID: ownerID,
	}
	versions := &fakeDialogReadModelVersions{hashes: map[store.ReadModelKey]int64{ownerKey: 81}}
	authoritative := &snapshotDialogStore{DialogStore: memory.NewDialogStore(), list: domain.DialogList{
		Dialogs: []domain.Dialog{
			{Peer: mainPinned, FolderID: domain.DialogMainFolderID, TopMessage: 30, TopMessageDate: 300, Pinned: true, PinnedOrder: 1},
			{Peer: mainRegular, FolderID: domain.DialogMainFolderID, TopMessage: 20, TopMessageDate: 200},
			{Peer: archived, FolderID: domain.DialogArchiveFolderID, TopMessage: 10, TopMessageDate: 100},
		},
		Users: []domain.User{{ID: mainPinned.ID}, {ID: mainRegular.ID}, {ID: archived.ID}},
	}}
	shared := &fakeSharedDialogListSnapshotCache{}
	service := NewService(authoritative).Configure(
		WithReadModelVersions(versions),
		WithSharedDialogListSnapshotCache(shared),
	)

	main, err := service.GetDialogs(context.Background(), ownerID, domain.DialogFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	excludePinned, err := service.GetDialogs(context.Background(), ownerID, domain.DialogFilter{ExcludePinned: true, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := service.GetDialogs(context.Background(), ownerID, domain.DialogFilter{PinnedOnly: true, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	archive, err := service.GetDialogs(context.Background(), ownerID, domain.DialogFilter{
		HasFolderID: true, FolderID: domain.DialogArchiveFolderID, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	explicitMain, err := service.GetDialogs(context.Background(), ownerID, domain.DialogFilter{
		HasFolderID: true, FolderID: domain.DialogMainFolderID, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}

	if authoritative.snapshotCalls != 1 || authoritative.listByPeersCalls != 1 || shared.getCalls != 1 || shared.putCalls != 1 {
		t.Fatalf("base/peer/get/put calls = %d/%d/%d/%d, want 1/1/1/1",
			authoritative.snapshotCalls, authoritative.listByPeersCalls, shared.getCalls, shared.putCalls)
	}
	if len(main.Dialogs) != 2 || main.Dialogs[0].Peer != mainPinned || main.Dialogs[1].Peer != mainRegular || main.ArchiveSummary == nil || main.ArchiveSummary.TopPeer != archived {
		t.Fatalf("main variant = %+v", main)
	}
	if len(excludePinned.Dialogs) != 1 || excludePinned.Dialogs[0].Peer != mainRegular || excludePinned.ArchiveSummary != nil {
		t.Fatalf("exclude-pinned variant = %+v", excludePinned)
	}
	if len(pinned.Dialogs) != 1 || pinned.Dialogs[0].Peer != mainPinned || pinned.ArchiveSummary == nil {
		t.Fatalf("pinned variant = %+v", pinned)
	}
	if len(archive.Dialogs) != 1 || archive.Dialogs[0].Peer != archived || archive.ArchiveSummary != nil {
		t.Fatalf("archive variant = %+v", archive)
	}
	if explicitMain.Hash != main.Hash || main.Hash == 0 || excludePinned.Hash == main.Hash || pinned.Hash == main.Hash || archive.Hash == main.Hash {
		t.Fatalf("variant hashes main=%d explicit=%d exclude=%d pinned=%d archive=%d",
			main.Hash, explicitMain.Hash, excludePinned.Hash, pinned.Hash, archive.Hash)
	}
}
