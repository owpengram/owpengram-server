package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"telesrv/internal/domain"
	"telesrv/internal/store/postgres/sqlcgen"
)

// StarsStore implements store.StarsStore (the Stars local ledger) over
// PostgreSQL. Debit/credit/grant each complete within a single transaction:
// balance and transactions never drift apart.
type StarsStore struct {
	db sqlcgen.DBTX
}

// NewStarsStore builds a StarsStore over a pgx pool (or an open transaction).
func NewStarsStore(db sqlcgen.DBTX) *StarsStore {
	return &StarsStore{db: db}
}

func (s *StarsStore) GetBalance(ctx context.Context, userID int64) (domain.StarsBalance, error) {
	if userID == 0 {
		return domain.StarsBalance{}, nil
	}
	bal := domain.StarsBalance{UserID: userID}
	err := s.db.QueryRow(ctx, `SELECT balance, granted FROM stars_balances WHERE user_id = $1`, userID).
		Scan(&bal.Balance, &bal.Granted)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.StarsBalance{UserID: userID}, nil
		}
		return domain.StarsBalance{}, fmt.Errorf("get stars balance: %w", err)
	}
	return bal, nil
}

func (s *StarsStore) EnsureGrant(ctx context.Context, userID, amount int64, date int) (domain.StarsBalance, bool, error) {
	if userID == 0 {
		return domain.StarsBalance{}, false, nil
	}
	if amount <= 0 {
		// A zero grant amount only needs a row to exist with granted=true
		// (idempotently turning the grant off).
		bal, err := s.GetBalance(ctx, userID)
		return bal, false, err
	}
	out := domain.StarsBalance{UserID: userID}
	applied := false
	err := withTx(ctx, s.db, "ensure stars grant", func(tx pgx.Tx) error {
		var balance int64
		var granted bool
		err := tx.QueryRow(ctx, `SELECT balance, granted FROM stars_balances WHERE user_id = $1 FOR UPDATE`, userID).
			Scan(&balance, &granted)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if _, err := tx.Exec(ctx, `INSERT INTO stars_balances (user_id, balance, granted, updated_at) VALUES ($1, $2, true, now())`,
				userID, amount); err != nil {
				return fmt.Errorf("insert stars balance grant: %w", err)
			}
			out.Balance, out.Granted, applied = amount, true, true
		case err != nil:
			return fmt.Errorf("select stars balance for grant: %w", err)
		case granted:
			out.Balance, out.Granted = balance, true
			return nil
		default:
			if err := tx.QueryRow(ctx, `UPDATE stars_balances SET balance = balance + $2, granted = true, updated_at = now() WHERE user_id = $1 RETURNING balance`,
				userID, amount).Scan(&out.Balance); err != nil {
				return fmt.Errorf("update stars balance grant: %w", err)
			}
			out.Granted, applied = true, true
		}
		return insertStarsTxn(ctx, tx, userID, amount, domain.StarsReasonGrant, domain.Peer{}, date, "", "")
	})
	if err != nil {
		return domain.StarsBalance{}, false, err
	}
	return out, applied, nil
}

func (s *StarsStore) Credit(ctx context.Context, userID, amount int64, reason domain.StarsTransactionReason, peer domain.Peer, date int, title, desc string) (domain.StarsBalance, error) {
	if userID == 0 || amount <= 0 {
		return domain.StarsBalance{}, domain.ErrStarsInvalidAmount
	}
	out := domain.StarsBalance{UserID: userID}
	err := withTx(ctx, s.db, "credit stars", func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
INSERT INTO stars_balances (user_id, balance, updated_at) VALUES ($1, $2, now())
ON CONFLICT (user_id) DO UPDATE SET balance = stars_balances.balance + EXCLUDED.balance, updated_at = now()
RETURNING balance, granted`, userID, amount).Scan(&out.Balance, &out.Granted); err != nil {
			return fmt.Errorf("credit stars balance: %w", err)
		}
		return insertStarsTxn(ctx, tx, userID, amount, reason, peer, date, title, desc)
	})
	if err != nil {
		return domain.StarsBalance{}, err
	}
	return out, nil
}

func (s *StarsStore) Debit(ctx context.Context, userID, amount int64, reason domain.StarsTransactionReason, peer domain.Peer, date int, title, desc string) (domain.StarsBalance, error) {
	if userID == 0 || amount <= 0 {
		return domain.StarsBalance{}, domain.ErrStarsInvalidAmount
	}
	out := domain.StarsBalance{UserID: userID, Granted: true}
	err := withTx(ctx, s.db, "debit stars", func(tx pgx.Tx) error {
		var balance int64
		var granted bool
		err := tx.QueryRow(ctx, `SELECT balance, granted FROM stars_balances WHERE user_id = $1 FOR UPDATE`, userID).
			Scan(&balance, &granted)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && balance < amount) {
			return domain.ErrStarsInsufficient
		}
		if err != nil {
			return fmt.Errorf("select stars balance for debit: %w", err)
		}
		if err := tx.QueryRow(ctx, `UPDATE stars_balances SET balance = balance - $2, updated_at = now() WHERE user_id = $1 RETURNING balance`,
			userID, amount).Scan(&out.Balance); err != nil {
			return fmt.Errorf("update stars balance debit: %w", err)
		}
		out.Granted = granted
		return insertStarsTxn(ctx, tx, userID, -amount, reason, peer, date, title, desc)
	})
	if err != nil {
		return domain.StarsBalance{}, err
	}
	return out, nil
}

func (s *StarsStore) ClaimMonthly(ctx context.Context, userID, amount int64, date int, cooldown time.Duration) (domain.StarsBalance, bool, time.Time, error) {
	if userID == 0 || amount <= 0 {
		return domain.StarsBalance{}, false, time.Time{}, domain.ErrStarsInvalidAmount
	}
	now := time.Unix(int64(date), 0).UTC()
	out := domain.StarsBalance{UserID: userID}
	claimed := false
	nextAt := now.Add(cooldown)
	err := withTx(ctx, s.db, "claim monthly stars", func(tx pgx.Tx) error {
		var claimedAt time.Time
		err := tx.QueryRow(ctx, `SELECT claimed_at FROM stars_monthly_claims WHERE user_id = $1 FOR UPDATE`, userID).Scan(&claimedAt)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// Never claimed before -- eligible.
		case err != nil:
			return fmt.Errorf("select stars monthly claim: %w", err)
		default:
			nextAt = claimedAt.Add(cooldown)
			if now.Before(nextAt) {
				var balance int64
				var granted bool
				berr := tx.QueryRow(ctx, `SELECT balance, granted FROM stars_balances WHERE user_id = $1`, userID).Scan(&balance, &granted)
				if berr != nil && !errors.Is(berr, pgx.ErrNoRows) {
					return fmt.Errorf("select stars balance for monthly claim: %w", berr)
				}
				out.Balance, out.Granted = balance, granted
				return nil
			}
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO stars_monthly_claims (user_id, claimed_at) VALUES ($1, $2)
ON CONFLICT (user_id) DO UPDATE SET claimed_at = EXCLUDED.claimed_at`, userID, now); err != nil {
			return fmt.Errorf("upsert stars monthly claim: %w", err)
		}
		if err := tx.QueryRow(ctx, `
INSERT INTO stars_balances (user_id, balance, updated_at) VALUES ($1, $2, now())
ON CONFLICT (user_id) DO UPDATE SET balance = stars_balances.balance + EXCLUDED.balance, updated_at = now()
RETURNING balance, granted`, userID, amount).Scan(&out.Balance, &out.Granted); err != nil {
			return fmt.Errorf("credit stars balance for monthly claim: %w", err)
		}
		if err := insertStarsTxn(ctx, tx, userID, amount, domain.StarsReasonMonthlyClaim, domain.Peer{}, date, "Monthly Stars claim", ""); err != nil {
			return err
		}
		claimed = true
		nextAt = now.Add(cooldown)
		return nil
	})
	if err != nil {
		return domain.StarsBalance{}, false, time.Time{}, err
	}
	return out, claimed, nextAt, nil
}

// DeviceFingerprintGranted counts how many OTHER accounts sharing this
// exact device_model+system_version+platform+ip fingerprint already have an
// actually-credited Stars grant or claim, and reports whether that count
// meets threshold. Same four-column device/IP match as
// cmd/telesrv-admin's ListSharedDeviceGroups, just answered live instead of
// only surfaced for an operator to look at.
//
// threshold exists because "ip" alone is a much noisier signal in the wild
// than a single self-hosted deployment's own testing suggests: production
// data showed carrier-grade NAT putting a dozen-plus distinct real users
// behind one public IP, and even the server's OWN address showing up as
// the recorded "client" IP for a handful of real, distinct accounts (very
// likely a reverse-proxy/NAT quirk in some network paths). A threshold of 1
// (the original behavior) means any two unrelated real users who happen to
// share both a NAT'd IP and a common phone model/OS build get treated as
// the same farmer -- see the git history around this function's threshold
// parameter for the incident that surfaced this. Requiring several
// distinct prior accounts on the exact same fingerprint before blocking
// keeps catching real farms (which reuse a fingerprint dozens of times)
// while tolerating an isolated coincidence.
//
// This deliberately checks stars_transactions (reason IN grant/monthly_claim),
// never stars_balances.granted: SkipStartingGrant also sets granted=true on
// an account whose grant was WITHHELD by this very guard, without writing a
// transaction. Treating that flag as "received a grant" here would poison
// the fingerprint for good -- once one alt account got its grant withheld,
// every other account on that device+IP (including the original, legitimate
// one that earned the flag in the first place) would look like a repeat
// offender and get blocked forever, including from claiming again next
// month on their own account. Only a real credit counts as evidence.
func (s *StarsStore) DeviceFingerprintGranted(ctx context.Context, excludeUserID int64, deviceModel, systemVersion, platform, ip string, threshold int) (bool, error) {
	if threshold < 1 {
		threshold = 1
	}
	var count int
	err := s.db.QueryRow(ctx, `
SELECT COUNT(DISTINCT a.user_id) FROM authorizations a
WHERE a.user_id <> $1
  AND a.device_model = $2 AND a.system_version = $3 AND a.platform = $4 AND a.ip = $5
  AND EXISTS (
    SELECT 1 FROM stars_transactions st
    WHERE st.user_id = a.user_id AND st.reason IN ('grant', 'monthly_claim')
  )`, excludeUserID, deviceModel, systemVersion, platform, ip).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check device fingerprint grant: %w", err)
	}
	return count >= threshold, nil
}

// SkipStartingGrant see store.StarsStore's doc comment.
func (s *StarsStore) SkipStartingGrant(ctx context.Context, userID int64) error {
	if userID == 0 {
		return nil
	}
	_, err := s.db.Exec(ctx, `
INSERT INTO stars_balances (user_id, balance, granted, updated_at) VALUES ($1, 0, true, now())
ON CONFLICT (user_id) DO NOTHING`, userID)
	if err != nil {
		return fmt.Errorf("skip starting stars grant: %w", err)
	}
	return nil
}

func (s *StarsStore) ListTransactions(ctx context.Context, userID int64, query domain.StarsTransactionQuery) (domain.StarsTransactionPage, error) {
	if userID == 0 {
		return domain.StarsTransactionPage{}, nil
	}
	query, err := domain.NormalizeStarsTransactionQuery(query)
	if err != nil {
		return domain.StarsTransactionPage{}, err
	}
	bal, err := s.GetBalance(ctx, userID)
	if err != nil {
		return domain.StarsTransactionPage{}, err
	}
	page := domain.StarsTransactionPage{Balance: bal.Balance}

	// keyset: the direction filter applies before LIMIT; one extra row is
	// fetched to probe whether this view has a next page.
	where, order, args := starsTransactionQueryParts("user_id", "amount", userID, query)
	rows, err := s.db.Query(ctx, `
SELECT id, peer_type, peer_id, amount, reason, title, description, date,
COALESCE(premium_payment_intent_id, 0), COALESCE(premium_recipient_user_id, 0), COALESCE(premium_months, 0)
FROM stars_transactions
WHERE `+where+`
ORDER BY id `+order+`
LIMIT $2`, args...)
	if err != nil {
		return domain.StarsTransactionPage{}, fmt.Errorf("list stars transactions: %w", err)
	}
	defer rows.Close()
	txns := make([]domain.StarsTransaction, 0, query.Limit+1)
	for rows.Next() {
		var (
			t        domain.StarsTransaction
			peerType string
			peerID   int64
			reason   string
		)
		if err := rows.Scan(&t.ID, &peerType, &peerID, &t.Amount, &reason, &t.Title, &t.Description, &t.Date,
			&t.PaymentID, &t.RecipientUserID, &t.PremiumMonths); err != nil {
			return domain.StarsTransactionPage{}, fmt.Errorf("scan stars transaction: %w", err)
		}
		t.UserID = userID
		t.Reason = domain.StarsTransactionReason(reason)
		if peerType != "" {
			t.Peer = domain.Peer{Type: domain.PeerType(peerType), ID: peerID}
		}
		txns = append(txns, t)
	}
	if err := rows.Err(); err != nil {
		return domain.StarsTransactionPage{}, fmt.Errorf("iterate stars transactions: %w", err)
	}
	if len(txns) > query.Limit {
		txns = txns[:query.Limit]
		page.NextOffset = domain.EncodeStarsCursor(txns[len(txns)-1].ID)
	}
	page.Transactions = txns
	return page, nil
}

// starsTransactionQueryParts centralizes the sign predicate and keyset
// direction for the Stars ledger. Column names are package-owned constants
// only; client values remain bind parameters.
func starsTransactionQueryParts(ownerColumn, amountColumn string, ownerID int64, query domain.StarsTransactionQuery) (string, string, []any) {
	where := ownerColumn + "=$1"
	switch query.Direction {
	case domain.StarsTransactionDirectionIncoming:
		where += " AND " + amountColumn + ">0"
	case domain.StarsTransactionDirectionOutgoing:
		where += " AND " + amountColumn + "<0"
	}
	order, comparator := "DESC", "<"
	if query.Ascending {
		order, comparator = "ASC", ">"
	}
	args := []any{ownerID, query.Limit + 1}
	if cursor, ok := domain.DecodeStarsCursor(query.Offset); ok {
		where += " AND id" + comparator + "$3"
		args = append(args, cursor)
	}
	return where, order, args
}

// insertStarsTxn writes one signed-amount transaction within a transaction.
func insertStarsTxn(ctx context.Context, tx pgx.Tx, userID, amount int64, reason domain.StarsTransactionReason, peer domain.Peer, date int, title, desc string) error {
	if _, err := tx.Exec(ctx, `
INSERT INTO stars_transactions (user_id, peer_type, peer_id, amount, reason, title, description, date)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		userID, string(peer.Type), peer.ID, amount, string(reason), title, desc, date); err != nil {
		return fmt.Errorf("insert stars transaction: %w", err)
	}
	return nil
}
