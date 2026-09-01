package postgres

import (
	"reflect"
	"testing"

	"telesrv/internal/domain"
)

type sparseContactProjectionScanValues []any

func (values sparseContactProjectionScanValues) Scan(dest ...any) error {
	for i := range dest {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(values[i]))
	}
	return nil
}

func TestScanSparseContactProjectionRowsKeepsOnlyOverlay(t *testing.T) {
	encoded, err := encodeMessageEntities([]domain.MessageEntity{{
		Type: domain.MessageEntityTextURL, Length: 4, URL: "https://example.test",
	}})
	if err != nil {
		t.Fatal(err)
	}
	viewerID, contact, err := scanSparseContactProjectionRows(sparseContactProjectionScanValues{
		int64(11), int64(22), true, true, "known-phone", "Local", "Name", "private note", string(encoded),
	})
	if err != nil {
		t.Fatal(err)
	}
	if viewerID != 11 || !reflect.DeepEqual(contact.User, domain.User{ID: 22}) {
		t.Fatalf("sparse identity = viewer %d user %+v", viewerID, contact.User)
	}
	if contact.FirstName != "Local" || contact.LastName != "Name" || contact.Phone != "known-phone" ||
		contact.Note != "private note" || !contact.Mutual || !contact.CloseFriend ||
		len(contact.NoteEntities) != 1 || contact.NoteEntities[0].URL != "https://example.test" {
		t.Fatalf("sparse overlay = %+v", contact)
	}
}
