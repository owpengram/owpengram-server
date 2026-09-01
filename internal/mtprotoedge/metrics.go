package mtprotoedge

import "time"

// Metrics 接收连接层运行指标。生产入口接入有界 Prometheus exporter；
// 其它 embedder 可继续使用 NopMetrics（零开销）。
type Metrics interface {
	// ConnOpened 在接受一个连接时调用。
	ConnOpened()
	// ConnClosed 在一个连接结束时调用。
	ConnClosed()
	// HandshakeDone 在一次密钥交换成功完成时调用，d 为握手耗时。
	HandshakeDone(d time.Duration)
	// RPCHandled 在一次 RPC 处理完成时调用：method 为 TL 方法名，
	// d 为耗时，err 非 nil 表示失败。
	RPCHandled(method string, d time.Duration, err error)
	// InboundRPCQueued 在 RPC 成功进入单连接 bounded queue 时调用。
	InboundRPCQueued(method string, len, cap int)
	// InboundRPCStarted 在 RPC 从 bounded queue 取出开始执行时调用。
	InboundRPCStarted(method string, queueWait time.Duration)
	// InboundRPCDropped 在 RPC 因队列满、连接关闭或调度错误被丢弃时调用。
	InboundRPCDropped(method, reason string)
	// OutboundSend 在一条 server 出站消息完成写入或失败时调用。
	OutboundSend(typeID uint32, queueWait time.Duration, bytes int, err error)
	// OutboundResend 在一次 msg_resend_req/重复 RPC 触发重发后调用。
	OutboundResend(count int, err error)
	// OutboundDropped 在出站队列或状态跟踪因背压丢弃时调用。
	OutboundDropped(reason string)
	// OutboundQueueWait 在出站入队等待超过阈值时调用。
	OutboundQueueWait(len, cap int)
}

// RPCDatabaseMetrics is an optional extension for request-scoped database
// work. queries/errors are counts attributed to one RPC and duration is the
// cumulative database time observed by the query wrapper under that request.
type RPCDatabaseMetrics interface {
	RPCDatabase(method string, queries int64, duration time.Duration, errors int64)
}

// RPCResultMetrics is an optional extension for the detached response pipeline.
// Keeping it separate preserves lightweight embedders while production exporters
// can observe preparation/compression and end-to-end delivery independently from
// business handler latency.
type RPCResultMetrics interface {
	RPCResultPrepared(method, priority string, innerBytes, wireBytes int, compressed bool)
	RPCResultDelivered(method string, egressLatency time.Duration, wireBytes int, err error)
}

// LogicalOutboxMetrics observes the sole owner of unacknowledged server frames.
// It is intentionally optional: embedders can keep the small Metrics surface,
// while production capacity tests can distinguish physical delivery from the
// later client ACK that actually releases retained bytes.
type LogicalOutboxMetrics interface {
	LogicalOutboxAcknowledged(bytes int, retainedFor time.Duration, rpcResult bool)
}

// ConnectionIntakeMetrics is an optional extension for the pre-session
// connection pipeline. stage is one of raw_accept, mux_sniff, mux_delivery,
// transport_dispatch, transport_promote, or first_frame; outcome is a bounded
// value suitable for metrics labels. Remote addresses are deliberately kept in
// structured logs only so exporters cannot create unbounded cardinality.
type ConnectionIntakeMetrics interface {
	ConnectionIntake(stage, outcome string, duration time.Duration)
}

// NopMetrics 是 Metrics 的空实现。
type NopMetrics struct{}

// ConnOpened 实现 Metrics。
func (NopMetrics) ConnOpened() {}

// ConnClosed 实现 Metrics。
func (NopMetrics) ConnClosed() {}

// HandshakeDone 实现 Metrics。
func (NopMetrics) HandshakeDone(time.Duration) {}

// RPCHandled 实现 Metrics。
func (NopMetrics) RPCHandled(string, time.Duration, error) {}

// InboundRPCQueued 实现 Metrics。
func (NopMetrics) InboundRPCQueued(string, int, int) {}

// InboundRPCStarted 实现 Metrics。
func (NopMetrics) InboundRPCStarted(string, time.Duration) {}

// InboundRPCDropped 实现 Metrics。
func (NopMetrics) InboundRPCDropped(string, string) {}

// OutboundSend 实现 Metrics。
func (NopMetrics) OutboundSend(uint32, time.Duration, int, error) {}

// OutboundResend 实现 Metrics。
func (NopMetrics) OutboundResend(int, error) {}

// OutboundDropped 实现 Metrics。
func (NopMetrics) OutboundDropped(string) {}

// OutboundQueueWait 实现 Metrics。
func (NopMetrics) OutboundQueueWait(int, int) {}
