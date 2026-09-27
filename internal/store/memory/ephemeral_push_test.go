package memory

import (
	"context"
	"testing"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

func TestEphemeralPublishSubscribe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s := NewEphemeralMessageStore()

	received := make(chan store.EphemeralPush, 1)
	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()
	go func() {
		_ = s.SubscribeEphemeralPushes(subCtx, func(_ context.Context, push store.EphemeralPush) {
			received <- push
		})
	}()
	time.Sleep(20 * time.Millisecond)

	event := store.EphemeralPush{SourceID: "srv1", Kind: store.EphemeralPushNew, TargetUserID: 100,
		Message: domain.EphemeralMessage{ID: 1}}
	if err := s.PublishEphemeralPush(ctx, event); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case push := <-received:
		if push.TargetUserID != 100 || push.Kind != store.EphemeralPushNew {
			t.Fatalf("received %+v, want TargetUserID 100 Kind new", push)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber never received the published push")
	}
}

func TestEphemeralPublishWithNoSubscriberDoesNotBlockOrError(t *testing.T) {
	ctx := context.Background()
	s := NewEphemeralMessageStore()
	if err := s.PublishEphemeralPush(ctx, store.EphemeralPush{SourceID: "srv1"}); err != nil {
		t.Fatalf("Publish with no subscribers: %v", err)
	}
}

func TestEphemeralPublishFansOutToMultipleSubscribers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s := NewEphemeralMessageStore()

	const subscribers = 3
	received := make(chan struct{}, subscribers)
	for i := 0; i < subscribers; i++ {
		subCtx, subCancel := context.WithCancel(ctx)
		defer subCancel()
		go func() {
			_ = s.SubscribeEphemeralPushes(subCtx, func(context.Context, store.EphemeralPush) {
				received <- struct{}{}
			})
		}()
	}
	time.Sleep(20 * time.Millisecond)
	if err := s.PublishEphemeralPush(ctx, store.EphemeralPush{SourceID: "srv1"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	for i := 0; i < subscribers; i++ {
		select {
		case <-received:
		case <-time.After(time.Second):
			t.Fatalf("only %d of %d subscribers received the push", i, subscribers)
		}
	}
}
