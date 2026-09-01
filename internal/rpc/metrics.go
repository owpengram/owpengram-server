package rpc

import "time"

// Metrics 接收 RPC 业务层指标。默认 NopMetrics，后续可对接 Prometheus。
type Metrics interface {
	MessageSend(d time.Duration, duplicate bool, err error)
	MessageRateLimited(retryAfterSeconds int)
	OutboxClaimed(count int)
	OutboxDelivered(d time.Duration)
	OutboxFailed(err error)
	PresenceLastSeenBatch(count int, d time.Duration, err error)
	PresenceLastSeenSubmitted()
	PresenceLastSeenPending(delta int)
	PresenceLastSeenOverflow()
	PresenceLastSeenDrainDropped(count int)
}

// NopMetrics 是 Metrics 的空实现。
type NopMetrics struct{}

func (NopMetrics) MessageSend(time.Duration, bool, error) {}

func (NopMetrics) MessageRateLimited(int) {}

func (NopMetrics) OutboxClaimed(int) {}

func (NopMetrics) OutboxDelivered(time.Duration) {}

func (NopMetrics) OutboxFailed(error) {}

func (NopMetrics) PresenceLastSeenBatch(int, time.Duration, error) {}

func (NopMetrics) PresenceLastSeenSubmitted() {}

func (NopMetrics) PresenceLastSeenPending(int) {}

func (NopMetrics) PresenceLastSeenOverflow() {}

func (NopMetrics) PresenceLastSeenDrainDropped(int) {}
