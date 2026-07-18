package deploy

import (
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// TestMigrationsLoad asserts the embedded migrations directory parses
// cleanly with no database connection involved. Two independently-added
// migrations landing on the same version (e.g. an upstream sync and a local
// feature branch both picking "next free sequential number" at merge-base
// time) don't show up as a git conflict — they're different files — so iofs
// only catches it at load time, and previously that meant "at server
// startup," crashing telesrv with "postgres migrate: iofs source: failed to
// init driver ... duplicate migration file." This is why our own migrations
// use a YYYYMMDDHHMMSS timestamp version instead of continuing upstream's
// plain sequential numbering (see deploy/migrations/README.md): a 14-digit
// timestamp never collides with upstream's numbers and always sorts after
// them, no renumbering needed on the next sync.
func TestMigrationsLoad(t *testing.T) {
	if _, err := iofs.New(Migrations, "migrations"); err != nil {
		t.Fatalf("iofs.New(migrations): %v", err)
	}
}
