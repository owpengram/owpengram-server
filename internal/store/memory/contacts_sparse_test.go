package memory

import (
	"context"
	"reflect"
	"testing"

	"telesrv/internal/domain"
)

func TestContactProjectionForViewerUserIDsDoesNotCrossPairs(t *testing.T) {
	ctx := context.Background()
	contacts := NewContactStore()
	const (
		viewerA = int64(11)
		viewerB = int64(12)
		ownerA  = int64(21)
		ownerB  = int64(22)
	)
	for _, row := range []struct {
		viewer int64
		owner  int64
		name   string
		photo  int64
	}{
		{viewerA, ownerA, "A expected", 101},
		{viewerA, ownerB, "B cross", 102},
		{viewerB, ownerA, "A cross", 103},
		{viewerB, ownerB, "B expected", 104},
	} {
		if _, err := contacts.Upsert(ctx, row.viewer, domain.ContactInput{
			ContactUserID: row.owner,
			FirstName:     row.name,
			Phone:         "known-phone",
			Note:          "private note",
			NoteEntities: []domain.MessageEntity{{
				Type: domain.MessageEntityBold, Length: 7,
			}},
		}); err != nil {
			t.Fatal(err)
		}
		if _, found, err := contacts.SetPersonalPhoto(ctx, row.viewer, row.owner, row.photo, 1); err != nil || !found {
			t.Fatalf("SetPersonalPhoto %d->%d: found=%v err=%v", row.viewer, row.owner, found, err)
		}
	}
	got, err := contacts.ContactProjectionForViewerUserIDs(ctx, map[int64][]int64{
		viewerA: {ownerA},
		viewerB: {ownerB},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Contacts[viewerA]) != 1 || got.Contacts[viewerA][ownerA].FirstName != "A expected" {
		t.Fatalf("viewer A contacts = %+v", got.Contacts[viewerA])
	}
	contactA := got.Contacts[viewerA][ownerA]
	if !reflect.DeepEqual(contactA.User, domain.User{ID: ownerA}) {
		t.Fatalf("viewer A sparse projection retained base user data: %+v", contactA.User)
	}
	if contactA.Phone != "known-phone" || contactA.Note != "private note" || len(contactA.NoteEntities) != 1 || contactA.NoteEntities[0].Length != 7 {
		t.Fatalf("viewer A sparse overlay = %+v", contactA)
	}
	if len(got.Contacts[viewerB]) != 1 || got.Contacts[viewerB][ownerB].FirstName != "B expected" {
		t.Fatalf("viewer B contacts = %+v", got.Contacts[viewerB])
	}
	if _, ok := got.Contacts[viewerA][ownerB]; ok {
		t.Fatal("viewer A unexpectedly received viewer B's requested owner")
	}
	if _, ok := got.Contacts[viewerB][ownerA]; ok {
		t.Fatal("viewer B unexpectedly received viewer A's requested owner")
	}
	if len(got.PersonalPhotos[viewerA]) != 1 || got.PersonalPhotos[viewerA][ownerA].PhotoID != 101 {
		t.Fatalf("viewer A personal photos = %+v", got.PersonalPhotos[viewerA])
	}
	if len(got.PersonalPhotos[viewerB]) != 1 || got.PersonalPhotos[viewerB][ownerB].PhotoID != 104 {
		t.Fatalf("viewer B personal photos = %+v", got.PersonalPhotos[viewerB])
	}

	// Returned overlay slices are caller-owned, and the personal photo remains
	// in its dedicated projection map rather than leaking through Contact.User.
	contactA.NoteEntities[0].Length = 99
	gotAgain, err := contacts.ContactProjectionForViewerUserIDs(ctx, map[int64][]int64{viewerA: {ownerA}})
	if err != nil {
		t.Fatal(err)
	}
	if gotAgain.Contacts[viewerA][ownerA].NoteEntities[0].Length != 7 {
		t.Fatalf("sparse overlay shared NoteEntities with caller: %+v", gotAgain.Contacts[viewerA][ownerA])
	}
	if !reflect.DeepEqual(gotAgain.Contacts[viewerA][ownerA].User, domain.User{ID: ownerA}) {
		t.Fatalf("sparse projection reintroduced base user data: %+v", gotAgain.Contacts[viewerA][ownerA].User)
	}
}
