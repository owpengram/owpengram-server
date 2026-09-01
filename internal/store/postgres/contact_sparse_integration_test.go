package postgres

import (
	"context"
	"reflect"
	"testing"
	"time"

	"telesrv/internal/domain"
)

func TestContactProjectionForViewerUserIDsPostgresDoesNotCrossPairs(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)
	users := NewUserStore(pool)
	viewerA := createTestUser(t, ctx, users, "+1910"+suffix+"01", "Viewer", "A")
	viewerB := createTestUser(t, ctx, users, "+1910"+suffix+"02", "Viewer", "B")
	ownerA := createTestUser(t, ctx, users, "+1910"+suffix+"03", "Owner", "A")
	ownerB := createTestUser(t, ctx, users, "+1910"+suffix+"04", "Owner", "B")
	userIDs := []int64{viewerA.ID, viewerB.ID, ownerA.ID, ownerB.ID}
	photoBase := time.Now().UnixNano() & 0x3fffffffffffffff
	photoIDs := []int64{photoBase + 1, photoBase + 2, photoBase + 3, photoBase + 4}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1::bigint[])", userIDs)
		_, _ = pool.Exec(ctx, "DELETE FROM photos WHERE id = ANY($1::bigint[])", photoIDs)
	})
	media := NewMediaStore(pool)
	for _, photoID := range photoIDs {
		if err := media.PutPhoto(ctx, domain.Photo{
			ID: photoID, AccessHash: photoID + 100, FileReference: []byte("sparse-ref"), Date: 1700000000, DCID: 2,
			Sizes: []domain.PhotoSize{{Kind: domain.PhotoSizeKindStripped, Type: "i", Bytes: []byte{1, 2, byte(photoID)}}},
		}); err != nil {
			t.Fatalf("PutPhoto(%d): %v", photoID, err)
		}
	}
	contacts := NewContactStore(pool)
	rows := []struct {
		viewer int64
		owner  int64
		name   string
		photo  int64
	}{
		{viewerA.ID, ownerA.ID, "A expected", photoIDs[0]},
		{viewerA.ID, ownerB.ID, "B cross", photoIDs[1]},
		{viewerB.ID, ownerA.ID, "A cross", photoIDs[2]},
		{viewerB.ID, ownerB.ID, "B expected", photoIDs[3]},
	}
	for _, row := range rows {
		if _, err := contacts.Upsert(ctx, row.viewer, domain.ContactInput{
			ContactUserID: row.owner,
			FirstName:     row.name,
			Phone:         "known-phone",
			Note:          "private note",
			NoteEntities: []domain.MessageEntity{{
				Type: domain.MessageEntityBold, Length: 7,
			}},
		}); err != nil {
			t.Fatalf("Upsert %d->%d: %v", row.viewer, row.owner, err)
		}
		if _, found, err := contacts.SetPersonalPhoto(ctx, row.viewer, row.owner, row.photo, 1700000001); err != nil || !found {
			t.Fatalf("SetPersonalPhoto %d->%d: found=%v err=%v", row.viewer, row.owner, found, err)
		}
	}
	got, err := contacts.ContactProjectionForViewerUserIDs(ctx, map[int64][]int64{
		viewerA.ID: {ownerA.ID},
		viewerB.ID: {ownerB.ID},
	})
	if err != nil {
		t.Fatalf("ContactProjectionForViewerUserIDs: %v", err)
	}
	if len(got.Contacts[viewerA.ID]) != 1 || got.Contacts[viewerA.ID][ownerA.ID].FirstName != "A expected" {
		t.Fatalf("viewer A contacts = %+v", got.Contacts[viewerA.ID])
	}
	contactA := got.Contacts[viewerA.ID][ownerA.ID]
	if !reflect.DeepEqual(contactA.User, domain.User{ID: ownerA.ID}) {
		t.Fatalf("viewer A sparse projection retained joined base user data: %+v", contactA.User)
	}
	if contactA.Phone != "known-phone" || contactA.Note != "private note" || len(contactA.NoteEntities) != 1 || contactA.NoteEntities[0].Length != 7 {
		t.Fatalf("viewer A sparse overlay = %+v", contactA)
	}
	if len(got.Contacts[viewerB.ID]) != 1 || got.Contacts[viewerB.ID][ownerB.ID].FirstName != "B expected" {
		t.Fatalf("viewer B contacts = %+v", got.Contacts[viewerB.ID])
	}
	if _, ok := got.Contacts[viewerA.ID][ownerB.ID]; ok {
		t.Fatal("viewer A received crossed owner B")
	}
	if _, ok := got.Contacts[viewerB.ID][ownerA.ID]; ok {
		t.Fatal("viewer B received crossed owner A")
	}
	if got.PersonalPhotos[viewerA.ID][ownerA.ID].PhotoID != photoIDs[0] || len(got.PersonalPhotos[viewerA.ID]) != 1 {
		t.Fatalf("viewer A personal photos = %+v", got.PersonalPhotos[viewerA.ID])
	}
	if got.PersonalPhotos[viewerB.ID][ownerB.ID].PhotoID != photoIDs[3] || len(got.PersonalPhotos[viewerB.ID]) != 1 {
		t.Fatalf("viewer B personal photos = %+v", got.PersonalPhotos[viewerB.ID])
	}
}

func TestGetReverseContactsForViewerUserIDsPostgresDoesNotCrossPairs(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)
	users := NewUserStore(pool)
	viewerA := createTestUser(t, ctx, users, "+1911"+suffix+"01", "Viewer", "A")
	viewerB := createTestUser(t, ctx, users, "+1911"+suffix+"02", "Viewer", "B")
	ownerA := createTestUser(t, ctx, users, "+1911"+suffix+"03", "Owner", "A")
	ownerB := createTestUser(t, ctx, users, "+1911"+suffix+"04", "Owner", "B")
	userIDs := []int64{viewerA.ID, viewerB.ID, ownerA.ID, ownerB.ID}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1::bigint[])", userIDs)
	})

	contacts := NewContactStore(pool)
	for _, row := range []struct {
		owner  int64
		viewer int64
		name   string
	}{
		{ownerA.ID, viewerA.ID, "A expected"},
		{ownerA.ID, viewerB.ID, "A cross"},
		{ownerB.ID, viewerA.ID, "B cross"},
		{ownerB.ID, viewerB.ID, "B expected"},
	} {
		if _, err := contacts.Upsert(ctx, row.owner, domain.ContactInput{
			ContactUserID: row.viewer,
			FirstName:     row.name,
			Note:          "relationship-only",
		}); err != nil {
			t.Fatalf("Upsert %d->%d: %v", row.owner, row.viewer, err)
		}
	}
	if _, err := contacts.SetCloseFriends(ctx, ownerA.ID, []int64{viewerA.ID}); err != nil {
		t.Fatal(err)
	}

	got, err := contacts.GetReverseContactsForViewerUserIDs(ctx, map[int64][]int64{
		ownerA.ID: {viewerA.ID},
		ownerB.ID: {viewerB.ID},
	})
	if err != nil {
		t.Fatalf("GetReverseContactsForViewerUserIDs: %v", err)
	}
	contactA, found := got[ownerA.ID][viewerA.ID]
	if !found || contactA.User.ID != viewerA.ID || contactA.FirstName != "A expected" || !contactA.CloseFriend {
		t.Fatalf("owner A exact relationship = %+v found=%v", contactA, found)
	}
	contactB, found := got[ownerB.ID][viewerB.ID]
	if !found || contactB.User.ID != viewerB.ID || contactB.FirstName != "B expected" || contactB.CloseFriend {
		t.Fatalf("owner B exact relationship = %+v found=%v", contactB, found)
	}
	if _, found := got[ownerA.ID][viewerB.ID]; found {
		t.Fatal("owner A received crossed viewer B")
	}
	if _, found := got[ownerB.ID][viewerA.ID]; found {
		t.Fatal("owner B received crossed viewer A")
	}
}
