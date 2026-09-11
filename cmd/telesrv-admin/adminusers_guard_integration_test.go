package main

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

// guardManagerRemoval is the only thing between an operator and a console
// nobody can administer, and it decides from a COUNT over admin_console_users
// whose predicate treats '*' as holding every right. Neither that wildcard
// matching nor the enabled filter can be proven anywhere but against the real
// table, so this is an integration test, gated on TELESRV_TEST_POSTGRES_DSN
// like the rest of the package.
//
// The count spans every enabled manager except the row being edited, so unlike
// the other integration tests here this one cannot keep to its own fixtures
// with a unique suffix -- a manager left behind by an earlier run would make
// the "nobody else" cases silently pass for the wrong reason. It empties
// admin_console_users instead; verificationReadStore refuses a DSN whose
// database name does not contain "test", which is what makes that safe, and no
// other test in the repo touches this table.
func TestGuardManagerRemovalIntegration(t *testing.T) {
	store, pool := verificationReadStore(t)
	srv := &server{read: store}
	ctx := context.Background()

	resetTable := func() {
		t.Helper()
		if _, err := pool.Exec(ctx, `TRUNCATE admin_console_users RESTART IDENTITY`); err != nil {
			t.Fatalf("truncate admin_console_users: %v", err)
		}
	}
	resetTable()
	t.Cleanup(resetTable)

	unique := time.Now().UnixNano() & 0x7fffffff
	insertOperator := func(permissions []string, enabled bool) int64 {
		t.Helper()
		unique++
		var id int64
		if err := pool.QueryRow(ctx, `
INSERT INTO admin_console_users (username, password_hash, permissions, enabled)
VALUES ($1, 'not-a-real-hash', $2, $3)
RETURNING id`, "guardop"+strconv.FormatInt(unique, 10), permissions, enabled).Scan(&id); err != nil {
			t.Fatalf("insert operator: %v", err)
		}
		return id
	}

	t.Run("sole wildcard holder cannot drop the wildcard", func(t *testing.T) {
		resetTable()
		id := insertOperator([]string{permissionAll}, true)
		err := srv.guardManagerRemoval(ctx, id, []string{permissionAccountsRead}, true)
		if !errors.Is(err, errLastManagerStanding) {
			t.Fatalf("err = %v, want errLastManagerStanding", err)
		}
	})

	t.Run("sole manager cannot disable itself", func(t *testing.T) {
		resetTable()
		id := insertOperator([]string{permissionAll}, true)
		err := srv.guardManagerRemoval(ctx, id, []string{permissionAll}, false)
		if !errors.Is(err, errLastManagerStanding) {
			t.Fatalf("err = %v, want errLastManagerStanding", err)
		}
	})

	// Narrowing the wildcard down to the managing right itself is the supported
	// way out of full access, so it must not trip the guard.
	t.Run("sole manager may trade the wildcard for admins.manage", func(t *testing.T) {
		resetTable()
		id := insertOperator([]string{permissionAll}, true)
		if err := srv.guardManagerRemoval(ctx, id, []string{permissionAdminsManage}, true); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})

	// The case the SQL's '*' arm exists for: the remaining manager holds the
	// wildcard rather than a literal admins.manage, and must still be counted.
	t.Run("another enabled wildcard holder counts as a manager", func(t *testing.T) {
		resetTable()
		id := insertOperator([]string{permissionAll}, true)
		insertOperator([]string{permissionAll}, true)
		if err := srv.guardManagerRemoval(ctx, id, []string{permissionAccountsRead}, true); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})

	t.Run("a disabled second manager does not count", func(t *testing.T) {
		resetTable()
		id := insertOperator([]string{permissionAll}, true)
		insertOperator([]string{permissionAdminsManage}, false)
		err := srv.guardManagerRemoval(ctx, id, []string{permissionAccountsRead}, true)
		if !errors.Is(err, errLastManagerStanding) {
			t.Fatalf("err = %v, want errLastManagerStanding", err)
		}
	})
}
