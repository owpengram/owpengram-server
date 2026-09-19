package stars

import (
	"context"
	"errors"
	"testing"

	"telesrv/internal/domain"
	"telesrv/internal/store/memory"
)

func newTestService(grant int64) *Service {
	return NewService(memory.NewStarsStore(), WithStartingGrant(grant))
}

// 起始授予幂等：多次 GetBalance 只授予一次。
func TestStartingGrantOnce(t *testing.T) {
	svc := newTestService(1000)
	ctx := context.Background()
	bal, err := svc.GetBalance(ctx, 7)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if bal.Balance != 1000 || !bal.Granted {
		t.Fatalf("first balance = %+v, want 1000 granted", bal)
	}
	// 再读不应重复授予。
	bal2, err := svc.GetBalance(ctx, 7)
	if err != nil {
		t.Fatalf("GetBalance#2: %v", err)
	}
	if bal2.Balance != 1000 {
		t.Fatalf("second balance = %d, want 1000 (no double grant)", bal2.Balance)
	}
	// 流水里应恰有一条 grant。
	page, err := svc.ListTransactions(ctx, 7, domain.StarsTransactionQuery{Limit: 100})
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	if len(page.Transactions) != 1 || page.Transactions[0].Reason != domain.StarsReasonGrant || page.Transactions[0].Amount != 1000 {
		t.Fatalf("grant txns = %+v, want one +1000 grant", page.Transactions)
	}
}

// 关闭授予（grant=0）时余额为 0、无 grant 流水。
func TestGrantDisabled(t *testing.T) {
	svc := newTestService(0)
	bal, err := svc.GetBalance(context.Background(), 9)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if bal.Balance != 0 || bal.Granted {
		t.Fatalf("balance = %+v, want 0 not granted", bal)
	}
}

// 借记成功扣减余额并写负流水；余额不足返回 ErrStarsInsufficient 且不动账。
func TestDebitAndInsufficient(t *testing.T) {
	svc := newTestService(1000)
	ctx := context.Background()
	peer := domain.Peer{Type: domain.PeerTypeChannel, ID: 555}

	bal, err := svc.Debit(ctx, 7, 300, domain.StarsReasonReaction, peer, "paid reaction", "")
	if err != nil {
		t.Fatalf("Debit: %v", err)
	}
	if bal.Balance != 700 {
		t.Fatalf("after debit = %d, want 700", bal.Balance)
	}

	// 余额不足。
	if _, err := svc.Debit(ctx, 7, 10_000, domain.StarsReasonReaction, peer, "", ""); !errors.Is(err, domain.ErrStarsInsufficient) {
		t.Fatalf("over-debit err = %v, want ErrStarsInsufficient", err)
	}
	// 余额未被改动。
	after, _ := svc.GetBalance(ctx, 7)
	if after.Balance != 700 {
		t.Fatalf("balance after failed debit = %d, want 700 unchanged", after.Balance)
	}

	// 非法金额。
	if _, err := svc.Debit(ctx, 7, 0, domain.StarsReasonReaction, peer, "", ""); !errors.Is(err, domain.ErrStarsInvalidAmount) {
		t.Fatalf("zero debit err = %v, want ErrStarsInvalidAmount", err)
	}
}

// 贷记增加余额并写正流水。
func TestCredit(t *testing.T) {
	svc := newTestService(0) // 关闭起始授予，单测贷记
	ctx := context.Background()
	bal, err := svc.Credit(ctx, 7, 250, domain.StarsReasonTopup, domain.Peer{}, "topup", "")
	if err != nil {
		t.Fatalf("Credit: %v", err)
	}
	if bal.Balance != 250 {
		t.Fatalf("after credit = %d, want 250", bal.Balance)
	}
}

// keyset 分页：末页 NextOffset 必须为空（否则客户端死循环）。
func TestListTransactionsPagination(t *testing.T) {
	svc := newTestService(0)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := svc.Credit(ctx, 7, int64(10+i), domain.StarsReasonTopup, domain.Peer{}, "", ""); err != nil {
			t.Fatalf("Credit#%d: %v", i, err)
		}
	}
	page1, err := svc.ListTransactions(ctx, 7, domain.StarsTransactionQuery{Limit: 2})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1.Transactions) != 2 || page1.NextOffset == "" {
		t.Fatalf("page1 = %d txns next=%q, want 2 + nonempty next", len(page1.Transactions), page1.NextOffset)
	}
	// 倒序：最新（id 最大，amount=14）在前。
	if page1.Transactions[0].Amount != 14 {
		t.Fatalf("page1[0].Amount = %d, want 14 (newest first)", page1.Transactions[0].Amount)
	}
	page2, err := svc.ListTransactions(ctx, 7, domain.StarsTransactionQuery{Offset: page1.NextOffset, Limit: 2})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2.Transactions) != 2 {
		t.Fatalf("page2 = %d txns, want 2", len(page2.Transactions))
	}
	page3, err := svc.ListTransactions(ctx, 7, domain.StarsTransactionQuery{Offset: page2.NextOffset, Limit: 2})
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(page3.Transactions) != 1 {
		t.Fatalf("page3 = %d txns, want 1 (last)", len(page3.Transactions))
	}
	if page3.NextOffset != "" {
		t.Fatalf("last page NextOffset = %q, want empty (no infinite paging)", page3.NextOffset)
	}
}

func TestListTransactionsDirectionAndAscending(t *testing.T) {
	svc := newTestService(0)
	ctx := context.Background()
	if _, err := svc.Credit(ctx, 7, 100, domain.StarsReasonTopup, domain.Peer{}, "", ""); err != nil {
		t.Fatalf("credit 100: %v", err)
	}
	if _, err := svc.Debit(ctx, 7, 40, domain.StarsReasonGift, domain.Peer{}, "", ""); err != nil {
		t.Fatalf("debit 40: %v", err)
	}
	if _, err := svc.Credit(ctx, 7, 20, domain.StarsReasonGift, domain.Peer{}, "", ""); err != nil {
		t.Fatalf("credit 20: %v", err)
	}
	if _, err := svc.Debit(ctx, 7, 10, domain.StarsReasonReaction, domain.Peer{}, "", ""); err != nil {
		t.Fatalf("debit 10: %v", err)
	}

	all, err := svc.ListTransactions(ctx, 7, domain.StarsTransactionQuery{Limit: 10})
	if err != nil {
		t.Fatalf("all transactions: %v", err)
	}
	assertStarsAmounts(t, all.Transactions, []int64{-10, 20, -40, 100})
	if all.Balance != 70 {
		t.Fatalf("all balance = %d, want 70", all.Balance)
	}

	incoming1, err := svc.ListTransactions(ctx, 7, domain.StarsTransactionQuery{
		Limit: 1, Direction: domain.StarsTransactionDirectionIncoming,
	})
	if err != nil {
		t.Fatalf("incoming page1: %v", err)
	}
	assertStarsAmounts(t, incoming1.Transactions, []int64{20})
	if incoming1.NextOffset == "" {
		t.Fatal("incoming page1 missing next offset")
	}
	incoming2, err := svc.ListTransactions(ctx, 7, domain.StarsTransactionQuery{
		Offset: incoming1.NextOffset, Limit: 1, Direction: domain.StarsTransactionDirectionIncoming,
	})
	if err != nil {
		t.Fatalf("incoming page2: %v", err)
	}
	assertStarsAmounts(t, incoming2.Transactions, []int64{100})
	if incoming2.NextOffset != "" {
		t.Fatalf("terminal incoming next offset = %q", incoming2.NextOffset)
	}

	outgoing, err := svc.ListTransactions(ctx, 7, domain.StarsTransactionQuery{
		Limit: 10, Direction: domain.StarsTransactionDirectionOutgoing, Ascending: true,
	})
	if err != nil {
		t.Fatalf("ascending outgoing: %v", err)
	}
	assertStarsAmounts(t, outgoing.Transactions, []int64{-40, -10})

	_, err = svc.ListTransactions(ctx, 7, domain.StarsTransactionQuery{Direction: 99})
	if !errors.Is(err, domain.ErrStarsTransactionQueryInvalid) {
		t.Fatalf("invalid direction error = %v", err)
	}
}

func assertStarsAmounts(t *testing.T, transactions []domain.StarsTransaction, want []int64) {
	t.Helper()
	if len(transactions) != len(want) {
		t.Fatalf("transaction count = %d, want %d: %+v", len(transactions), len(want), transactions)
	}
	for i, amount := range want {
		if transactions[i].Amount != amount {
			t.Fatalf("transaction[%d].amount = %d, want %d", i, transactions[i].Amount, amount)
		}
	}
}
