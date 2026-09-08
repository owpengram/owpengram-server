package postgres

import (
	"context"
	"telesrv/internal/domain"
	"testing"
)

func TestMessageStoreListByUserSupportsForwardAndAroundHistoryOffsets(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)

	users := NewUserStore(pool)
	alice, err := users.Create(ctx, domain.User{
		AccessHash: 91,
		Phone:      "+1667" + suffix + "01",
		FirstName:  "Alice",
	})
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	bob, err := users.Create(ctx, domain.User{
		AccessHash: 92,
		Phone:      "+1667" + suffix + "02",
		FirstName:  "Bob",
	})
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1::bigint[])", []int64{alice.ID, bob.ID})
	})

	messages := NewMessageStore(pool)
	for i := 1; i <= 6; i++ {
		if _, err := messages.SendPrivateText(ctx, domain.SendPrivateTextRequest{
			SenderUserID:    alice.ID,
			RecipientUserID: bob.ID,
			RandomID:        int64(700 + i),
			Message:         "history",
			Date:            1700000000 + i,
		}); err != nil {
			t.Fatalf("seed message %d: %v", i, err)
		}
	}
	peer := domain.Peer{Type: domain.PeerTypeUser, ID: alice.ID}

	around, err := messages.ListByUser(ctx, bob.ID, domain.MessageFilter{
		HasPeer:        true,
		Peer:           peer,
		OffsetID:       3,
		AddOffset:      -3,
		Limit:          6,
		NeedTotalCount: true,
	})
	if err != nil {
		t.Fatalf("around history: %v", err)
	}
	if got := messageIDs(around.Messages); !sameInts(got, []int{5, 4, 3, 2, 1}) {
		t.Fatalf("around ids = %v, want unread/newer side plus older context", got)
	}
	if around.Count != 6 {
		t.Fatalf("around count = %d, want full dialog count", around.Count)
	}

	forward, err := messages.ListByUser(ctx, bob.ID, domain.MessageFilter{
		HasPeer:        true,
		Peer:           peer,
		OffsetID:       3,
		AddOffset:      -3,
		Limit:          3,
		NeedTotalCount: true,
	})
	if err != nil {
		t.Fatalf("forward history: %v", err)
	}
	if got := messageIDs(forward.Messages); !sameInts(got, []int{5, 4, 3}) {
		t.Fatalf("forward ids = %v, want forward window including offset anchor", got)
	}
	if forward.Count != 6 {
		t.Fatalf("forward count = %d, want full dialog count", forward.Count)
	}
}

func TestMessageStoreSearchPagesUseHasMoreHintWithoutTotalCount(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)

	users := NewUserStore(pool)
	alice, err := users.Create(ctx, domain.User{
		AccessHash: 93,
		Phone:      "+1668" + suffix + "01",
		FirstName:  "Alice",
	})
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	bob, err := users.Create(ctx, domain.User{
		AccessHash: 94,
		Phone:      "+1668" + suffix + "02",
		FirstName:  "Bob",
	})
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1::bigint[])", []int64{alice.ID, bob.ID})
	})

	messages := NewMessageStore(pool)
	for i := 1; i <= 5; i++ {
		if _, err := messages.SendPrivateText(ctx, domain.SendPrivateTextRequest{
			SenderUserID:    alice.ID,
			RecipientUserID: bob.ID,
			RandomID:        int64(760 + i),
			Message:         "needle page",
			Date:            1700000100 + i,
		}); err != nil {
			t.Fatalf("seed message %d: %v", i, err)
		}
	}
	peer := domain.Peer{Type: domain.PeerTypeUser, ID: alice.ID}

	first, err := messages.ListByUser(ctx, bob.ID, domain.MessageFilter{
		HasPeer:        true,
		Peer:           peer,
		Query:          "needle",
		Limit:          2,
		NeedTotalCount: true,
	})
	if err != nil {
		t.Fatalf("first search page: %v", err)
	}
	if got := messageIDs(first.Messages); !sameInts(got, []int{5, 4}) {
		t.Fatalf("first search ids = %v, want newest page", got)
	}
	if first.Count != 5 {
		t.Fatalf("first search count = %d, want exact total", first.Count)
	}

	second, err := messages.ListByUser(ctx, bob.ID, domain.MessageFilter{
		HasPeer:        true,
		Peer:           peer,
		Query:          "needle",
		OffsetID:       4,
		Limit:          2,
		NeedTotalCount: false,
	})
	if err != nil {
		t.Fatalf("second search page: %v", err)
	}
	if got := messageIDs(second.Messages); !sameInts(got, []int{3, 2}) {
		t.Fatalf("second search ids = %v, want middle page", got)
	}
	if second.Count != 3 {
		t.Fatalf("second search count hint = %d, want len(page)+hasMore", second.Count)
	}

	last, err := messages.ListByUser(ctx, bob.ID, domain.MessageFilter{
		HasPeer:        true,
		Peer:           peer,
		Query:          "needle",
		OffsetID:       2,
		Limit:          2,
		NeedTotalCount: false,
	})
	if err != nil {
		t.Fatalf("last search page: %v", err)
	}
	if got := messageIDs(last.Messages); !sameInts(got, []int{1}) {
		t.Fatalf("last search ids = %v, want final message", got)
	}
	if last.Count != 1 {
		t.Fatalf("last search count hint = %d, want page length without hasMore", last.Count)
	}
}
