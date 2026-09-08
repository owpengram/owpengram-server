package postgres

import (
	"context"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

// channelChangeNotifyChannel 是 channels 触发器(迁移 0117) pg_notify 的频道名，必须与 SQL 一致。
const channelChangeNotifyChannel = "telesrv_channel_changed"

const (
	channelListenerInitialBackoff = 500 * time.Millisecond
	channelListenerMaxBackoff     = 30 * time.Second
)

// ChannelChangeListener 用一条专用 PG 连接 LISTEN channels 变更通知，实时失效 ChannelRowCache。
//
// 强一致保证：每次 (重)连接先建立 LISTEN、再 flush 整表——任何在断连窗口内提交、其 NOTIFY 未送达
// 的变更，都会被这次 flush 清掉；LISTEN 建立之后提交的变更则由 NOTIFY 精确 delete。两者衔接处由
// flush 兜底，故缓存对 channels 表始终强一致。
type ChannelChangeListener struct {
	dsn   string
	cache *ChannelRowCache
	log   *zap.Logger
}

// NewChannelChangeListener 创建监听器；cache 为 nil 时 Run 直接返回(缓存禁用即无需监听)。
func NewChannelChangeListener(dsn string, cache *ChannelRowCache, log *zap.Logger) *ChannelChangeListener {
	if log == nil {
		log = zap.NewNop()
	}
	return &ChannelChangeListener{dsn: dsn, cache: cache, log: log}
}

// Run 阻塞运行监听循环直到 ctx 取消(应在独立 goroutine 启动)。断连自动重连(指数退避)。
func (l *ChannelChangeListener) Run(ctx context.Context) {
	if l == nil || l.cache == nil {
		return
	}
	backoff := channelListenerInitialBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		err := l.listenAndConsume(ctx)
		if ctx.Err() != nil {
			return
		}
		l.log.Warn("channel change listener disconnected; reconnecting",
			zap.Error(err), zap.Duration("backoff", backoff))
		if !sleepCtx(ctx, backoff) {
			return
		}
		backoff *= 2
		if backoff > channelListenerMaxBackoff {
			backoff = channelListenerMaxBackoff
		}
	}
}

// listenAndConsume 建立一条连接、LISTEN、flush，然后消费通知直到出错或 ctx 取消。
// 成功消费到通知前不重置退避，由 Run 控制重连节奏。
func (l *ChannelChangeListener) listenAndConsume(ctx context.Context) error {
	conn, err := connectPostgresAdmitted(ctx, l.dsn)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx, "LISTEN "+channelChangeNotifyChannel); err != nil {
		return err
	}
	// LISTEN 已就绪：清空缓存，丢弃断连窗口内可能已变更但通知未送达的条目。
	l.cache.flush()
	l.log.Info("channel change listener ready", zap.String("notify_channel", channelChangeNotifyChannel))

	for {
		notification, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		if notification == nil {
			continue
		}
		id, perr := strconv.ParseInt(strings.TrimSpace(notification.Payload), 10, 64)
		if perr != nil || id == 0 {
			continue
		}
		l.cache.delete(id)
	}
}

// sleepCtx 睡 d 或在 ctx 取消时提前返回；返回 false 表示 ctx 已取消。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
