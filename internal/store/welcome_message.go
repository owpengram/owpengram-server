package store

import (
	"context"
	"time"

	"telesrv/internal/domain"
)

// WelcomeMessageStore is the independent durable Layer 229 template state.
// It must not reuse transient EphemeralMessageStore or write update/outbox rows.
type WelcomeMessageStore interface {
	CreateWelcomeMessage(ctx context.Context, req domain.CreateWelcomeMessageRequest) (domain.WelcomeMessage, bool, error)
	EditWelcomeMessage(ctx context.Context, req domain.EditWelcomeMessageRequest) (domain.WelcomeMessage, error)
	ListWelcomeMessages(ctx context.Context, peer domain.Peer, hash int64) (domain.WelcomeMessageList, error)
	DeleteWelcomeMessage(ctx context.Context, peer domain.Peer, id int) (bool, error)
	DeleteAllWelcomeMessages(ctx context.Context, peer domain.Peer) (bool, error)
	HasWelcomeMessages(ctx context.Context, peer domain.Peer) (bool, error)
}

// WelcomeMessageDeliveryStore is the bounded non-PTS recovery boundary for
// actual join greetings. Implementations lease rows, ACK the first compatible
// online delivery, and physically delete pending and delivered rows at TTL.
type WelcomeMessageDeliveryStore interface {
	ClaimWelcomeMessageDeliveries(ctx context.Context, owner string, now time.Time, limit int, lease time.Duration) ([]domain.WelcomeMessageDelivery, error)
	AckWelcomeMessageDeliveries(ctx context.Context, owner string, ids []int64, deliveredAt time.Time) (int, error)
	RetryWelcomeMessageDeliveries(ctx context.Context, owner string, ids []int64, nextAttempt time.Time, lastError string) (int, error)
	DeleteExpiredWelcomeMessageDeliveries(ctx context.Context, now time.Time, limit int) (int, error)
}
