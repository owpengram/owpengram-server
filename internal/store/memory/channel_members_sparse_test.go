package memory

import (
	"context"
	"errors"
	"testing"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

func TestFilterActiveChannelMemberPairsKeepsExactEdges(t *testing.T) {
	ctx := context.Background()
	channels := NewChannelStore()
	first, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: 1,
		MemberUserIDs: []int64{11, 12},
		Title:         "first",
		Megagroup:     true,
		Date:          1700000000,
	})
	if err != nil {
		t.Fatalf("CreateChannel(first): %v", err)
	}
	second, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: 2,
		MemberUserIDs: []int64{11, 12},
		Title:         "second",
		Megagroup:     true,
		Date:          1700000001,
	})
	if err != nil {
		t.Fatalf("CreateChannel(second): %v", err)
	}

	got, err := channels.FilterActiveChannelMemberPairs(ctx, map[int64][]int64{
		first.Channel.ID:  {11},
		second.Channel.ID: {12},
	})
	if err != nil {
		t.Fatalf("FilterActiveChannelMemberPairs: %v", err)
	}
	if len(got[first.Channel.ID]) != 1 || got[first.Channel.ID][0] != 11 {
		t.Fatalf("first channel result = %+v, want [11]", got[first.Channel.ID])
	}
	if len(got[second.Channel.ID]) != 1 || got[second.Channel.ID][0] != 12 {
		t.Fatalf("second channel result = %+v, want [12]", got[second.Channel.ID])
	}
}

func TestFilterActiveChannelMemberPairsRejectsOverLimit(t *testing.T) {
	userIDs := make([]int64, store.MaxActiveChannelMemberPairs+1)
	for i := range userIDs {
		userIDs[i] = int64(i + 1)
	}
	_, err := NewChannelStore().FilterActiveChannelMemberPairs(context.Background(), map[int64][]int64{1: userIDs})
	if !errors.Is(err, store.ErrActiveChannelMemberPairsLimit) {
		t.Fatalf("FilterActiveChannelMemberPairs error = %v, want ErrActiveChannelMemberPairsLimit", err)
	}
}
