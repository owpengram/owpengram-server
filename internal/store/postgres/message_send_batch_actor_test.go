package postgres

import (
	"context"
	"errors"
	"testing"

	"telesrv/internal/domain"
	"telesrv/internal/observability/dbtrace"
	storepkg "telesrv/internal/store"
)

func TestSelectPlainPrivateSendBatchPreservesConflictingFIFOAndCombinesDisjointScopes(t *testing.T) {
	messageStore := &MessageStore{}
	task := func(sender, recipient int64) *plainPrivateSendBatchTask {
		return &plainPrivateSendBatchTask{
			ctx: context.Background(), store: messageStore,
			req: domain.SendPrivateTextRequest{SenderUserID: sender, RecipientUserID: recipient},
		}
	}
	first := task(1, 2)
	blockedFollower := task(2, 3)
	disjoint := task(4, 5)
	batch, remaining, canceled := selectPlainPrivateSendBatch(
		[]*plainPrivateSendBatchTask{first, blockedFollower, disjoint},
		map[plainPrivateSendScope]struct{}{},
	)
	if len(canceled) != 0 || len(batch) != 2 || batch[0] != first || batch[1] != disjoint ||
		len(remaining) != 1 || remaining[0] != blockedFollower {
		t.Fatalf("batch=%v remaining=%v canceled=%d", batch, remaining, len(canceled))
	}

	busy := map[plainPrivateSendScope]struct{}{{store: messageStore, userID: 1}: {}}
	batch, remaining, canceled = selectPlainPrivateSendBatch(
		[]*plainPrivateSendBatchTask{first, blockedFollower, disjoint}, busy,
	)
	if len(canceled) != 0 || len(batch) != 1 || batch[0] != disjoint ||
		len(remaining) != 2 || remaining[0] != first || remaining[1] != blockedFollower {
		t.Fatalf("busy batch=%v remaining=%v canceled=%d", batch, remaining, len(canceled))
	}
}

func TestPlainPrivateSendBatchEligibilityRejectsLocalAllocator(t *testing.T) {
	pool := testPool(t)
	messages := NewMessageStore(pool, WithMessageAllocators(&perUserCounterAllocator{}))
	if processPlainPrivateSendBatcher.Eligible(messages) {
		t.Fatal("local allocator entered distributed private-send batch path")
	}
}

func TestExecutePlainPrivateSendBatchCommitsDisjointMessagesTogetherPostgres(t *testing.T) {
	pool := testPool(t)
	baseCtx := context.Background()
	users := NewUserStore(pool)
	suffix := randomSuffix(t)
	const count = 12
	authKeyID := [8]byte{0xfe, 0xdc, 0xba, 0x98, 0x76, 0x54, 0x32, 0x80}
	createdUsers := make([]int64, 0, count*2)
	requests := make([]domain.SendPrivateTextRequest, 0, count)
	for i := range count {
		sender := createTestUser(t, baseCtx, users, "+1671"+suffix+batchTestSuffix(i, 0), "BatchSender", "")
		recipient := createTestUser(t, baseCtx, users, "+1671"+suffix+batchTestSuffix(i, 1), "BatchRecipient", "")
		createdUsers = append(createdUsers, sender.ID, recipient.ID)
		request := domain.SendPrivateTextRequest{
			SenderUserID: sender.ID, RecipientUserID: recipient.ID,
			RandomID: int64(2508251000 + i), Message: "batched private send", Date: 1800001000 + i,
			IdempotencyPreflighted: true,
		}
		if i == 0 {
			request.OriginAuthKeyID = authKeyID
			request.OriginSessionID = 250825
		}
		requests = append(requests, request)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(baseCtx, "DELETE FROM users WHERE id = ANY($1::bigint[])", createdUsers)
	})
	messageStore := NewMessageStore(pool, WithMessageAllocators(&perUserCounterAllocator{}))
	ctx, stats := dbtrace.WithStats(baseCtx)
	tasks := plainPrivateSendTestTasks(t, ctx, messageStore, requests)
	results := executePlainPrivateSendBatch(tasks)
	for i, result := range results {
		if result.err != nil {
			t.Fatalf("result %d: %v", i, result.err)
		}
		if result.result.Duplicate || result.result.SenderMessage.Pts != 1 || result.result.RecipientMessage.Pts != 1 {
			t.Fatalf("result %d = %+v", i, result.result)
		}
	}
	// BEGIN + ordered account lock + ordered append fence + set-based logical
	// insert + set-based durable projection + COMMIT. Width does not change it.
	const wantQueries = int64(6)
	if snapshot := stats.Snapshot(); snapshot.Queries != wantQueries || snapshot.Errors != 0 {
		t.Fatalf("query stats=%+v want queries=%d", snapshot, wantQueries)
	}
	var boxes, events, outbox int
	if err := pool.QueryRow(baseCtx, `
SELECT
  (SELECT count(*) FROM message_boxes WHERE owner_user_id = ANY($1::bigint[])),
  (SELECT count(*) FROM user_update_events WHERE user_id = ANY($1::bigint[])),
  (SELECT count(*) FROM dispatch_outbox WHERE target_user_id = ANY($1::bigint[]))`, createdUsers).Scan(&boxes, &events, &outbox); err != nil {
		t.Fatal(err)
	}
	if boxes != count*2 || events != count*2 || outbox != count*2 {
		t.Fatalf("durable rows boxes/events/outbox=%d/%d/%d want=%d", boxes, events, outbox, count*2)
	}
	var excludeAuthKeyID, excludeSessionID int64
	if err := pool.QueryRow(baseCtx, `
SELECT exclude_auth_key_id, exclude_session_id
FROM dispatch_outbox
WHERE target_user_id=$1`, requests[0].SenderUserID).Scan(&excludeAuthKeyID, &excludeSessionID); err != nil {
		t.Fatalf("load batched sender exclusion: %v", err)
	}
	if excludeAuthKeyID != authKeyIDToInt64(authKeyID) || excludeSessionID != 250825 {
		t.Fatalf("batched sender exclusion=%d/%d want=%d/250825", excludeAuthKeyID, excludeSessionID, authKeyIDToInt64(authKeyID))
	}
}

func TestExecutePlainPrivateSendBatchPreservesBlockedSelfAndReplayPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	users := NewUserStore(pool)
	suffix := randomSuffix(t)
	createdUsers := make([]int64, 0, 5)
	makeUser := func(index int) int64 {
		user := createTestUser(t, ctx, users, "+1672"+suffix+batchTestSuffix(index, 0), "BatchMode", "")
		createdUsers = append(createdUsers, user.ID)
		return user.ID
	}
	normalSender, normalRecipient := makeUser(1), makeUser(2)
	blockedSender, blockedRecipient := makeUser(3), makeUser(4)
	selfUser := makeUser(5)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1::bigint[])", createdUsers)
	})
	messages := NewMessageStore(pool, WithMessageAllocators(&perUserCounterAllocator{}))
	requests := []domain.SendPrivateTextRequest{
		{SenderUserID: normalSender, RecipientUserID: normalRecipient, RandomID: 2508252001, Message: "normal", Date: 1800002001, IdempotencyPreflighted: true},
		{SenderUserID: blockedSender, RecipientUserID: blockedRecipient, RandomID: 2508252002, Message: "blocked", Date: 1800002002, RecipientBlocked: true, IdempotencyPreflighted: true},
		{SenderUserID: selfUser, RecipientUserID: selfUser, RandomID: 2508252003, Message: "self", Date: 1800002003, IdempotencyPreflighted: true},
	}
	results := executePlainPrivateSendBatch(plainPrivateSendTestTasks(t, ctx, messages, requests))
	for i, result := range results {
		if result.err != nil || result.result.Duplicate {
			t.Fatalf("first result %d = %+v error=%v", i, result.result, result.err)
		}
	}
	if results[1].result.RecipientMessage.ID != 0 || results[1].result.RecipientEvent.Pts != 0 {
		t.Fatalf("blocked recipient leaked durable projection: %+v", results[1].result)
	}
	if results[2].result.RecipientMessage.ID != results[2].result.SenderMessage.ID ||
		results[2].result.RecipientMessage.Pts != results[2].result.SenderMessage.Pts {
		t.Fatalf("self projection differs sender=%+v recipient=%+v", results[2].result.SenderMessage, results[2].result.RecipientMessage)
	}
	replay := executePlainPrivateSendBatch(plainPrivateSendTestTasks(t, ctx, messages, requests[:1]))
	if len(replay) != 1 || replay[0].err != nil || !replay[0].result.Duplicate ||
		replay[0].result.SenderMessage.ID != results[0].result.SenderMessage.ID ||
		replay[0].result.SenderMessage.Pts != results[0].result.SenderMessage.Pts {
		t.Fatalf("replay=%+v first=%+v", replay, results[0])
	}
}

func TestExecutePlainPrivateSendBatchRollsBackEveryTaskOnProjectionFailurePostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	users := NewUserStore(pool)
	suffix := randomSuffix(t)
	createdUsers := make([]int64, 0, 4)
	makeUser := func(index int) int64 {
		user := createTestUser(t, ctx, users, "+1673"+suffix+batchTestSuffix(index, 0), "BatchRollback", "")
		createdUsers = append(createdUsers, user.ID)
		return user.ID
	}
	firstSender, firstRecipient := makeUser(1), makeUser(2)
	badSender, badRecipient := makeUser(3), makeUser(4)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1::bigint[])", createdUsers)
	})
	messages := NewMessageStore(pool, WithMessageAllocators(&perUserCounterAllocator{}))
	requests := []domain.SendPrivateTextRequest{
		{SenderUserID: firstSender, RecipientUserID: firstRecipient, RandomID: 2508253001, Message: "must roll back", Date: 1800003001, IdempotencyPreflighted: true},
		{
			SenderUserID: badSender, RecipientUserID: badRecipient, RandomID: 2508253002,
			Message: "invalid exclusion", Date: 1800003002, IdempotencyPreflighted: true,
			OriginAuthKeyID: [8]byte{1},
		},
	}
	results := executePlainPrivateSendBatch(plainPrivateSendTestTasks(t, ctx, messages, requests))
	if len(results) != len(requests) {
		t.Fatalf("results=%d want=%d", len(results), len(requests))
	}
	for i, result := range results {
		if !errors.Is(result.err, errInvalidDispatchOutboxExclusionPair) {
			t.Fatalf("result %d error=%v want=%v", i, result.err, errInvalidDispatchOutboxExclusionPair)
		}
	}
	var logical, boxes, events, outbox int
	if err := pool.QueryRow(ctx, `
SELECT
  (SELECT count(*) FROM private_messages WHERE sender_user_id = ANY($1::bigint[])),
  (SELECT count(*) FROM message_boxes WHERE owner_user_id = ANY($1::bigint[])),
  (SELECT count(*) FROM user_update_events WHERE user_id = ANY($1::bigint[])),
  (SELECT count(*) FROM dispatch_outbox WHERE target_user_id = ANY($1::bigint[]))`, createdUsers).
		Scan(&logical, &boxes, &events, &outbox); err != nil {
		t.Fatal(err)
	}
	if logical != 0 || boxes != 0 || events != 0 || outbox != 0 {
		t.Fatalf("partial batch commit logical/boxes/events/outbox=%d/%d/%d/%d", logical, boxes, events, outbox)
	}
}

func plainPrivateSendTestTasks(
	t *testing.T,
	ctx context.Context,
	messages *MessageStore,
	requests []domain.SendPrivateTextRequest,
) []*plainPrivateSendBatchTask {
	t.Helper()
	tasks := make([]*plainPrivateSendBatchTask, len(requests))
	for i, req := range requests {
		fingerprint, err := storepkg.PrivateSendFingerprint(req)
		if err != nil {
			t.Fatal(err)
		}
		tasks[i] = &plainPrivateSendBatchTask{ctx: ctx, store: messages, req: req, fingerprint: fingerprint}
	}
	return tasks
}

func batchTestSuffix(index, side int) string {
	return string([]byte{
		byte('0' + (index/10)%10),
		byte('0' + index%10),
		byte('0' + side),
	})
}
