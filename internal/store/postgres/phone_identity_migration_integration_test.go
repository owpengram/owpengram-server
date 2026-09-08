package postgres

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"telesrv/internal/domain"
)

func TestPhoneIdentityMigrationCanonicalizesOnlyAfterCompleteAuditPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	t.Run("canonicalizes national trunk and preserves significant zero", func(t *testing.T) {
		tx := beginPhoneIdentityFixtureTx(t, ctx, pool)
		defer func() { _ = tx.Rollback(context.Background()) }()
		if _, err := tx.Exec(ctx, `
INSERT INTO users(id, phone) VALUES
    (1001, '9809981679461'),
    (1002, '390212345678'),
    ($1, $2)`, domain.OfficialSystemUserID, domain.OfficialSystemPhone); err != nil {
			t.Fatalf("insert phone fixtures: %v", err)
		}

		if err := canonicalizeStoredPhoneIdentitiesInTableTx(ctx, tx, pgx.Identifier{"users"}); err != nil {
			t.Fatalf("canonicalizeStoredPhoneIdentitiesInTableTx: %v", err)
		}
		assertFixturePhone(t, ctx, tx, 1001, "989981679461")
		assertFixturePhone(t, ctx, tx, 1002, "390212345678")
		assertFixturePhone(t, ctx, tx, domain.OfficialSystemUserID, domain.OfficialSystemPhone)
	})

	t.Run("duplicate canonical identity fails without changing rows", func(t *testing.T) {
		tx := beginPhoneIdentityFixtureTx(t, ctx, pool)
		defer func() { _ = tx.Rollback(context.Background()) }()
		if _, err := tx.Exec(ctx, `
INSERT INTO users(id, phone) VALUES
    (2001, '9809981679461'),
    (2002, '989981679461')`); err != nil {
			t.Fatalf("insert duplicate fixtures: %v", err)
		}

		err := canonicalizeStoredPhoneIdentitiesInTableTx(ctx, tx, pgx.Identifier{"users"})
		if err == nil || !strings.Contains(err.Error(), "users 2001 and 2002 resolve to the same canonical phone") {
			t.Fatalf("canonicalize err = %v, want duplicate user IDs", err)
		}
		assertFixturePhone(t, ctx, tx, 2001, "9809981679461")
		assertFixturePhone(t, ctx, tx, 2002, "989981679461")
	})

	t.Run("invalid ordinary identity remains unreachable without blocking valid rows", func(t *testing.T) {
		tx := beginPhoneIdentityFixtureTx(t, ctx, pool)
		defer func() { _ = tx.Rollback(context.Background()) }()
		const invalidPhone = "legacy-not-a-phone"
		if _, err := tx.Exec(ctx, `
INSERT INTO users(id, phone) VALUES
    (3001, $1),
    (3002, '9809981679461')`, invalidPhone); err != nil {
			t.Fatalf("insert invalid fixture: %v", err)
		}

		if err := canonicalizeStoredPhoneIdentitiesInTableTx(ctx, tx, pgx.Identifier{"users"}); err != nil {
			t.Fatalf("canonicalizeStoredPhoneIdentitiesInTableTx: %v", err)
		}
		assertFixturePhone(t, ctx, tx, 3001, invalidPhone)
		assertFixturePhone(t, ctx, tx, 3002, "989981679461")
	})
}

func beginPhoneIdentityFixtureTx(t *testing.T, ctx context.Context, pool interface {
	Begin(context.Context) (pgx.Tx, error)
}) pgx.Tx {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin phone identity fixture: %v", err)
	}
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE users (id bigint PRIMARY KEY, phone text NOT NULL) ON COMMIT DROP`); err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("create isolated users fixture: %v", err)
	}
	return tx
}

func assertFixturePhone(t *testing.T, ctx context.Context, tx pgx.Tx, userID int64, want string) {
	t.Helper()
	var got string
	if err := tx.QueryRow(ctx, `SELECT phone FROM users WHERE id=$1`, userID).Scan(&got); err != nil {
		t.Fatalf("read user %d phone: %v", userID, err)
	}
	if got != want {
		t.Fatalf("user %d phone = %q, want %q", userID, got, want)
	}
}
