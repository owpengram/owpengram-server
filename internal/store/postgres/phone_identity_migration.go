package postgres

import (
	"context"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"

	"telesrv/internal/domain"
)

const phoneIdentityMigrationBatchSize = 1000

type phoneIdentityCandidate struct {
	userID         int64
	canonicalPhone string
	needsUpdate    bool
}

// canonicalizeStoredPhoneIdentities is the data half of migration 0182. SQL
// alone cannot correctly distinguish a removable national trunk prefix from a
// significant leading zero, so the application numbering-plan metadata builds
// a complete plan first. Legacy values that do not describe any valid phone
// identity are left untouched and therefore remain unreachable through the
// canonical login path. No users row is changed until no two parseable rows
// converge on the same E.164 identity.
func canonicalizeStoredPhoneIdentities(ctx context.Context, conn *pgx.Conn) error {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin phone identity audit: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()
	if err := canonicalizeStoredPhoneIdentitiesTx(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit phone identity migration: %w", err)
	}
	committed = true
	return nil
}

func canonicalizeStoredPhoneIdentitiesTx(ctx context.Context, tx pgx.Tx) error {
	return canonicalizeStoredPhoneIdentitiesInTableTx(ctx, tx, pgx.Identifier{"public", "users"})
}

func canonicalizeStoredPhoneIdentitiesInTableTx(ctx context.Context, tx pgx.Tx, usersTable pgx.Identifier) error {
	if _, err := tx.Exec(ctx, `
CREATE TEMP TABLE phone_identity_0182 (
    user_id bigint PRIMARY KEY,
    canonical_phone text NOT NULL,
    needs_update boolean NOT NULL
) ON COMMIT DROP`); err != nil {
		return fmt.Errorf("create phone identity plan: %w", err)
	}

	lastID := int64(math.MinInt64)
	for {
		rows, err := tx.Query(ctx, fmt.Sprintf(`
SELECT id, phone
FROM %s
WHERE id > $1 AND phone <> ''
ORDER BY id
LIMIT $2`, usersTable.Sanitize()), lastID, phoneIdentityMigrationBatchSize)
		if err != nil {
			return fmt.Errorf("scan stored phone identities: %w", err)
		}
		rowsRead := 0
		candidates := make([]phoneIdentityCandidate, 0, phoneIdentityMigrationBatchSize)
		for rows.Next() {
			rowsRead++
			var userID int64
			var rawPhone string
			if err := rows.Scan(&userID, &rawPhone); err != nil {
				rows.Close()
				return fmt.Errorf("read stored phone identity: %w", err)
			}
			lastID = userID
			canonical := rawPhone
			if !domain.IsSystemUserID(userID) {
				canonical = domain.NormalizePhone(rawPhone)
				if canonical == "" {
					continue
				}
			}
			candidates = append(candidates, phoneIdentityCandidate{
				userID:         userID,
				canonicalPhone: canonical,
				needsUpdate:    canonical != rawPhone,
			})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate stored phone identities: %w", err)
		}
		rows.Close()
		if len(candidates) > 0 {
			copyRows := make([][]any, 0, len(candidates))
			for _, candidate := range candidates {
				copyRows = append(copyRows, []any{candidate.userID, candidate.canonicalPhone, candidate.needsUpdate})
			}
			if _, err := tx.CopyFrom(ctx,
				pgx.Identifier{"phone_identity_0182"},
				[]string{"user_id", "canonical_phone", "needs_update"},
				pgx.CopyFromRows(copyRows),
			); err != nil {
				return fmt.Errorf("stage phone identity plan: %w", err)
			}
		}
		if rowsRead < phoneIdentityMigrationBatchSize {
			break
		}
	}

	if _, err := tx.Exec(ctx, `
CREATE INDEX phone_identity_0182_canonical_idx
ON phone_identity_0182(canonical_phone, user_id)`); err != nil {
		return fmt.Errorf("index phone identity plan: %w", err)
	}

	var firstUserID, secondUserID int64
	err := tx.QueryRow(ctx, `
SELECT first.user_id, second.user_id
FROM phone_identity_0182 AS first
JOIN phone_identity_0182 AS second
  ON second.canonical_phone = first.canonical_phone
 AND second.user_id > first.user_id
ORDER BY first.user_id, second.user_id
LIMIT 1`).Scan(&firstUserID, &secondUserID)
	switch {
	case err == nil:
		return fmt.Errorf("users %d and %d resolve to the same canonical phone", firstUserID, secondUserID)
	case err != pgx.ErrNoRows:
		return fmt.Errorf("audit canonical phone uniqueness: %w", err)
	}

	if _, err := tx.Exec(ctx, fmt.Sprintf(`
UPDATE %s AS target
SET phone = planned.canonical_phone
FROM phone_identity_0182 AS planned
WHERE planned.user_id = target.id
  AND planned.needs_update`, usersTable.Sanitize())); err != nil {
		return fmt.Errorf("apply canonical phone identities: %w", err)
	}
	return nil
}
