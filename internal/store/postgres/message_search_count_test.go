package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"strings"
	"telesrv/internal/domain"
	"testing"
)

// A DB boundary that only accepts the count query. Embedded unexpected methods
// panic, so a page fetch, enrichment query or mutation cannot pass this test.
type countReadDB struct {
	pgx.Tx
	calls  int
	result int32
	err    error
}

func (d *countReadDB) QueryRow(_ context.Context, q string, args ...any) pgx.Row {
	if !strings.HasPrefix(q, "-- name: CountMessagesByUser :one") {
		panic("unexpected query")
	}
	d.calls++
	return countReadRow{d.result, d.err}
}

type countReadRow struct {
	value int32
	err   error
}

func (r countReadRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*int32) = r.value
	return nil
}
func TestMessageSearchCountOnlySkipsPageAndHydration(t *testing.T) {
	for _, count := range []int32{0, 7} {
		db := &countReadDB{result: count}
		s := NewMessageStore(db)
		got, err := s.ListByUser(context.Background(), 1, domain.MessageFilter{CountOnly: true, SenderUserID: 2, OffsetID: 8, AddOffset: -3, Limit: 500})
		if err != nil || got.Count != int(count) || len(got.Messages) != 0 || len(got.Users) != 0 || db.calls != 1 {
			t.Fatalf("result=%+v err=%v calls=%d", got, err, db.calls)
		}
	}
	failure := errors.New("database unavailable")
	db := &countReadDB{err: failure}
	got, err := NewMessageStore(db).ListByUser(context.Background(), 1, domain.MessageFilter{CountOnly: true})
	if !errors.Is(err, failure) || got.Count != 0 || db.calls != 1 {
		t.Fatalf("error swallowed: result=%+v err=%v", got, err)
	}
}
