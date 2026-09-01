package postgres

import (
	"context"
	"slices"
	"testing"

	"telesrv/internal/domain"
)

func TestChannelStoreListActiveChannelIDPagesPreservesSelectorOrdinality(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)
	users := NewUserStore(pool)
	owner, err := users.Create(ctx, domain.User{AccessHash: 901, Phone: "+1887" + suffix + "01", FirstName: "BatchOwner"})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	member, err := users.Create(ctx, domain.User{AccessHash: 902, Phone: "+1887" + suffix + "02", FirstName: "BatchMember"})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	channels := NewChannelStore(pool)
	first, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID, Title: "Batch First " + suffix, Megagroup: true,
		MemberUserIDs: []int64{member.ID}, Date: 1700007010,
	})
	if err != nil {
		t.Fatalf("create first channel: %v", err)
	}
	second, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID, Title: "Batch Second " + suffix, Megagroup: true, Date: 1700007011,
	})
	if err != nil {
		t.Fatalf("create second channel: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id = ANY($1::bigint[])", []int64{first.Channel.ID, second.Channel.ID})
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1::bigint[])", []int64{owner.ID, member.ID})
	})
	selectors := []activeChannelIDsSelector{
		{userID: owner.ID, afterChannelID: 0, limit: 1},
		{userID: member.ID, afterChannelID: 0, limit: 1000},
		{userID: owner.ID, afterChannelID: first.Channel.ID, limit: 1000},
	}
	pages, err := channels.listActiveChannelIDPages(ctx, selectors)
	if err != nil {
		t.Fatalf("list batch: %v", err)
	}
	for index, selector := range selectors {
		want, err := channels.ListActiveChannelIDsForUser(ctx, selector.userID, selector.afterChannelID, selector.limit)
		if err != nil {
			t.Fatalf("list direct selector %d: %v", index, err)
		}
		if !slices.Equal(pages[index], want) {
			t.Fatalf("page %d = %v, want %v", index, pages[index], want)
		}
	}
}
