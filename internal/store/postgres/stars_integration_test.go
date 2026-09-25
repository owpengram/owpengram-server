package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"telesrv/internal/domain"
)

// TestStarsLedgerPostgres 回归迁移 0009：Stars 本地账本对真实 PG 的原子语义
// （首读授予幂等 / 贷记 / 借记原子 / 余额不足拦截且不动账 / keyset 分页末页无游标）。
func TestStarsLedgerPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	st := NewStarsStore(pool)

	users := NewUserStore(pool)
	suffix := randomSuffix(t)
	u, err := users.Create(ctx, domain.User{AccessHash: 92, Phone: "+1665" + suffix + "01", FirstName: "StarsLedger"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM stars_transactions WHERE user_id = $1", u.ID)
		_, _ = pool.Exec(ctx, "DELETE FROM stars_balances WHERE user_id = $1", u.ID)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", u.ID)
	})

	// 空账号：余额 0、未授予。
	if bal, err := st.GetBalance(ctx, u.ID); err != nil || bal.Balance != 0 || bal.Granted {
		t.Fatalf("empty balance = %+v err %v, want 0 not granted", bal, err)
	}

	// 首读授予一次。
	bal, applied, err := st.EnsureGrant(ctx, u.ID, 1000, 1700000000)
	if err != nil || !applied || bal.Balance != 1000 || !bal.Granted {
		t.Fatalf("first grant = %+v applied %v err %v, want 1000 granted applied", bal, applied, err)
	}
	// 再次授予幂等：不重复。
	bal, applied, err = st.EnsureGrant(ctx, u.ID, 1000, 1700000001)
	if err != nil || applied || bal.Balance != 1000 {
		t.Fatalf("second grant = %+v applied %v err %v, want 1000 not applied", bal, applied, err)
	}

	// 借记原子扣减 + 写负流水。
	peer := domain.Peer{Type: domain.PeerTypeChannel, ID: 4242}
	bal, err = st.Debit(ctx, u.ID, 300, domain.StarsReasonReaction, peer, 1700000002, "paid reaction", "")
	if err != nil || bal.Balance != 700 {
		t.Fatalf("debit = %+v err %v, want 700", bal, err)
	}

	// 余额不足：拦截且不动账（CHECK + FOR UPDATE 双保险）。
	if _, err := st.Debit(ctx, u.ID, 100000, domain.StarsReasonReaction, peer, 1700000003, "", ""); !errors.Is(err, domain.ErrStarsInsufficient) {
		t.Fatalf("over-debit err = %v, want ErrStarsInsufficient", err)
	}
	if bal, err := st.GetBalance(ctx, u.ID); err != nil || bal.Balance != 700 {
		t.Fatalf("balance after failed debit = %+v err %v, want 700 unchanged", bal, err)
	}

	// 贷记。
	bal, err = st.Credit(ctx, u.ID, 50, domain.StarsReasonGift, domain.Peer{Type: domain.PeerTypeUser, ID: 9}, 1700000004, "gift", "")
	if err != nil || bal.Balance != 750 {
		t.Fatalf("credit = %+v err %v, want 750", bal, err)
	}

	// 流水：grant(+1000) / debit(-300) / credit(+50) 共 3 条，倒序最新在前。
	page, err := st.ListTransactions(ctx, u.ID, domain.StarsTransactionQuery{Limit: 2})
	if err != nil {
		t.Fatalf("list page1: %v", err)
	}
	if len(page.Transactions) != 2 || page.NextOffset == "" {
		t.Fatalf("page1 = %d txns next=%q, want 2 + next", len(page.Transactions), page.NextOffset)
	}
	if page.Transactions[0].Amount != 50 || page.Transactions[0].Reason != domain.StarsReasonGift {
		t.Fatalf("page1[0] = %+v, want +50 gift (newest)", page.Transactions[0])
	}
	if page.Balance != 750 {
		t.Fatalf("page balance = %d, want 750", page.Balance)
	}
	page2, err := st.ListTransactions(ctx, u.ID, domain.StarsTransactionQuery{Offset: page.NextOffset, Limit: 2})
	if err != nil {
		t.Fatalf("list page2: %v", err)
	}
	if len(page2.Transactions) != 1 || page2.NextOffset != "" {
		t.Fatalf("page2 = %d txns next=%q, want 1 + empty (terminal)", len(page2.Transactions), page2.NextOffset)
	}
	if page2.Transactions[0].Reason != domain.StarsReasonGrant || page2.Transactions[0].Amount != 1000 {
		t.Fatalf("page2[0] = %+v, want +1000 grant (oldest)", page2.Transactions[0])
	}

	incoming1, err := st.ListTransactions(ctx, u.ID, domain.StarsTransactionQuery{
		Limit: 1, Direction: domain.StarsTransactionDirectionIncoming,
	})
	if err != nil {
		t.Fatalf("incoming page1: %v", err)
	}
	if len(incoming1.Transactions) != 1 || incoming1.Transactions[0].Amount != 50 || incoming1.NextOffset == "" {
		t.Fatalf("incoming page1 = %+v next=%q, want +50 and next", incoming1.Transactions, incoming1.NextOffset)
	}
	incoming2, err := st.ListTransactions(ctx, u.ID, domain.StarsTransactionQuery{
		Offset: incoming1.NextOffset, Limit: 1, Direction: domain.StarsTransactionDirectionIncoming,
	})
	if err != nil {
		t.Fatalf("incoming page2: %v", err)
	}
	if len(incoming2.Transactions) != 1 || incoming2.Transactions[0].Amount != 1000 || incoming2.NextOffset != "" {
		t.Fatalf("incoming page2 = %+v next=%q, want +1000 terminal", incoming2.Transactions, incoming2.NextOffset)
	}

	outgoing, err := st.ListTransactions(ctx, u.ID, domain.StarsTransactionQuery{
		Limit: 10, Direction: domain.StarsTransactionDirectionOutgoing,
	})
	if err != nil || len(outgoing.Transactions) != 1 || outgoing.Transactions[0].Amount != -300 {
		t.Fatalf("outgoing = %+v err=%v, want only -300", outgoing.Transactions, err)
	}

	ascending, err := st.ListTransactions(ctx, u.ID, domain.StarsTransactionQuery{Limit: 10, Ascending: true})
	if err != nil || len(ascending.Transactions) != 3 {
		t.Fatalf("ascending = %+v err=%v", ascending.Transactions, err)
	}
	wantAscending := []int64{1000, -300, 50}
	for i, amount := range wantAscending {
		if ascending.Transactions[i].Amount != amount {
			t.Fatalf("ascending[%d].amount = %d, want %d", i, ascending.Transactions[i].Amount, amount)
		}
	}
}

// TestStarsMonthlyClaimPostgres 回归迁移 20260922120000：@premiumbot /claim
// 的一次性冷却窗口对真实 PG 的原子语义——首claim 记账+写流水，冷却期内第二次
// claim 既不重复记账也报告下次可领时刻，冷却期外第三次 claim 恢复可领。
// TestStarsDeviceFingerprintGuardPostgres proves the anti-farming device+IP
// join against real Postgres: two accounts sharing the exact
// device_model+system_version+platform+ip (via a real authorizations row
// each, not a mock) are detected as the same farmer only once one of them
// has actually been granted, only in the direction that excludes the
// account being checked, and only for that exact fingerprint -- a
// different IP on an otherwise-identical device never matches. This is the
// SQL app/stars.Service.GuardStartingGrant/GuardClaim rely on.
func TestStarsDeviceFingerprintGuardPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	st := NewStarsStore(pool)
	keys := NewAuthKeyStore(pool)
	auths := NewAuthorizationStore(pool)

	userA := createRevokeTestUser(t, ctx, pool, "fp-a")
	userB := createRevokeTestUser(t, ctx, pool, "fp-b")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM stars_transactions WHERE user_id IN ($1,$2)", userA, userB)
		_, _ = pool.Exec(ctx, "DELETE FROM stars_balances WHERE user_id IN ($1,$2)", userA, userB)
	})

	const deviceModel, systemVersion, platform, ip = "Pixel 8", "Android 15", "android", "203.0.113.9"
	keyA := saveTempIdentityTestAuthKey(t, ctx, pool, keys, 0)
	if err := auths.Bind(ctx, domain.Authorization{
		AuthKeyID: keyA, UserID: userA, DeviceModel: deviceModel, SystemVersion: systemVersion, Platform: platform, IP: ip,
	}); err != nil {
		t.Fatalf("bind userA authorization: %v", err)
	}
	keyB := saveTempIdentityTestAuthKey(t, ctx, pool, keys, 0)
	if err := auths.Bind(ctx, domain.Authorization{
		AuthKeyID: keyB, UserID: userB, DeviceModel: deviceModel, SystemVersion: systemVersion, Platform: platform, IP: ip,
	}); err != nil {
		t.Fatalf("bind userB authorization: %v", err)
	}

	// Neither account has been granted yet: no match in either direction.
	if dup, err := st.DeviceFingerprintGranted(ctx, userB, deviceModel, systemVersion, platform, ip, 1); err != nil || dup {
		t.Fatalf("pre-grant DeviceFingerprintGranted = %v, %v, want false, nil", dup, err)
	}

	if _, _, err := st.EnsureGrant(ctx, userA, 1000, 1700000000); err != nil {
		t.Fatalf("grant userA: %v", err)
	}

	// Now userB's identical fingerprint matches userA's grant.
	if dup, err := st.DeviceFingerprintGranted(ctx, userB, deviceModel, systemVersion, platform, ip, 1); err != nil || !dup {
		t.Fatalf("post-grant DeviceFingerprintGranted(excl userB) = %v, %v, want true, nil", dup, err)
	}
	// The check excludes the caller's own account: userA never matches itself.
	if dup, err := st.DeviceFingerprintGranted(ctx, userA, deviceModel, systemVersion, platform, ip, 1); err != nil || dup {
		t.Fatalf("DeviceFingerprintGranted(excl userA) = %v, %v, want false, nil (must not match itself)", dup, err)
	}
	// A different IP on the same device never matches.
	if dup, err := st.DeviceFingerprintGranted(ctx, userB, deviceModel, systemVersion, platform, "203.0.113.99", 1); err != nil || dup {
		t.Fatalf("DeviceFingerprintGranted with a different IP = %v, %v, want false, nil", dup, err)
	}

	if err := st.SkipStartingGrant(ctx, userB); err != nil {
		t.Fatalf("skip starting grant: %v", err)
	}
	bal, err := st.GetBalance(ctx, userB)
	if err != nil || bal.Balance != 0 || !bal.Granted {
		t.Fatalf("userB balance after skip = %+v err %v, want 0 granted (never actually credited)", bal, err)
	}

	// Regression: a withheld grant (SkipStartingGrant) must never itself
	// count as evidence against a THIRD account sharing the fingerprint --
	// otherwise userA's own account, the legitimate one that earned the
	// flag in the first place, would find userB's granted=true skip-marker
	// and get blocked from ever claiming again. userB is still correctly
	// excluded from claiming itself (that's the real, working guard); this
	// only checks that userB's presence doesn't poison userA.
	if dup, err := st.DeviceFingerprintGranted(ctx, userA, deviceModel, systemVersion, platform, ip, 1); err != nil || dup {
		t.Fatalf("DeviceFingerprintGranted(excl userA) after userB's withheld grant = %v, %v, want false, nil (userB's skip must not count as evidence)", dup, err)
	}
	// userB itself is still correctly recognized as sharing userA's
	// fingerprint (unaffected by the fix above, which only changes what
	// counts as evidence -- userA's real grant still does).
	if dup, err := st.DeviceFingerprintGranted(ctx, userB, deviceModel, systemVersion, platform, ip, 1); err != nil || !dup {
		t.Fatalf("DeviceFingerprintGranted(excl userB) after userB's withheld grant = %v, %v, want true, nil (userA's real grant still matches)", dup, err)
	}
}

// TestStarsDeviceFingerprintThresholdPostgres proves threshold>1 tolerates
// an isolated coincidence -- exactly the production scenario that motivated
// raising the default: two unrelated real accounts sharing a fingerprint
// (carrier-grade NAT, or a reverse-proxy quirk making the server's own IP
// look like the client's) must NOT block each other, but once enough
// distinct accounts pile up on the same fingerprint, it's evidently a farm
// again.
func TestStarsDeviceFingerprintThresholdPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	st := NewStarsStore(pool)
	keys := NewAuthKeyStore(pool)
	auths := NewAuthorizationStore(pool)

	userA := createRevokeTestUser(t, ctx, pool, "fpt-a")
	userB := createRevokeTestUser(t, ctx, pool, "fpt-b")
	userC := createRevokeTestUser(t, ctx, pool, "fpt-c")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM stars_transactions WHERE user_id IN ($1,$2,$3)", userA, userB, userC)
		_, _ = pool.Exec(ctx, "DELETE FROM stars_balances WHERE user_id IN ($1,$2,$3)", userA, userB, userC)
	})

	const deviceModel, systemVersion, platform, ip = "Galaxy S23", "Android 14", "android", "198.51.100.42"
	for _, u := range []int64{userA, userB, userC} {
		key := saveTempIdentityTestAuthKey(t, ctx, pool, keys, 0)
		if err := auths.Bind(ctx, domain.Authorization{
			AuthKeyID: key, UserID: u, DeviceModel: deviceModel, SystemVersion: systemVersion, Platform: platform, IP: ip,
		}); err != nil {
			t.Fatalf("bind authorization for %d: %v", u, err)
		}
	}
	if _, _, err := st.EnsureGrant(ctx, userA, 1000, 1700000000); err != nil {
		t.Fatalf("grant userA: %v", err)
	}

	// Only userA (1 other account) has been granted so far: threshold 2
	// tolerates it as a coincidence, threshold 1 still catches it.
	if dup, err := st.DeviceFingerprintGranted(ctx, userB, deviceModel, systemVersion, platform, ip, 1); err != nil || !dup {
		t.Fatalf("threshold=1 with 1 prior grantee = %v, %v, want true, nil", dup, err)
	}
	if dup, err := st.DeviceFingerprintGranted(ctx, userB, deviceModel, systemVersion, platform, ip, 2); err != nil || dup {
		t.Fatalf("threshold=2 with only 1 prior grantee = %v, %v, want false, nil (isolated coincidence tolerated)", dup, err)
	}

	if _, _, err := st.EnsureGrant(ctx, userB, 1000, 1700000001); err != nil {
		t.Fatalf("grant userB: %v", err)
	}

	// Now 2 other accounts (A and B) have been granted on this fingerprint:
	// threshold 2 catches userC, but threshold 3 still tolerates it.
	if dup, err := st.DeviceFingerprintGranted(ctx, userC, deviceModel, systemVersion, platform, ip, 2); err != nil || !dup {
		t.Fatalf("threshold=2 with 2 prior grantees = %v, %v, want true, nil", dup, err)
	}
	if dup, err := st.DeviceFingerprintGranted(ctx, userC, deviceModel, systemVersion, platform, ip, 3); err != nil || dup {
		t.Fatalf("threshold=3 with only 2 prior grantees = %v, %v, want false, nil", dup, err)
	}
}

func TestStarsMonthlyClaimPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	st := NewStarsStore(pool)

	users := NewUserStore(pool)
	suffix := randomSuffix(t)
	u, err := users.Create(ctx, domain.User{AccessHash: 93, Phone: "+1665" + suffix + "02", FirstName: "StarsMonthlyClaim"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM stars_transactions WHERE user_id = $1", u.ID)
		_, _ = pool.Exec(ctx, "DELETE FROM stars_monthly_claims WHERE user_id = $1", u.ID)
		_, _ = pool.Exec(ctx, "DELETE FROM stars_balances WHERE user_id = $1", u.ID)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", u.ID)
	})

	cooldown := 30 * 24 * time.Hour

	// 首次 claim：记账 + 写流水，下次可领时刻 = 本次时刻 + cooldown。
	bal, claimed, nextAt, err := st.ClaimMonthly(ctx, u.ID, 100, 1700000000, cooldown)
	if err != nil || !claimed || bal.Balance != 100 {
		t.Fatalf("first claim = %+v claimed=%v err=%v, want 100 claimed", bal, claimed, err)
	}
	wantNextAt := time.Unix(1700000000, 0).UTC().Add(cooldown)
	if !nextAt.Equal(wantNextAt) {
		t.Fatalf("first claim nextAt = %v, want %v", nextAt, wantNextAt)
	}

	// 冷却期内第二次 claim（1 秒后）：不记账，报告同一个下次可领时刻。
	bal, claimed, nextAt, err = st.ClaimMonthly(ctx, u.ID, 100, 1700000001, cooldown)
	if err != nil || claimed || bal.Balance != 100 {
		t.Fatalf("second claim (on cooldown) = %+v claimed=%v err=%v, want 100 not claimed", bal, claimed, err)
	}
	if !nextAt.Equal(wantNextAt) {
		t.Fatalf("second claim nextAt = %v, want %v (unchanged)", nextAt, wantNextAt)
	}

	// 冷却期外第三次 claim：恢复可领，余额累加。
	afterCooldown := 1700000000 + int(cooldown.Seconds()) + 1
	bal, claimed, nextAt, err = st.ClaimMonthly(ctx, u.ID, 100, afterCooldown, cooldown)
	if err != nil || !claimed || bal.Balance != 200 {
		t.Fatalf("third claim (after cooldown) = %+v claimed=%v err=%v, want 200 claimed", bal, claimed, err)
	}
	wantThirdNextAt := time.Unix(int64(afterCooldown), 0).UTC().Add(cooldown)
	if !nextAt.Equal(wantThirdNextAt) {
		t.Fatalf("third claim nextAt = %v, want %v", nextAt, wantThirdNextAt)
	}

	page, err := st.ListTransactions(ctx, u.ID, domain.StarsTransactionQuery{Limit: 10})
	if err != nil {
		t.Fatalf("list transactions: %v", err)
	}
	if len(page.Transactions) != 2 {
		t.Fatalf("transactions = %d, want 2 (one per successful claim, cooldown-blocked claim wrote nothing)", len(page.Transactions))
	}
	for _, txn := range page.Transactions {
		if txn.Reason != domain.StarsReasonMonthlyClaim || txn.Amount != 100 {
			t.Fatalf("transaction = %+v, want +100 monthly_claim", txn)
		}
	}
}
