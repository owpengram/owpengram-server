package postgres

import (
	"context"
	"testing"

	"telesrv/internal/domain"
)

func TestFilterActiveChannelMemberPairsPostgresKeepsExactEdges(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)
	users := NewUserStore(pool)
	owner := createTestUser(t, ctx, users, "+1911"+suffix+"01", "Pair", "Owner")
	memberA := createTestUser(t, ctx, users, "+1911"+suffix+"02", "Pair", "A")
	memberB := createTestUser(t, ctx, users, "+1911"+suffix+"03", "Pair", "B")
	userIDs := []int64{owner.ID, memberA.ID, memberB.ID}
	var channelIDs []int64
	t.Cleanup(func() {
		if len(channelIDs) > 0 {
			_, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id = ANY($1::bigint[])", channelIDs)
		}
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1::bigint[])", userIDs)
	})

	channels := NewChannelStore(pool)
	first, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID,
		MemberUserIDs: []int64{memberA.ID, memberB.ID},
		Title:         "pair first " + suffix,
		Megagroup:     true,
		Date:          1700000000,
	})
	if err != nil {
		t.Fatalf("CreateChannel(first): %v", err)
	}
	channelIDs = append(channelIDs, first.Channel.ID)
	second, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID,
		MemberUserIDs: []int64{memberA.ID, memberB.ID},
		Title:         "pair second " + suffix,
		Megagroup:     true,
		Date:          1700000001,
	})
	if err != nil {
		t.Fatalf("CreateChannel(second): %v", err)
	}
	channelIDs = append(channelIDs, second.Channel.ID)

	got, err := channels.FilterActiveChannelMemberPairs(ctx, map[int64][]int64{
		first.Channel.ID:  {memberA.ID},
		second.Channel.ID: {memberB.ID},
	})
	if err != nil {
		t.Fatalf("FilterActiveChannelMemberPairs: %v", err)
	}
	if len(got[first.Channel.ID]) != 1 || got[first.Channel.ID][0] != memberA.ID {
		t.Fatalf("first channel result = %+v, want [%d]", got[first.Channel.ID], memberA.ID)
	}
	if len(got[second.Channel.ID]) != 1 || got[second.Channel.ID][0] != memberB.ID {
		t.Fatalf("second channel result = %+v, want [%d]", got[second.Channel.ID], memberB.ID)
	}
}
