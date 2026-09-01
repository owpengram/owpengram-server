package postgres

import (
	"context"
	"testing"

	"telesrv/internal/domain"
)

func TestChannelDialogSnapshotHashTracksDependenciesWithoutWideHeaders(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)

	owner, err := NewUserStore(pool).Create(ctx, domain.User{
		AccessHash: 51,
		Phone:      "+1778" + suffix + "01",
		FirstName:  "SnapshotHashOwner",
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	created, err := NewChannelStore(pool).CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID,
		Title:         "Snapshot Hash " + suffix,
		Megagroup:     true,
		Date:          1700000600,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channelID := created.Channel.ID
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id = $1", channelID)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", owner.ID)
	})

	channels := NewChannelStore(pool)
	load := func() domain.ChannelDialogList {
		t.Helper()
		list, err := channels.ListChannelDialogSnapshotHeaders(ctx, owner.ID, domain.DialogFilter{})
		if err != nil {
			t.Fatalf("list snapshot headers: %v", err)
		}
		if len(list.Dialogs) != 1 || list.Hash == 0 {
			t.Fatalf("snapshot = %+v, want one dialog and non-zero hash", list)
		}
		dialog := list.Dialogs[0]
		if dialog.Peer.ID != channelID || dialog.TopMessage == 0 || dialog.TopMessageDate == 0 {
			t.Fatalf("ordering header = %+v, want peer/top/date", dialog)
		}
		if dialog.ReadInboxMaxID != 0 || dialog.ReadOutboxMaxID != 0 || dialog.UnreadCount != 0 ||
			dialog.UnreadMentions != 0 || dialog.UnreadReactions != 0 || dialog.Pts != 0 {
			t.Fatalf("snapshot header retained mutable hydration fields: %+v", dialog)
		}
		return list
	}

	before := load()
	changed, err := channels.SetChannelDialogUnreadMark(ctx, owner.ID, channelID, true)
	if err != nil || !changed {
		t.Fatalf("set unread mark = %v, %v", changed, err)
	}
	afterOwnerState := load()
	if afterOwnerState.Hash == before.Hash {
		t.Fatalf("owner-local dependency hash stayed %d after unread mark", before.Hash)
	}
	if afterOwnerState.Dialogs[0].TopMessage != before.Dialogs[0].TopMessage ||
		afterOwnerState.Dialogs[0].TopMessageDate != before.Dialogs[0].TopMessageDate {
		t.Fatalf("unread mark changed ordering header: before=%+v after=%+v", before.Dialogs[0], afterOwnerState.Dialogs[0])
	}

	if _, err := pool.Exec(ctx, "UPDATE channels SET title = title || ' changed' WHERE id = $1", channelID); err != nil {
		t.Fatalf("update channel title: %v", err)
	}
	afterSharedState := load()
	if afterSharedState.Hash == afterOwnerState.Hash {
		t.Fatalf("shared channel dependency hash stayed %d after channel-base change", afterOwnerState.Hash)
	}
}

func TestPrivateDialogSnapshotHashTracksDraftDependency(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)
	users := NewUserStore(pool)
	owner, err := users.Create(ctx, domain.User{
		AccessHash: 52,
		Phone:      "+1778" + suffix + "02",
		FirstName:  "PrivateSnapshotOwner",
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	peer, err := users.Create(ctx, domain.User{
		AccessHash: 53,
		Phone:      "+1778" + suffix + "03",
		FirstName:  "PrivateSnapshotPeer",
	})
	if err != nil {
		t.Fatalf("create peer: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1::bigint[])", []int64{owner.ID, peer.ID})
	})

	dialogs := NewDialogStore(pool)
	dialogPeer := domain.Peer{Type: domain.PeerTypeUser, ID: peer.ID}
	if err := dialogs.Upsert(ctx, owner.ID, domain.Dialog{
		Peer:           dialogPeer,
		TopMessage:     7,
		TopMessageDate: 1700000610,
	}); err != nil {
		t.Fatalf("upsert dialog: %v", err)
	}
	before, err := dialogs.ListDialogSnapshotHeaders(ctx, owner.ID, domain.DialogFilter{})
	if err != nil {
		t.Fatalf("list snapshot before draft: %v", err)
	}
	if len(before.Dialogs) != 1 || before.Hash == 0 {
		t.Fatalf("snapshot before draft = %+v", before)
	}
	if err := dialogs.SaveDraft(ctx, owner.ID, domain.DialogDraft{
		Peer:    dialogPeer,
		Date:    1700000611,
		Message: "draft changes hash without changing ordering",
	}); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	after, err := dialogs.ListDialogSnapshotHeaders(ctx, owner.ID, domain.DialogFilter{})
	if err != nil {
		t.Fatalf("list snapshot after draft: %v", err)
	}
	if after.Hash == before.Hash {
		t.Fatalf("private snapshot hash stayed %d after draft change", before.Hash)
	}
	if after.Dialogs[0].TopMessage != before.Dialogs[0].TopMessage || after.Dialogs[0].TopMessageDate != before.Dialogs[0].TopMessageDate {
		t.Fatalf("draft changed ordering header: before=%+v after=%+v", before.Dialogs[0], after.Dialogs[0])
	}
}

func TestPrivateDialogAllBuiltinSnapshotIncludesMainAndArchive(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)
	users := NewUserStore(pool)
	owner, err := users.Create(ctx, domain.User{AccessHash: 61, Phone: "+1778" + suffix + "11", FirstName: "AllFolderOwner"})
	if err != nil {
		t.Fatal(err)
	}
	mainPeer, err := users.Create(ctx, domain.User{AccessHash: 62, Phone: "+1778" + suffix + "12", FirstName: "MainPeer"})
	if err != nil {
		t.Fatal(err)
	}
	archivePeer, err := users.Create(ctx, domain.User{AccessHash: 63, Phone: "+1778" + suffix + "13", FirstName: "ArchivePeer"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1::bigint[])", []int64{owner.ID, mainPeer.ID, archivePeer.ID})
	})
	dialogs := NewDialogStore(pool)
	for index, peer := range []int64{mainPeer.ID, archivePeer.ID} {
		if err := dialogs.Upsert(ctx, owner.ID, domain.Dialog{
			Peer:       domain.Peer{Type: domain.PeerTypeUser, ID: peer},
			TopMessage: 10 + index, TopMessageDate: 1700000700 + index,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := dialogs.EditPeerFolders(ctx, owner.ID, []domain.FolderPeerUpdate{{
		Peer: domain.Peer{Type: domain.PeerTypeUser, ID: archivePeer.ID}, FolderID: domain.DialogArchiveFolderID,
	}}); err != nil {
		t.Fatal(err)
	}
	all, err := dialogs.ListAllBuiltinDialogSnapshotHeaders(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Dialogs) != 2 {
		t.Fatalf("all built-in private dialogs = %+v", all.Dialogs)
	}
	folders := map[int64]int{}
	for _, dialog := range all.Dialogs {
		folders[dialog.Peer.ID] = dialog.FolderID
	}
	if folders[mainPeer.ID] != domain.DialogMainFolderID || folders[archivePeer.ID] != domain.DialogArchiveFolderID {
		t.Fatalf("all built-in private folders = %#v", folders)
	}
}
