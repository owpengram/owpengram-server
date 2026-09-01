package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"telesrv/internal/domain"
)

const tombstoneAuthorizationTestUserSQL = `
UPDATE users SET
  phone = '', first_name = '', last_name = '', username = '', country_code = '', about = '',
  verified = false, support = false, last_seen_at = 0,
  premium_expires_at = NULL, emoji_status_document_id = 0, emoji_status_until = 0,
  emoji_status_collectible_id = NULL, emoji_status_collectible = '{}'::jsonb,
  color_set = false, color = 0, color_background_emoji_id = 0,
  profile_color_set = false, profile_color = 0, profile_color_background_emoji_id = 0,
  birthday_day = 0, birthday_month = 0, birthday_year = 0, personal_channel_id = 0,
  deleted_at = $2, deletion_source = 'manual', deletion_reason = '',
  account_delete_at = NULL, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL`

func TestAuthorizationStoreBindRejectsTombstonePostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	userID := createRevokeTestUser(t, ctx, pool, "bind-tombstone")
	key := saveTempIdentityTestAuthKey(t, ctx, pool, NewAuthKeyStore(pool), 0)

	if _, err := pool.Exec(ctx, tombstoneAuthorizationTestUserSQL, userID, time.Now().UTC()); err != nil {
		t.Fatalf("tombstone user: %v", err)
	}
	err := NewAuthorizationStore(pool).Bind(ctx, domain.Authorization{AuthKeyID: key, UserID: userID})
	if !errors.Is(err, domain.ErrAccountDeleted) {
		t.Fatalf("Bind tombstone err = %v, want ErrAccountDeleted", err)
	}
	assertRevokeTestNoAuthorization(t, ctx, NewAuthorizationStore(pool), key)
	assertRevokeTestTableCount(t, ctx, pool, "update_states", "auth_key_id", authKeyIDToInt64(key), 0)
}

func TestAuthorizationStoreBindWaitsForTombstoneThenRejectsPostgres(t *testing.T) {
	pool := testPool(t)
	testCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	userID := createRevokeTestUser(t, testCtx, pool, "bind-tombstone-race")
	key := saveTempIdentityTestAuthKey(t, testCtx, pool, NewAuthKeyStore(pool), 0)

	deleteTx, err := pool.Begin(testCtx)
	if err != nil {
		t.Fatalf("begin tombstone transaction: %v", err)
	}
	defer func() { _ = deleteTx.Rollback(context.Background()) }()
	if err := lockUsersForUpdate(testCtx, deleteTx, userID); err != nil {
		t.Fatalf("lock tombstone user: %v", err)
	}
	if _, err := deleteTx.Exec(testCtx, tombstoneAuthorizationTestUserSQL, userID, time.Now().UTC()); err != nil {
		t.Fatalf("stage tombstone: %v", err)
	}

	bindConn, err := pool.Acquire(testCtx)
	if err != nil {
		t.Fatalf("acquire bind connection: %v", err)
	}
	t.Cleanup(bindConn.Release)
	var bindPID int
	if err := bindConn.QueryRow(testCtx, "SELECT pg_backend_pid()").Scan(&bindPID); err != nil {
		t.Fatalf("get bind backend pid: %v", err)
	}
	bindResult := make(chan error, 1)
	go func() {
		bindResult <- NewAuthorizationStore(bindConn).Bind(testCtx, domain.Authorization{AuthKeyID: key, UserID: userID})
	}()
	waitForPostgresBackendLockWait(t, testCtx, pool, bindPID)

	if err := deleteTx.Commit(testCtx); err != nil {
		t.Fatalf("commit tombstone: %v", err)
	}
	select {
	case err := <-bindResult:
		if !errors.Is(err, domain.ErrAccountDeleted) {
			t.Fatalf("Bind after tombstone lock err = %v, want ErrAccountDeleted", err)
		}
	case <-testCtx.Done():
		t.Fatalf("Bind did not finish after tombstone commit: %v", testCtx.Err())
	}
	assertRevokeTestNoAuthorization(t, testCtx, NewAuthorizationStore(pool), key)
	assertRevokeTestTableCount(t, testCtx, pool, "update_states", "auth_key_id", authKeyIDToInt64(key), 0)
}
