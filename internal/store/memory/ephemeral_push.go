package memory

import (
	"context"

	"telesrv/internal/store"
)

var _ store.EphemeralPushBroker = (*EphemeralMessageStore)(nil)

// PublishEphemeralPush fans event out to every current subscriber. Matches
// store.EphemeralPush's own doc comment: this is a "process-to-process
// online accelerator," deliberately not durable -- a push nobody is
// listening for yet is simply not delivered, same as the Redis Pub/Sub
// version it replaces (a subscriber that starts after Publish never sees
// it either).
func (s *EphemeralMessageStore) PublishEphemeralPush(ctx context.Context, event store.EphemeralPush) error {
	s.pushMu.Lock()
	subs := append([]func(context.Context, store.EphemeralPush){}, s.pushSubscribers...)
	s.pushMu.Unlock()
	for _, sub := range subs {
		go sub(ctx, event)
	}
	return nil
}

// SubscribeEphemeralPushes registers handle and blocks until ctx is
// cancelled, matching the Redis version's blocking-subscribe contract.
func (s *EphemeralMessageStore) SubscribeEphemeralPushes(ctx context.Context, handle func(context.Context, store.EphemeralPush)) error {
	if handle == nil {
		return nil
	}
	s.pushMu.Lock()
	s.pushSubscribers = append(s.pushSubscribers, handle)
	s.pushMu.Unlock()
	<-ctx.Done()
	return nil
}
