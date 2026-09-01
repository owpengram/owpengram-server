package rpc

import (
	"context"
	"errors"
	"testing"

	"telesrv/internal/domain"
)

type partialSuggestedPostChannels struct {
	ChannelsService
	results []domain.ToggleSuggestedPostApprovalResult
	err     error
}

func (s *partialSuggestedPostChannels) ProcessSuggestedPostLifecycle(context.Context, domain.SuggestedPostLifecycleRequest) ([]domain.ToggleSuggestedPostApprovalResult, error) {
	return s.results, s.err
}

func (s *partialSuggestedPostChannels) ToggleSuggestedPostApproval(ctx context.Context, req domain.ToggleSuggestedPostApprovalRequest) (domain.ToggleSuggestedPostApprovalResult, error) {
	return s.ChannelsService.(suggestedPostApprovalService).ToggleSuggestedPostApproval(ctx, req)
}

func TestSuggestedPostDispatcherFansOutSuccessfulPrefixWhenAnotherAggregateFails(t *testing.T) {
	fixture := newRPCChannelFixture(t)
	fixture.router.deps.Channels = &partialSuggestedPostChannels{
		ChannelsService: fixture.router.deps.Channels,
		results:         []domain.ToggleSuggestedPostApprovalResult{{}},
		err:             errors.New("poisoned lifecycle row"),
	}
	if !NewSuggestedPostDispatcher(fixture.router, nil).DispatchOnce(context.Background()) {
		t.Fatal("DispatchOnce = false, want successful result preserved despite sibling failure")
	}
}

func TestSuggestedPostDispatcherContinuesAfterFanoutFailure(t *testing.T) {
	fixture := newRPCChannelFixture(t)
	fixture.router.deps.Channels = &partialSuggestedPostChannels{
		ChannelsService: fixture.router.deps.Channels,
		results: []domain.ToggleSuggestedPostApprovalResult{
			{State: domain.SuggestedPostStateCompleted},
			{State: domain.SuggestedPostStateRefunded},
		},
	}
	dispatcher := NewSuggestedPostDispatcher(fixture.router, nil)
	attempts := 0
	dispatcher.enqueue = func(_ context.Context, _ int64, result domain.ToggleSuggestedPostApprovalResult) error {
		attempts++
		if result.State == domain.SuggestedPostStateCompleted {
			return errors.New("first committed result cannot be projected online")
		}
		return nil
	}

	if !dispatcher.DispatchOnce(context.Background()) {
		t.Fatal("DispatchOnce = false, want later committed result enqueued")
	}
	if attempts != 2 {
		t.Fatalf("fanout attempts = %d, want 2", attempts)
	}
}
