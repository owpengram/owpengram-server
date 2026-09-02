package maintenance

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// DispatchOutboxRetentionStore 清理彻底失败（已放弃重试）的 outbox 死任务。
type DispatchOutboxRetentionStore interface {
	DeleteFailed(ctx context.Context, olderThan time.Duration, limit int) (int, error)
}

// TempAuthKeyRetentionStore 回收过期的 PFS temp auth key（含未绑定 key）。
type TempAuthKeyRetentionStore interface {
	DeleteExpired(ctx context.Context, expiredBefore int64, limit int) (int, error)
}

// AuthKeySessionLayerRetentionStore reclaims expired short-lived Layer
// watermarks. Selector freshness, not retention timing, is the correctness
// gate; this worker only bounds durable storage.
type AuthKeySessionLayerRetentionStore interface {
	DeleteExpiredSessionLayers(ctx context.Context, limit int) (int, error)
}

// OrphanAuthKeyRetentionStore 回收从未形成授权/temp binding 的旧握手 key。
// protected 是当前连接注册表实际使用的 raw auth_key_id 快照。
type OrphanAuthKeyRetentionStore interface {
	DeleteOrphaned(ctx context.Context, olderThan time.Duration, limit int, protected [][8]byte) (int, error)
}

type ActiveRawAuthKeyProvider interface {
	ActiveRawAuthKeyIDs() [][8]byte
}

// ActiveAuthKeyHeartbeatStore 把本实例仍在使用的 raw auth key 活性持久化。多实例下
// orphan GC 不能只看当前进程的 active 快照；其它实例的 heartbeat 会推进数据库
// last_used_at，使它们不会被误判为孤儿。
type ActiveAuthKeyHeartbeatStore interface {
	TouchActiveRawAuthKeys(ctx context.Context, ids [][8]byte) error
}

// BotAPIUpdateRetentionStore 回收 Bot API getUpdates 投递队列的死行（性能审计 H1）：
// 已确认且超过宽限期的行 + 按消息 date 超过保留期的行（官方 Bot API updates 最多保留 24h）。
type BotAPIUpdateRetentionStore interface {
	DeleteDeliveredOrExpired(ctx context.Context, confirmedGrace, maxAge time.Duration, limit int) (int, error)
}

// UserUpdateEventRetentionStore 只回收所有当前授权设备都明确确认过的账号事件前缀。
// 它不是普通 TTL：任一授权缺 state 时确认水位为 0，不得删除该设备可能仍需的事件。
type UserUpdateEventRetentionStore interface {
	DeleteConfirmedPrefix(ctx context.Context, olderThan time.Duration, limit int) (int, error)
}

// ChannelUpdateEventRetentionStore 回收超过保留期的 channel durable update 连续前缀。
// 具体 store 必须在同一事务内删除事件并推进 retained floor，低于 floor 的客户端由
// updates.getChannelDifference 走 channelDifferenceTooLong 快照恢复。
type ChannelUpdateEventRetentionStore interface {
	DeleteExpiredChannelUpdateEvents(ctx context.Context, olderThan time.Duration, limit int) (int, error)
}

// LoginCodeDeliveryRetentionStore reclaims only compact idempotency receipts
// after their associated opaque code lifetime. It must not delete the message,
// durable update event, or outbox facts created by the delivery transaction.
type LoginCodeDeliveryRetentionStore interface {
	DeleteExpiredLoginCodeDeliveries(ctx context.Context, expiredBefore time.Time, limit int) (int, error)
}

type ClientTelemetryRetentionStore interface {
	DeleteExpiredClientTelemetry(ctx context.Context, olderThan time.Time, limit int) (int, error)
}

type AuthDeliveryReportRetentionStore interface {
	DeleteExpiredAuthDeliveryReports(ctx context.Context, olderThan time.Time, limit int) (int, error)
}

type ModerationRetentionStore interface {
	DeleteExpiredSponsoredMessageImpressions(ctx context.Context, olderThan time.Time, limit int) (int, error)
	DeleteExpiredModerationAppealLinks(ctx context.Context, olderThan time.Time, limit int) (int, error)
}

// botAPIConfirmedGrace 是已确认 Bot API update 行的删除宽限：确认水位之下的行不会再被
// getUpdates 读取（fromID 恒 > confirmed），宽限仅防御 offset 回拨调试；回收目标是清堆积。
const botAPIConfirmedGrace = 15 * time.Minute

// tempAuthKeyExpiryGrace 只是一段数据库物理回收宽限。MTProto edge 在 expires_at
// 到点即停止入站 RPC、主动推送和重发，并断开连接；ResolveAuthKey 不容忍过期 key。
// 晚一天删除用于吸收客户端轮换/诊断窗口，不会延长协议有效期。
const tempAuthKeyExpiryGrace = 24 * time.Hour

const (
	// terminal failed outbox 只承担短期诊断隔离；它不是 durable update log。
	// 删除该任务会由 head trigger 立即放行同账号下一 pts，而 user_update_events
	// 继续保留，在线漏推由正常 difference 路径补偿。
	defaultOutboxPoisonRetention = time.Minute
	defaultOutboxPoisonInterval  = 15 * time.Second
	// minMediaRetentionInterval floors the derived media-sweep cadence below
	// so a very short (e.g. test-only) TELESRV_STORAGE_RETENTION_MAX_AGE
	// can't spin the sweep query in a tight loop.
	minMediaRetentionInterval = 15 * time.Second
)

// RetentionWorker 周期性回收存储中的死数据。
//
// 注意：TDesktop 不支持账号级 updates.differenceTooLong（api_updates.cpp 收到该响应只
// 记录日志且不清 requesting，会永久锁死 update 引擎），因此绝不能按普通 TTL 硬裁剪
// user_update_events。本 worker 只允许 store 删除“所有当前授权设备都明确确认”的连续安全
// 前缀；落后或缺 state 的任一设备都会把 floor 压回 0。客户端偶然带回已确认前的旧 pts 时，
// updates 服务通过普通 differenceSlice checkpoint 推进，不发送 differenceTooLong。
type RetentionWorker struct {
	outbox                      DispatchOutboxRetentionStore
	tempKeys                    TempAuthKeyRetentionStore // 可为 nil（不回收 temp key 绑定）
	authKeySessionLayers        AuthKeySessionLayerRetentionStore
	botAPIUpdates               BotAPIUpdateRetentionStore // 可为 nil（不回收 Bot API 队列）
	userUpdates                 UserUpdateEventRetentionStore
	channelUpdates              ChannelUpdateEventRetentionStore
	loginCodeDeliveries         LoginCodeDeliveryRetentionStore
	clientTelemetry             ClientTelemetryRetentionStore
	authDeliveryReports         AuthDeliveryReportRetentionStore
	moderation                  ModerationRetentionStore
	orphanAuthKeys              OrphanAuthKeyRetentionStore
	activeAuthKeys              ActiveRawAuthKeyProvider
	activeAuthKeyHeartbeat      ActiveAuthKeyHeartbeatStore
	orphanedMedia               OrphanedMediaRetentionStore
	hardMedia                   HardMediaRetentionStore
	logger                      *zap.Logger
	retention                   time.Duration
	botAPIRetention             time.Duration
	orphanRetention             time.Duration
	clientTelemetryRetention    time.Duration
	authDeliveryReportRetention time.Duration
	outboxPoisonRetention       time.Duration
	outboxPoisonInterval        time.Duration
	orphanedMediaMaxAge         time.Duration
	hardMediaMaxAge             time.Duration
	interval                    time.Duration
	batch                       int
}

func NewRetentionWorker(outbox DispatchOutboxRetentionStore, tempKeys TempAuthKeyRetentionStore, logger *zap.Logger, retention, interval time.Duration, batch int) *RetentionWorker {
	if logger == nil {
		logger = zap.NewNop()
	}
	if retention <= 0 {
		retention = 168 * time.Hour
	}
	if interval <= 0 {
		interval = time.Hour
	}
	if batch <= 0 {
		batch = 10000
	}
	return &RetentionWorker{
		outbox:                outbox,
		tempKeys:              tempKeys,
		logger:                logger,
		retention:             retention,
		outboxPoisonRetention: defaultOutboxPoisonRetention,
		outboxPoisonInterval:  defaultOutboxPoisonInterval,
		interval:              interval,
		batch:                 batch,
	}
}

// WithDispatchOutboxPoisonPolicy 配置 terminal failed head 的独立短隔离与清理周期。
// 该周期不能复用 durable update 的周级保留期，否则一条确定性构造错误会冻结该
// 用户整条在线 pts lane。<=0 分别回退到 1m/15s 的安全默认值。
func (w *RetentionWorker) WithDispatchOutboxPoisonPolicy(retention, interval time.Duration) *RetentionWorker {
	if retention <= 0 {
		retention = defaultOutboxPoisonRetention
	}
	if interval <= 0 {
		interval = defaultOutboxPoisonInterval
	}
	w.outboxPoisonRetention = retention
	w.outboxPoisonInterval = interval
	return w
}

// WithBotAPIUpdateRetention 启用 bot_api_updates 队列回收；retention <=0 时用官方语义默认 24h。
func (w *RetentionWorker) WithBotAPIUpdateRetention(store BotAPIUpdateRetentionStore, retention time.Duration) *RetentionWorker {
	if retention <= 0 {
		retention = 24 * time.Hour
	}
	w.botAPIUpdates = store
	w.botAPIRetention = retention
	return w
}

// WithUserUpdateRetention 启用账号 update 的共同确认安全前缀回收。TDesktop 不支持
// account differenceTooLong，具体 store 必须保证未确认前缀永不删除。
func (w *RetentionWorker) WithUserUpdateRetention(store UserUpdateEventRetentionStore) *RetentionWorker {
	w.userUpdates = store
	return w
}

// WithChannelUpdateRetention 启用 channel durable update 的有界 TTL 回收；复用 worker 的
// retention/interval/batch，并由 store 的 retained floor 保证旧 pts 不会读到静默空洞。
func (w *RetentionWorker) WithChannelUpdateRetention(store ChannelUpdateEventRetentionStore) *RetentionWorker {
	w.channelUpdates = store
	return w
}

// WithLoginCodeDeliveryRetention enables bounded seek cleanup for compact
// phone_code_hash receipts. Each row carries its own expiry derived from the
// code TTL, so this cleanup intentionally does not reuse update-log retention.
func (w *RetentionWorker) WithLoginCodeDeliveryRetention(store LoginCodeDeliveryRetentionStore) *RetentionWorker {
	w.loginCodeDeliveries = store
	return w
}

// WithAuthKeySessionLayerRetention enables bounded seek cleanup for expired
// per-session Layer evidence.
func (w *RetentionWorker) WithAuthKeySessionLayerRetention(store AuthKeySessionLayerRetentionStore) *RetentionWorker {
	w.authKeySessionLayers = store
	return w
}

func (w *RetentionWorker) WithClientTelemetryRetention(store ClientTelemetryRetentionStore, retention time.Duration) *RetentionWorker {
	if retention <= 0 {
		retention = 30 * 24 * time.Hour
	}
	w.clientTelemetry = store
	w.clientTelemetryRetention = retention
	return w
}

func (w *RetentionWorker) WithAuthDeliveryReportRetention(store AuthDeliveryReportRetentionStore, retention time.Duration) *RetentionWorker {
	if retention <= 0 {
		retention = 30 * 24 * time.Hour
	}
	w.authDeliveryReports = store
	w.authDeliveryReportRetention = retention
	return w
}

func (w *RetentionWorker) WithModerationRetention(store ModerationRetentionStore) *RetentionWorker {
	w.moderation = store
	return w
}

// WithOrphanAuthKeyRetention 启用未授权握手 key 的有界回收。active 必须提供 raw key，
// 不能提供 temp→perm business key；否则未登录或 PFS 连接会被误判为 orphan。
func (w *RetentionWorker) WithOrphanAuthKeyRetention(store OrphanAuthKeyRetentionStore, active ActiveRawAuthKeyProvider, retention time.Duration) *RetentionWorker {
	w.orphanAuthKeys = store
	w.activeAuthKeys = active
	w.activeAuthKeyHeartbeat, _ = store.(ActiveAuthKeyHeartbeatStore)
	w.orphanRetention = retention
	return w
}

func (w *RetentionWorker) Run(ctx context.Context) {
	w.runOnce(ctx)
	retentionTicker := time.NewTicker(w.interval)
	defer retentionTicker.Stop()
	poisonTicker := time.NewTicker(w.outboxPoisonInterval)
	defer poisonTicker.Stop()
	var (
		heartbeatTicker *time.Ticker
		heartbeatC      <-chan time.Time
	)
	if interval := w.orphanHeartbeatInterval(); interval > 0 {
		heartbeatTicker = time.NewTicker(interval)
		heartbeatC = heartbeatTicker.C
		defer heartbeatTicker.Stop()
	}
	var (
		mediaTicker *time.Ticker
		mediaC      <-chan time.Time
	)
	if interval := w.mediaRetentionInterval(); interval > 0 {
		mediaTicker = time.NewTicker(interval)
		mediaC = mediaTicker.C
		defer mediaTicker.Stop()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-retentionTicker.C:
			w.runRetentionOnce(ctx)
		case <-poisonTicker.C:
			w.runOutboxPoisonOnce(ctx)
		case <-heartbeatC:
			w.heartbeatActiveAuthKeys(ctx)
		case <-mediaC:
			w.runMediaRetentionOnce(ctx)
		}
	}
}

func (w *RetentionWorker) runOnce(ctx context.Context) {
	w.runOutboxPoisonOnce(ctx)
	w.runRetentionOnce(ctx)
	w.runMediaRetentionOnce(ctx)
}

// mediaRetentionInterval derives how often the storage media sweep
// (orphan/hard) runs, independent of the shared housekeeping w.interval.
// A short TELESRV_STORAGE_RETENTION_MAX_AGE (e.g. 30m) configured under a
// much longer TELESRV_RETENTION_INTERVAL (default 1h) would otherwise let
// media sit for up to age+interval past its cutoff before actually being
// swept -- capping the sweep cadence at the retention age itself bounds
// that worst case to at most 2x the configured age instead.
func (w *RetentionWorker) mediaRetentionInterval() time.Duration {
	var maxAge time.Duration
	if w.orphanedMedia != nil && w.orphanedMediaMaxAge > 0 {
		maxAge = w.orphanedMediaMaxAge
	}
	if w.hardMedia != nil && w.hardMediaMaxAge > 0 && (maxAge == 0 || w.hardMediaMaxAge < maxAge) {
		maxAge = w.hardMediaMaxAge
	}
	if maxAge <= 0 {
		return 0
	}
	interval := w.interval
	if interval <= 0 || maxAge < interval {
		interval = maxAge
	}
	if interval < minMediaRetentionInterval {
		interval = minMediaRetentionInterval
	}
	return interval
}

func (w *RetentionWorker) runMediaRetentionOnce(ctx context.Context) {
	if w.orphanedMedia != nil && w.orphanedMediaMaxAge > 0 {
		// Only ever touches documents/photos already marked orphaned (no live
		// message/profile-photo/sticker-set reference remains) -- media still
		// visible in a conversation is never a candidate, regardless of age.
		mediaDeleted, err := w.orphanedMedia.DeleteOrphanedOlderThan(ctx, time.Now().Add(-w.orphanedMediaMaxAge), w.batch)
		if err != nil {
			w.logger.Warn("orphaned media storage retention sweep failed", zap.Error(err))
		} else if mediaDeleted > 0 {
			w.logger.Info("orphaned media storage retention sweep complete", zap.Int("deleted", mediaDeleted))
		}
	}
	if w.hardMedia != nil && w.hardMediaMaxAge > 0 {
		// "Hard" mode: purges blob bytes for documents/photos older than
		// hardMediaMaxAge regardless of whether a live reference remains --
		// the store is required to keep the document/photo metadata row
		// intact, only removing file_blobs rows/bytes, so a message still
		// renders its media placeholder (LOCATION_INVALID on download)
		// instead of breaking outright.
		hardDeleted, err := w.hardMedia.DeleteBlobBytesForMediaOlderThan(ctx, time.Now().Add(-w.hardMediaMaxAge), w.batch)
		if err != nil {
			w.logger.Warn("hard media storage retention sweep failed", zap.Error(err))
		} else if hardDeleted > 0 {
			w.logger.Info("hard media storage retention sweep complete", zap.Int("deleted", hardDeleted))
		}
	}
}

func (w *RetentionWorker) runOutboxPoisonOnce(ctx context.Context) {
	if w.outbox == nil {
		return
	}
	outboxDeleted, err := w.outbox.DeleteFailed(ctx, w.outboxPoisonRetention, w.batch)
	if err != nil {
		w.logger.Error("cleaning up terminal-failed dispatch_outbox rows failed",
			zap.String("signal", "dispatch_outbox_poison_cleanup_failed"),
			zap.Duration("quarantine", w.outboxPoisonRetention),
			zap.Error(err),
		)
	} else if outboxDeleted > 0 {
		// The Error level is intentionally kept: a terminal failure means deterministic
		// encoding, a missing event, or some other non-retryable fault. Deleting the row
		// only unfreezes the online lane — it does not delete the durable event.
		w.logger.Error("terminal-failed dispatch_outbox rows released from quarantine and unfroze their user lane",
			zap.String("signal", "dispatch_outbox_poison_released"),
			zap.Int("deleted", outboxDeleted),
			zap.Duration("quarantine", w.outboxPoisonRetention),
		)
	}
}

func (w *RetentionWorker) runRetentionOnce(ctx context.Context) {
	if w.authKeySessionLayers != nil {
		deleted, err := w.authKeySessionLayers.DeleteExpiredSessionLayers(ctx, w.batch)
		if err != nil {
			w.logger.Warn("expired auth-key session layer evidence cleanup failed", zap.Error(err))
		} else if deleted > 0 {
			w.logger.Info("expired auth-key session layer evidence cleanup complete", zap.Int("deleted", deleted))
		}
	}
	if w.loginCodeDeliveries != nil {
		deleted, err := w.loginCodeDeliveries.DeleteExpiredLoginCodeDeliveries(ctx, time.Now(), w.batch)
		if err != nil {
			w.logger.Warn("expired login-code delivery receipt cleanup failed", zap.Error(err))
		} else if deleted > 0 {
			w.logger.Info("expired login-code delivery receipt cleanup complete", zap.Int("deleted", deleted))
		}
	}
	if w.clientTelemetry != nil {
		deleted, err := w.clientTelemetry.DeleteExpiredClientTelemetry(
			ctx, time.Now().Add(-w.clientTelemetryRetention), w.batch,
		)
		if err != nil {
			w.logger.Warn("回收过期客户端 telemetry 失败", zap.Error(err))
		} else if deleted > 0 {
			w.logger.Info("回收过期客户端 telemetry 完成", zap.Int("deleted", deleted))
		}
	}
	if w.authDeliveryReports != nil {
		deleted, err := w.authDeliveryReports.DeleteExpiredAuthDeliveryReports(
			ctx, time.Now().Add(-w.authDeliveryReportRetention), w.batch,
		)
		if err != nil {
			w.logger.Warn("回收过期验证码投递诊断失败", zap.Error(err))
		} else if deleted > 0 {
			w.logger.Info("回收过期验证码投递诊断完成", zap.Int("deleted", deleted))
		}
	}
	if w.moderation != nil {
		now := time.Now()
		impressions, err := w.moderation.DeleteExpiredSponsoredMessageImpressions(
			ctx, now, w.batch,
		)
		if err != nil {
			w.logger.Warn("回收过期 sponsored impression 失败", zap.Error(err))
		} else if impressions > 0 {
			w.logger.Info("回收过期 sponsored impression 完成", zap.Int("deleted", impressions))
		}
		links, err := w.moderation.DeleteExpiredModerationAppealLinks(
			ctx, now, w.batch,
		)
		if err != nil {
			w.logger.Warn("回收过期审核申诉链接失败", zap.Error(err))
		} else if links > 0 {
			w.logger.Info("回收过期审核申诉链接完成", zap.Int("deleted", links))
		}
	}
	if w.tempKeys != nil {
		expiredBefore := time.Now().Add(-tempAuthKeyExpiryGrace).Unix()
		tempDeleted, err := w.tempKeys.DeleteExpired(ctx, expiredBefore, w.batch)
		if err != nil {
			w.logger.Warn("expired temp auth key binding cleanup failed", zap.Error(err))
		} else if tempDeleted > 0 {
			w.logger.Info("expired temp auth key binding cleanup complete", zap.Int("deleted", tempDeleted))
		}
	}
	if w.orphanAuthKeys != nil && w.orphanRetention > 0 {
		var protected [][8]byte
		if w.activeAuthKeys != nil {
			protected = w.activeAuthKeys.ActiveRawAuthKeyIDs()
		}
		if !w.touchActiveAuthKeys(ctx, protected) {
			// Fail safe: if this instance cannot publish its own active set, deleting against a
			// stale database heartbeat could evict keys used by another instance too. Keep all
			// candidates for this pass and retry after the next heartbeat.
		} else {
			orphanDeleted, err := w.orphanAuthKeys.DeleteOrphaned(ctx, w.orphanRetention, w.batch, protected)
			if err != nil {
				w.logger.Warn("unauthorized orphan auth key cleanup failed", zap.Error(err))
			} else if orphanDeleted > 0 {
				w.logger.Info("unauthorized orphan auth key cleanup complete", zap.Int("deleted", orphanDeleted))
			}
		}
	}
	if w.botAPIUpdates != nil {
		botAPIDeleted, err := w.botAPIUpdates.DeleteDeliveredOrExpired(ctx, botAPIConfirmedGrace, w.botAPIRetention, w.batch)
		if err != nil {
			w.logger.Warn("bot_api_updates queue cleanup failed", zap.Error(err))
		} else if botAPIDeleted > 0 {
			w.logger.Info("bot_api_updates queue cleanup complete", zap.Int("deleted", botAPIDeleted))
		}
	}
	if w.userUpdates != nil {
		userDeleted, err := w.userUpdates.DeleteConfirmedPrefix(ctx, w.retention, w.batch)
		if err != nil {
			w.logger.Warn("jointly-confirmed user_update_events prefix cleanup failed", zap.Error(err))
		} else if userDeleted > 0 {
			w.logger.Info("jointly-confirmed user_update_events prefix cleanup complete", zap.Int("deleted", userDeleted))
		}
	}
	if w.channelUpdates != nil {
		channelDeleted, err := w.channelUpdates.DeleteExpiredChannelUpdateEvents(ctx, w.retention, w.batch)
		if err != nil {
			// The store quarantines bad gaps per-channel and keeps going for the rest of this
			// pass; deleted can be non-zero, so it must be logged alongside the error — the pass
			// must not look like a total failure, nor should the invariant error get swallowed.
			w.logger.Warn("expired channel_update_events cleanup hit quarantined channels",
				zap.Int("deleted", channelDeleted),
				zap.Error(err),
			)
		} else if channelDeleted > 0 {
			w.logger.Info("expired channel_update_events contiguous-prefix cleanup complete", zap.Int("deleted", channelDeleted))
		}
	}
}

func (w *RetentionWorker) orphanHeartbeatInterval() time.Duration {
	if w.activeAuthKeyHeartbeat == nil || w.activeAuthKeys == nil || w.orphanRetention <= 0 {
		return 0
	}
	interval := w.orphanRetention / 3
	if interval <= 0 {
		interval = time.Nanosecond
	}
	if w.interval > 0 && w.interval < interval {
		interval = w.interval
	}
	return interval
}

func (w *RetentionWorker) heartbeatActiveAuthKeys(ctx context.Context) {
	if w.activeAuthKeys == nil {
		return
	}
	w.touchActiveAuthKeys(ctx, w.activeAuthKeys.ActiveRawAuthKeyIDs())
}

// touchActiveAuthKeys returns false only when a configured durable heartbeat failed. A store that
// predates the optional heartbeat interface keeps single-instance behavior.
func (w *RetentionWorker) touchActiveAuthKeys(ctx context.Context, protected [][8]byte) bool {
	if w.activeAuthKeyHeartbeat == nil {
		return true
	}
	if err := w.activeAuthKeyHeartbeat.TouchActiveRawAuthKeys(ctx, protected); err != nil {
		w.logger.Error("refreshing active raw auth key heartbeat failed, skipping orphan GC this round",
			zap.String("signal", "auth_key_heartbeat_failed"),
			zap.Int("active_keys", len(protected)),
			zap.Error(err),
		)
		return false
	}
	return true
}
