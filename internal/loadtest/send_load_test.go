package loadtest

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/iamxvbaba/td/clock"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	messageapp "telesrv/internal/app/messages"
	appusers "telesrv/internal/app/users"
	"telesrv/internal/domain"
	"telesrv/internal/mtprotoedge"
	"telesrv/internal/rpc"
	"telesrv/internal/store/postgres"
	"telesrv/internal/store/redisstore"
)

// 第一阶段单机 SLO 目标，来自 docs/message-module.md 的 Next Execution Plan。
// 默认仅作为信息打印；设 TELESRV_LOAD_ENFORCE_SLO=1 时超标会 fail（用于回归门禁）。
const (
	sloSendP99     = 150 * time.Millisecond
	sloDiffP99     = 100 * time.Millisecond
	sloThroughput  = 200.0 // msg/s
	drainTimeout   = 60 * time.Second
	diffSampleGoal = 500
)

// TestMessageSendBaseline 用真实 PostgreSQL + Redis 压测私聊文本发送热路径，
// 并发跑 outbox dispatcher 排空在线推送，最后采样 getDifference 读路径。
//
// 这是 closed-loop 饱和压测：concurrency 个 worker 各自不停发，直到发满 messages 条。
// 吞吐 = messages / wallclock，延迟分位反映该并发下的饱和延迟。
// 固定到达率（open-loop）的版本留作后续细化（见 docs/message-module.md）。
func TestMessageSendBaseline(t *testing.T) {
	dsn := os.Getenv("TELESRV_TEST_POSTGRES_DSN")
	redisAddr := os.Getenv("TELESRV_TEST_REDIS_ADDR")
	if dsn == "" || redisAddr == "" {
		t.Skip("set TELESRV_TEST_POSTGRES_DSN and TELESRV_TEST_REDIS_ADDR to run message load baseline")
	}

	// 默认用户池取较大值：用户太少会把写集中到少数 dialog/message_box 行造成行锁争用，
	// 拉高 send 尾延迟（这是小池假象，生产 20 万用户分散后争用极低）。
	users := envInt("TELESRV_LOAD_USERS", 1000)
	if users < 2 {
		users = 2
	}
	concurrency := envInt("TELESRV_LOAD_CONCURRENCY", 32)
	if concurrency < 1 {
		concurrency = 1
	}
	totalMsgs := envInt("TELESRV_LOAD_MESSAGES", 5000)
	if totalMsgs < 1 {
		totalMsgs = 1
	}
	poolConns := envInt("TELESRV_LOAD_POOL_CONNS", 64)
	workers := envInt("TELESRV_OUTBOX_WORKERS", 8)
	outboxBatch := envInt("TELESRV_OUTBOX_BATCH", 100)
	outboxInterval := envDuration("TELESRV_OUTBOX_INTERVAL", 50*time.Millisecond)
	leaseTimeout := envDuration("TELESRV_OUTBOX_LEASE_TIMEOUT", 30*time.Second)
	enforceSLO := os.Getenv("TELESRV_LOAD_ENFORCE_SLO") == "1"
	deferDispatch := os.Getenv("TELESRV_LOAD_DEFER_DISPATCH") == "1"

	ctx := context.Background()
	if err := postgres.Migrate(dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := postgres.Open(ctx, dsn, postgres.WithMaxConns(poolConns))
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(pool.Close)

	rdb, err := redisstore.Open(ctx, redisAddr, os.Getenv("TELESRV_TEST_REDIS_PASSWORD"), 0)
	if err != nil {
		t.Fatalf("open redis: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })

	// 装配与 main.go 一致的消息热路径：Redis box_id 分配器 + PG 消息存储 + transactional outbox。
	userStore := postgres.NewUserStore(pool)
	updateEventStore := postgres.NewUpdateEventStore(pool)
	dispatchOutboxStore := postgres.NewDispatchOutboxStore(pool, postgres.WithLeaseTimeout(leaseTimeout))
	dialogStore := postgres.NewDialogStore(pool)
	boxIDAllocator := redisstore.NewBoxIDAllocator(rdb, postgres.NewMessageBoxCounterSource(pool))
	messageStore := postgres.NewMessageStore(pool, postgres.WithMessageAllocators(boxIDAllocator))
	svc := messageapp.NewService(messageStore, dialogStore)

	// 创建独立的测试用户池；用随机 salt 隔离历史残留，结束按 FK 依赖序清理。
	ids := seedUsers(t, ctx, userStore, users)
	t.Cleanup(func() { cleanup(t, pool, rdb, ids) })

	// 在线推送 binder 用真实 SessionManager（零连接），PushToUserExceptSession 返回 0，
	// 让 outbox 走完整 claim→ListAfter→MarkDelivered 的 PG 往返，测排空而非网络 fanout。
	binder := mtprotoedge.NewSessionManager(zap.NewNop())
	metrics := &loadMetrics{}
	projectionRouter := rpc.New(rpc.Config{}, rpc.Deps{
		Users: appusers.NewService(userStore),
	}, zap.NewNop(), clock.System)
	dispatcher := rpc.NewOutboxDispatcher(updateEventStore, dispatchOutboxStore, binder, zap.NewNop(),
		rpc.WithOutboxWorkers(workers),
		rpc.WithOutboxBatch(outboxBatch),
		rpc.WithOutboxInterval(outboxInterval),
		rpc.WithOutboxMetrics(metrics),
		rpc.WithOutboxUpdateBuilder(projectionRouter.BuildOutboxUpdates),
	)
	dispCtx, stopDispatcher := context.WithCancel(ctx)
	dispDone := make(chan struct{})
	startDispatcher := func() {
		go func() {
			dispatcher.Run(dispCtx)
			close(dispDone)
		}()
	}
	// deferDispatch=1 时先不投递，让发送把积压攒满，再在发送结束后启动 dispatcher，
	// 以隔离测量「纯排空上限」（否则 dispatcher 实时跟上发送、积压近 0 量不出天花板）。
	if !deferDispatch {
		startDispatcher()
	}

	// 后台采样 outbox 积压（pending+dispatching），记录运行期峰值。
	var maxBacklog atomic.Int64
	sampleCtx, stopSampler := context.WithCancel(ctx)
	sampleDone := make(chan struct{})
	go func() {
		defer close(sampleDone)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-sampleCtx.Done():
				return
			case <-ticker.C:
				if n := backlog(ctx, pool, ids); n > maxBacklog.Load() {
					maxBacklog.Store(n)
				}
			}
		}
	}()

	// 随机 RandomID 基址，保证 (sender,random_id) 幂等唯一且跨重跑不撞。
	randBase := int64(randomUint64(t) & 0x7fff_ffff_ffff)
	nowUnix := int(time.Now().Unix())
	body := "telesrv load baseline message body"

	perWorkerLat := make([][]time.Duration, concurrency)
	var sent, dup, sendErr atomic.Int64
	var counter atomic.Int64

	// 预热：先并发发一批不计时的消息，热连接池（MinConns→MaxConns）与 PG plan 缓存，
	// 让后续计时窗口反映稳态而非冷启动尾延迟（池过冷时首批 32 并发会挤少量连接）。
	warmup := envInt("TELESRV_LOAD_WARMUP", min(users*10, 1000))
	if warmup > 0 {
		var wwg sync.WaitGroup
		for w := 0; w < concurrency; w++ {
			wwg.Add(1)
			go func() {
				defer wwg.Done()
				for {
					n := counter.Add(1)
					if n > int64(warmup) {
						return
					}
					sid := ids[(n-1)%int64(users)]
					rid := ids[n%int64(users)]
					_, _ = svc.SendPrivateText(ctx, sid, domain.SendPrivateTextRequest{
						SenderUserID:    sid,
						RecipientUserID: rid,
						RandomID:        randBase + n,
						Message:         body,
						Date:            nowUnix,
					})
				}
			}()
		}
		wwg.Wait()
		counter.Store(int64(warmup))
	}

	start := time.Now()
	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			lat := make([]time.Duration, 0, totalMsgs/concurrency+1)
			for {
				n := counter.Add(1)
				if n > int64(warmup+totalMsgs) {
					break
				}
				senderID := ids[(n-1)%int64(users)]
				recipientID := ids[n%int64(users)]
				req := domain.SendPrivateTextRequest{
					SenderUserID:    senderID,
					RecipientUserID: recipientID,
					RandomID:        randBase + n,
					Message:         body,
					Date:            nowUnix,
				}
				t0 := time.Now()
				res, err := svc.SendPrivateText(ctx, senderID, req)
				lat = append(lat, time.Since(t0))
				if err != nil {
					sendErr.Add(1)
					continue
				}
				if res.Duplicate {
					dup.Add(1)
				}
				sent.Add(1)
			}
			perWorkerLat[w] = lat
		}(w)
	}
	wg.Wait()
	sendWall := time.Since(start)
	deliveredAtSendEnd := metrics.delivered.Load()
	if deferDispatch {
		// 发送已把全部积压攒满，此刻才启动 dispatcher：drain 阶段即纯排空，drainRate 反映排空上限。
		startDispatcher()
	}

	// 等 outbox 排空（积压回 0），记录排空耗时，再停采样和 dispatcher。
	// 用「发送结束后」单独排空的速率隔离 dispatcher 吞吐，去掉发送期对 PG 的争用。
	drainStart := time.Now()
	drained := waitDrain(ctx, pool, ids, drainTimeout)
	drainWall := time.Since(drainStart)
	drainRate := float64(metrics.delivered.Load()-deliveredAtSendEnd) / drainWall.Seconds()
	stopSampler()
	<-sampleDone
	stopDispatcher()
	<-dispDone

	// 合并发送延迟样本并排序。
	latencies := make([]time.Duration, 0, totalMsgs)
	for _, l := range perWorkerLat {
		latencies = append(latencies, l...)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	// 采样 getDifference 读路径（ListAfter 从 pts=0 拉账号事件）。
	diffLat := sampleGetDifference(t, ctx, updateEventStore, ids)
	sort.Slice(diffLat, func(i, j int) bool { return diffLat[i] < diffLat[j] })

	okSent := sent.Load()
	throughput := float64(okSent) / sendWall.Seconds()
	sendP99 := percentile(latencies, 99)
	diffP99 := percentile(diffLat, 99)

	t.Logf("==== message module load baseline ====")
	t.Logf("config:  users=%d concurrency=%d messages=%d pool=%d outbox(workers=%d batch=%d interval=%s lease=%s)",
		users, concurrency, totalMsgs, poolConns, workers, outboxBatch, outboxInterval, leaseTimeout)
	t.Logf("send:    %d ok, %d dup, %d err in %s -> %.0f msg/s",
		okSent, dup.Load(), sendErr.Load(), sendWall.Round(time.Millisecond), throughput)
	t.Logf("send.lat p50=%s p90=%s p99=%s max=%s",
		percentile(latencies, 50).Round(time.Microsecond),
		percentile(latencies, 90).Round(time.Microsecond),
		sendP99.Round(time.Microsecond),
		percentile(latencies, 100).Round(time.Microsecond))
	t.Logf("outbox:  delivered=%d failed=%d claimed=%d maxBacklog=%d drain=%s drainRate=%.0f rows/s drained=%v",
		metrics.delivered.Load(), metrics.failed.Load(), metrics.claimed.Load(),
		maxBacklog.Load(), drainWall.Round(time.Millisecond), drainRate, drained)
	t.Logf("getDiff: samples=%d p50=%s p90=%s p99=%s max=%s",
		len(diffLat),
		percentile(diffLat, 50).Round(time.Microsecond),
		percentile(diffLat, 90).Round(time.Microsecond),
		diffP99.Round(time.Microsecond),
		percentile(diffLat, 100).Round(time.Microsecond))
	t.Logf("SLO:     send.p99 %s(<%s) %s | getDiff.p99 %s(<%s) %s | throughput %.0f(>=%.0f) %s",
		sendP99.Round(time.Millisecond), sloSendP99, pass(sendP99 < sloSendP99),
		diffP99.Round(time.Millisecond), sloDiffP99, pass(diffP99 < sloDiffP99),
		throughput, sloThroughput, pass(throughput >= sloThroughput))
	t.Logf("=======================================")

	// 正确性硬断言：发送不应出错、不应有意外重复、outbox 必须排空且无终态失败。
	if sendErr.Load() != 0 {
		t.Fatalf("send errors = %d, want 0", sendErr.Load())
	}
	if dup.Load() != 0 {
		t.Fatalf("duplicates = %d, want 0 (random_id 应唯一)", dup.Load())
	}
	if okSent != int64(totalMsgs) {
		t.Fatalf("sent ok = %d, want %d", okSent, totalMsgs)
	}
	if !drained {
		t.Fatalf("outbox 未在 %s 内排空，残留积压 %d", drainTimeout, backlog(ctx, pool, ids))
	}
	if metrics.failed.Load() != 0 {
		t.Fatalf("outbox failed = %d, want 0", metrics.failed.Load())
	}

	// 性能 SLO：默认信息化，门禁模式（TELESRV_LOAD_ENFORCE_SLO=1）才硬失败。
	if enforceSLO {
		if sendP99 >= sloSendP99 {
			t.Errorf("send p99 %s >= SLO %s", sendP99, sloSendP99)
		}
		if diffP99 >= sloDiffP99 {
			t.Errorf("getDifference p99 %s >= SLO %s", diffP99, sloDiffP99)
		}
		if throughput < sloThroughput {
			t.Errorf("throughput %.0f msg/s < SLO %.0f msg/s", throughput, sloThroughput)
		}
	}
}

// loadMetrics 实现 rpc.Metrics，统计 outbox claim/deliver/fail。
type loadMetrics struct {
	claimed   atomic.Int64
	delivered atomic.Int64
	failed    atomic.Int64
}

func (m *loadMetrics) MessageSend(time.Duration, bool, error)          {}
func (m *loadMetrics) MessageRateLimited(int)                          {}
func (m *loadMetrics) OutboxClaimed(n int)                             { m.claimed.Add(int64(n)) }
func (m *loadMetrics) OutboxDelivered(time.Duration)                   { m.delivered.Add(1) }
func (m *loadMetrics) OutboxFailed(error)                              { m.failed.Add(1) }
func (m *loadMetrics) PresenceLastSeenBatch(int, time.Duration, error) {}
func (m *loadMetrics) PresenceLastSeenSubmitted()                      {}
func (m *loadMetrics) PresenceLastSeenPending(int)                     {}
func (m *loadMetrics) PresenceLastSeenOverflow()                       {}
func (m *loadMetrics) PresenceLastSeenDrainDropped(int)                {}

func seedUsers(t *testing.T, ctx context.Context, store *postgres.UserStore, n int) []int64 {
	t.Helper()
	salt := randomUint64(t) % 1_000_000
	ids := make([]int64, n)
	errs := make([]error, n)
	sem := make(chan struct{}, 16)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			u, err := store.Create(ctx, domain.User{
				AccessHash: int64(i + 1),
				Phone:      fmt.Sprintf("+1555%06d%05d", salt, i),
				FirstName:  fmt.Sprintf("Load%05d", i),
			})
			if err != nil {
				errs[i] = err
				return
			}
			ids[i] = u.ID
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("create load user %d: %v", i, err)
		}
	}
	return ids
}

// cleanup 按 FK 依赖序删除测试数据：outbox→events→boxes→private_messages→dialogs→users，
// 再清 Redis box_id 计数。message_boxes.from_user_id 为 ON DELETE RESTRICT，必须先删盒子。
// cleanup 在断言之后运行，出错只告警不影响已得结果。
func cleanup(t *testing.T, pool *pgxpool.Pool, rdb *redis.Client, ids []int64) {
	t.Helper()
	ctx := context.Background()
	stmts := []string{
		"DELETE FROM dispatch_outbox WHERE target_user_id = ANY($1::bigint[])",
		"DELETE FROM user_update_events WHERE user_id = ANY($1::bigint[])",
		"DELETE FROM message_boxes WHERE owner_user_id = ANY($1::bigint[])",
		"DELETE FROM private_messages WHERE sender_user_id = ANY($1::bigint[])",
		"DELETE FROM dialogs WHERE user_id = ANY($1::bigint[])",
		"DELETE FROM users WHERE id = ANY($1::bigint[])",
	}
	for _, sql := range stmts {
		if _, err := pool.Exec(ctx, sql, ids); err != nil {
			t.Logf("cleanup %q: %v", sql, err)
		}
	}
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, fmt.Sprintf("counter:box_id:{%d}", id))
	}
	if err := rdb.Del(ctx, keys...).Err(); err != nil {
		t.Logf("cleanup redis counters: %v", err)
	}
}

// backlog 返回测试用户集当前未投递（pending+dispatching）的 outbox 行数。
func backlog(ctx context.Context, pool *pgxpool.Pool, ids []int64) int64 {
	var n int64
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM dispatch_outbox WHERE target_user_id = ANY($1::bigint[]) AND status IN ('pending','dispatching')",
		ids,
	).Scan(&n); err != nil {
		return -1
	}
	return n
}

// waitDrain 轮询 backlog 直到归零或超时，返回是否排空。
func waitDrain(ctx context.Context, pool *pgxpool.Pool, ids []int64, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if backlog(ctx, pool, ids) == 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func sampleGetDifference(t *testing.T, ctx context.Context, events *postgres.UpdateEventStore, ids []int64) []time.Duration {
	t.Helper()
	out := make([]time.Duration, 0, diffSampleGoal)
	for i := 0; len(out) < diffSampleGoal; i++ {
		id := ids[i%len(ids)]
		t0 := time.Now()
		if _, err := events.ListAfter(ctx, id, 0, 100); err != nil {
			t.Fatalf("getDifference ListAfter user %d: %v", id, err)
		}
		out = append(out, time.Since(t0))
	}
	return out
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func pass(ok bool) string {
	if ok {
		return "PASS"
	}
	return "WARN"
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func randomUint64(t *testing.T) uint64 {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return binary.LittleEndian.Uint64(b[:])
}
