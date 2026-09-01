package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"telesrv/internal/domain"
	"telesrv/internal/store"
	"telesrv/internal/store/postgres/sqlcgen"
)

const (
	plainPrivateSendBatchWorkers        = 8
	plainPrivateSendBatchMaxTasks       = 32
	plainPrivateSendBatchMinTasks       = 8
	plainPrivateSendBatchMailbox        = 8192
	plainPrivateSendBatchFlushInterval  = 8 * time.Millisecond
	plainPrivateSendBatchTimeout        = 10 * time.Second
	plainPrivateSendBatchMaxQueuedBytes = int64(64 << 20)
	plainPrivateSendTaskFixedBytes      = int64(1024)
)

var errPlainPrivateSendBatchOverloaded = errors.New("postgres: plain private send batch actor overloaded")

var processPlainPrivateSendBatcher = newPlainPrivateSendBatchActor()

type plainPrivateSendBatchTask struct {
	ctx         context.Context
	store       *MessageStore
	req         domain.SendPrivateTextRequest
	fingerprint []byte
	bytes       int64
	done        chan plainPrivateSendBatchResult
}

type plainPrivateSendBatchResult struct {
	result domain.SendPrivateTextResult
	err    error
}

type plainPrivateSendBatchCompletion struct {
	tasks   []*plainPrivateSendBatchTask
	results []plainPrivateSendBatchResult
}

type plainPrivateSendScope struct {
	store  *MessageStore
	userID int64
}

type plainPrivateSendBatchActor struct {
	submit      chan *plainPrivateSendBatchTask
	work        chan []*plainPrivateSendBatchTask
	completed   chan plainPrivateSendBatchCompletion
	queuedBytes atomic.Int64
	batches     atomic.Uint64
	tasks       atomic.Uint64
}

type plainPrivateSendBatchSnapshot struct {
	Batches uint64
	Tasks   uint64
}

func newPlainPrivateSendBatchActor() *plainPrivateSendBatchActor {
	a := &plainPrivateSendBatchActor{
		submit:    make(chan *plainPrivateSendBatchTask, plainPrivateSendBatchMailbox),
		work:      make(chan []*plainPrivateSendBatchTask, plainPrivateSendBatchWorkers),
		completed: make(chan plainPrivateSendBatchCompletion, plainPrivateSendBatchWorkers),
	}
	for range plainPrivateSendBatchWorkers {
		go a.worker()
	}
	go a.run()
	return a
}

func (a *plainPrivateSendBatchActor) Snapshot() plainPrivateSendBatchSnapshot {
	if a == nil {
		return plainPrivateSendBatchSnapshot{}
	}
	return plainPrivateSendBatchSnapshot{Batches: a.batches.Load(), Tasks: a.tasks.Load()}
}

func (a *plainPrivateSendBatchActor) Eligible(messageStore *MessageStore) bool {
	if a == nil || messageStore == nil || messageStore.boxIDs == nil {
		return false
	}
	if _, ok := messageStore.db.(*pgxpool.Pool); !ok {
		return false
	}
	// The process batch path is a distributed allocator + PostgreSQL boundary.
	// Local and test allocators cannot reserve gap-safe ids before the
	// transaction under cross-process concurrency and therefore remain on the
	// single-command transaction path.
	_, distributed := messageStore.boxIDs.(store.DistributedBoxIDAllocator)
	return distributed
}

func (a *plainPrivateSendBatchActor) Submit(
	ctx context.Context,
	messageStore *MessageStore,
	req domain.SendPrivateTextRequest,
	fingerprint []byte,
) (domain.SendPrivateTextResult, error) {
	if !a.Eligible(messageStore) || ctx == nil || len(fingerprint) == 0 {
		return domain.SendPrivateTextResult{}, fmt.Errorf("postgres: invalid plain private send batch submission")
	}
	retained := plainPrivateSendTaskFixedBytes + int64(len(req.Message)+len(fingerprint))
	if !reservePlainPrivateSendBatchBytes(&a.queuedBytes, retained) {
		return domain.SendPrivateTextResult{}, errPlainPrivateSendBatchOverloaded
	}
	task := &plainPrivateSendBatchTask{
		ctx: ctx, store: messageStore, req: req,
		fingerprint: append([]byte(nil), fingerprint...),
		bytes:       retained,
		done:        make(chan plainPrivateSendBatchResult, 1),
	}
	select {
	case a.submit <- task:
	case <-ctx.Done():
		a.queuedBytes.Add(-retained)
		return domain.SendPrivateTextResult{}, ctx.Err()
	}
	select {
	case result := <-task.done:
		return result.result, result.err
	case <-ctx.Done():
		// Once accepted, the bounded actor may commit after the caller stops
		// waiting. random_id is the durable receipt for an exact replay.
		return domain.SendPrivateTextResult{}, ctx.Err()
	}
}

func reservePlainPrivateSendBatchBytes(used *atomic.Int64, amount int64) bool {
	if used == nil || amount <= 0 || amount > plainPrivateSendBatchMaxQueuedBytes {
		return false
	}
	for {
		current := used.Load()
		if current > plainPrivateSendBatchMaxQueuedBytes-amount {
			return false
		}
		if used.CompareAndSwap(current, current+amount) {
			return true
		}
	}
}

func (a *plainPrivateSendBatchActor) run() {
	ticker := time.NewTicker(plainPrivateSendBatchFlushInterval)
	defer ticker.Stop()
	pending := make([]*plainPrivateSendBatchTask, 0, plainPrivateSendBatchMailbox)
	busy := make(map[plainPrivateSendScope]struct{})
	available := plainPrivateSendBatchWorkers

	completeCanceled := func(task *plainPrivateSendBatchTask) {
		err := context.Canceled
		if task.ctx != nil && task.ctx.Err() != nil {
			err = task.ctx.Err()
		}
		task.done <- plainPrivateSendBatchResult{err: err}
		a.queuedBytes.Add(-task.bytes)
	}
	dispatch := func(flush bool) {
		for available > 0 && len(pending) > 0 {
			if !flush && len(pending) < plainPrivateSendBatchMinTasks {
				return
			}
			batch, remaining, canceled := selectPlainPrivateSendBatch(pending, busy)
			pending = remaining
			for _, task := range canceled {
				completeCanceled(task)
			}
			if len(batch) == 0 {
				return
			}
			for _, task := range batch {
				for _, userID := range plainPrivateSendTaskUsers(task) {
					busy[plainPrivateSendScope{store: task.store, userID: userID}] = struct{}{}
				}
			}
			available--
			a.work <- batch
		}
	}

	for {
		select {
		case task := <-a.submit:
			pending = append(pending, task)
			dispatch(false)
		case completion := <-a.completed:
			available++
			for i, task := range completion.tasks {
				for _, userID := range plainPrivateSendTaskUsers(task) {
					delete(busy, plainPrivateSendScope{store: task.store, userID: userID})
				}
				result := plainPrivateSendBatchResult{err: errors.New("postgres: missing plain private send batch result")}
				if i < len(completion.results) {
					result = completion.results[i]
				}
				task.done <- result
				a.queuedBytes.Add(-task.bytes)
			}
			dispatch(false)
		case <-ticker.C:
			dispatch(true)
		}
	}
}

func selectPlainPrivateSendBatch(
	pending []*plainPrivateSendBatchTask,
	busy map[plainPrivateSendScope]struct{},
) (batch, remaining, canceled []*plainPrivateSendBatchTask) {
	selected := make(map[plainPrivateSendScope]struct{}, plainPrivateSendBatchMaxTasks*2)
	blockedByOlder := make(map[plainPrivateSendScope]struct{}, plainPrivateSendBatchMaxTasks*2)
	var selectedStore *MessageStore
	remaining = make([]*plainPrivateSendBatchTask, 0, len(pending))
	for _, task := range pending {
		if task == nil || task.ctx == nil || task.ctx.Err() != nil {
			if task != nil {
				canceled = append(canceled, task)
			}
			continue
		}
		users := plainPrivateSendTaskUsers(task)
		blocked := selectedStore != nil && task.store != selectedStore
		for _, userID := range users {
			key := plainPrivateSendScope{store: task.store, userID: userID}
			if _, ok := busy[key]; ok {
				blocked = true
			}
			if _, ok := blockedByOlder[key]; ok {
				blocked = true
			}
			if _, ok := selected[key]; ok {
				blocked = true
			}
		}
		if blocked || len(batch) >= plainPrivateSendBatchMaxTasks {
			remaining = append(remaining, task)
			for _, userID := range users {
				blockedByOlder[plainPrivateSendScope{store: task.store, userID: userID}] = struct{}{}
			}
			continue
		}
		if selectedStore == nil {
			selectedStore = task.store
		}
		batch = append(batch, task)
		for _, userID := range users {
			selected[plainPrivateSendScope{store: task.store, userID: userID}] = struct{}{}
		}
	}
	return batch, remaining, canceled
}

func plainPrivateSendTaskUsers(task *plainPrivateSendBatchTask) []int64 {
	if task == nil {
		return nil
	}
	return normalizedUserLaneIDs([]int64{task.req.SenderUserID, task.req.RecipientUserID})
}

func (a *plainPrivateSendBatchActor) worker() {
	for batch := range a.work {
		results := executePlainPrivateSendBatch(batch)
		a.batches.Add(1)
		a.tasks.Add(uint64(len(batch)))
		a.completed <- plainPrivateSendBatchCompletion{tasks: batch, results: results}
	}
}

func executePlainPrivateSendBatch(tasks []*plainPrivateSendBatchTask) []plainPrivateSendBatchResult {
	results := make([]plainPrivateSendBatchResult, len(tasks))
	if len(tasks) == 0 || tasks[0] == nil || tasks[0].store == nil {
		return plainPrivateSendBatchErrorResults(results, errors.New("postgres: empty plain private send batch"))
	}
	messageStore := tasks[0].store
	pool, ok := messageStore.db.(*pgxpool.Pool)
	if !ok {
		return plainPrivateSendBatchErrorResults(results, errors.New("postgres: plain private send batch requires pgx pool"))
	}
	allocationUsers := make([]int64, 0, len(tasks)*2)
	lockUsers := make([]int64, 0, len(tasks)*2)
	seen := make(map[int64]struct{}, len(tasks)*2)
	for _, task := range tasks {
		if task == nil || task.store != messageStore {
			return plainPrivateSendBatchErrorResults(results, errors.New("postgres: mixed message stores in plain private send batch"))
		}
		for _, userID := range plainPrivateSendTaskUsers(task) {
			if _, exists := seen[userID]; exists {
				return plainPrivateSendBatchErrorResults(results, fmt.Errorf("postgres: overlapping user %d in plain private send batch", userID))
			}
			seen[userID] = struct{}{}
		}
		lockUsers = append(lockUsers, task.req.SenderUserID, task.req.RecipientUserID)
		allocationUsers = append(allocationUsers, task.req.SenderUserID)
		if task.req.SenderUserID != task.req.RecipientUserID && !task.req.RecipientBlocked {
			allocationUsers = append(allocationUsers, task.req.RecipientUserID)
		}
	}
	lockUsers = normalizedUserLaneIDs(lockUsers)
	allocationUsers = normalizedUserLaneIDs(allocationUsers)
	parent := context.Background()
	if tasks[0].ctx != nil {
		parent = context.WithoutCancel(tasks[0].ctx)
	}
	ctx, cancel := context.WithTimeout(parent, plainPrivateSendBatchTimeout)
	defer cancel()

	boxIDs, err := messageStore.boxIDs.NextBoxIDs(ctx, allocationUsers)
	if err != nil {
		return plainPrivateSendBatchErrorResults(results, fmt.Errorf("allocate plain private send batch box ids: %w", err))
	}
	releaseLanes, err := messageStore.privateSendLanes.acquire(ctx, lockUsers...)
	if err != nil {
		return plainPrivateSendBatchErrorResults(results, fmt.Errorf("admit plain private send batch: %w", err))
	}
	defer releaseLanes()

	tx, err := pool.Begin(ctx)
	if err != nil {
		return plainPrivateSendBatchErrorResults(results, fmt.Errorf("begin plain private send batch: %w", err))
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()
	if err := lockUsersForUpdate(ctx, tx, lockUsers...); err != nil {
		return plainPrivateSendBatchErrorResults(results, fmt.Errorf("lock plain private send batch users: %w", err))
	}
	if err := lockDispatchOutboxAppendFences(ctx, tx, allocationUsers); err != nil {
		return plainPrivateSendBatchErrorResults(results, err)
	}
	created, err := createPlainPrivateMessageBatch(ctx, tx, tasks)
	if err != nil {
		return plainPrivateSendBatchErrorResults(results, err)
	}
	qtx := sqlcgen.New(tx)
	for i, task := range tasks {
		if created[i].inserted {
			continue
		}
		duplicate, found, duplicateErr := messageStore.duplicateSendResult(ctx, qtx, task.req, task.fingerprint)
		if duplicateErr != nil {
			return plainPrivateSendBatchErrorResults(results, duplicateErr)
		}
		if !found {
			return plainPrivateSendBatchErrorResults(results, errors.New("duplicate batched private message disappeared after unique conflict"))
		}
		duplicate.Duplicate = true
		results[i].result = duplicate
	}
	projections, err := persistPlainPrivateSendProjectionBatch(ctx, tx, tasks, created, boxIDs)
	if err != nil {
		return plainPrivateSendBatchErrorResults(results, err)
	}
	for i := range tasks {
		if !created[i].inserted {
			continue
		}
		projection, ok := projections[i]
		if !ok {
			return plainPrivateSendBatchErrorResults(results, fmt.Errorf("missing plain private send batch projection %d", i))
		}
		results[i].result = domain.SendPrivateTextResult{
			SenderMessage: projection.Sender, RecipientMessage: projection.Recipient,
			SenderEvent: eventFromMessage(projection.Sender), RecipientEvent: eventFromMessage(projection.Recipient),
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return plainPrivateSendBatchErrorResults(results, fmt.Errorf("commit plain private send batch: %w", err))
	}
	committed = true
	return results
}

func lockDispatchOutboxAppendFences(ctx context.Context, tx pgx.Tx, userIDs []int64) error {
	userIDs = normalizedUserLaneIDs(userIDs)
	if len(userIDs) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
SELECT pg_advisory_xact_lock_shared(dispatch_outbox_lane_advisory_key(streams.target_user_id))
FROM unnest($1::bigint[]) AS streams(target_user_id)
ORDER BY streams.target_user_id`, userIDs); err != nil {
		return fmt.Errorf("lock dispatch outbox append fences: %w", err)
	}
	return nil
}

func plainPrivateSendBatchErrorResults(results []plainPrivateSendBatchResult, err error) []plainPrivateSendBatchResult {
	for i := range results {
		results[i] = plainPrivateSendBatchResult{err: err}
	}
	return results
}
