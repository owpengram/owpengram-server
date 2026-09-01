package rpc

import (
	"context"
	"time"
)

type captureRPCMetrics struct {
	messageSend    int
	messageDup     bool
	messageSendErr error
	rateLimited    int
}

func (m *captureRPCMetrics) MessageSend(_ time.Duration, duplicate bool, err error) {
	m.messageSend++
	m.messageDup = duplicate
	m.messageSendErr = err
}

func (m *captureRPCMetrics) MessageRateLimited(retryAfterSeconds int) {
	m.rateLimited = retryAfterSeconds
}

func (m *captureRPCMetrics) OutboxClaimed(int) {}

func (m *captureRPCMetrics) OutboxDelivered(time.Duration) {}

func (m *captureRPCMetrics) OutboxFailed(error) {}

func (m *captureRPCMetrics) PresenceLastSeenBatch(int, time.Duration, error) {}

func (m *captureRPCMetrics) PresenceLastSeenSubmitted() {}

func (m *captureRPCMetrics) PresenceLastSeenPending(int) {}

func (m *captureRPCMetrics) PresenceLastSeenOverflow() {}

func (m *captureRPCMetrics) PresenceLastSeenDrainDropped(int) {}

type rateLimitCall struct {
	key    string
	cost   int
	limit  int
	window time.Duration
}

type captureRateLimiter struct {
	block      bool
	retryAfter int
	calls      []rateLimitCall
}

func (l *captureRateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, int, error) {
	return l.AllowN(ctx, key, 1, limit, window)
}

func (l *captureRateLimiter) AllowN(_ context.Context, key string, cost, limit int, window time.Duration) (bool, int, error) {
	l.calls = append(l.calls, rateLimitCall{key: key, cost: cost, limit: limit, window: window})
	if l.block {
		retry := l.retryAfter
		if retry <= 0 {
			retry = 1
		}
		return false, retry, nil
	}
	return true, 0, nil
}
