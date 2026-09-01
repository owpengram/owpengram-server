package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"telesrv/internal/domain"
)

func TestWelcomeMessageStoreDurabilityIdempotencyConcurrencyAndNoPTS(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)
	users := NewUserStore(pool)
	owner := createTestUser(t, ctx, users, "+1779"+suffix+"01", "WelcomeOwner", "")
	channels := NewChannelStore(pool)
	createdChannel, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID, Title: "Welcome " + suffix, Megagroup: true, Date: 1700000000,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	peer := domain.Peer{Type: domain.PeerTypeChannel, ID: createdChannel.Channel.ID}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id = $1", peer.ID)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", owner.ID)
	})

	countsBefore := welcomeSideEffectCounts(t, ctx, pool, owner.ID, peer.ID)
	store := NewWelcomeMessageStore(pool)
	initial, err := store.ListWelcomeMessages(ctx, peer, 0)
	if err != nil || initial.Hash != domain.InitialWelcomeRevision || initial.NotModified || len(initial.Messages) != 0 {
		t.Fatalf("initial list = %+v err=%v", initial, err)
	}
	if same, err := store.ListWelcomeMessages(ctx, peer, initial.Hash); err != nil || !same.NotModified {
		t.Fatalf("initial not-modified = %+v err=%v", same, err)
	}

	makeRequest := func(randomID int64, text string) domain.CreateWelcomeMessageRequest {
		content := domain.WelcomeMessageContent{Message: text}
		fingerprint, err := domain.WelcomeCreateFingerprint(peer, owner.ID, randomID, content)
		if err != nil {
			t.Fatal(err)
		}
		return domain.CreateWelcomeMessageRequest{
			Peer: peer, CreatorUserID: owner.ID, Date: 1700000001,
			RandomID: randomID, Content: content, CreateFingerprint: fingerprint,
		}
	}
	firstRequest := makeRequest(5001, "first")
	first, fresh, err := store.CreateWelcomeMessage(ctx, firstRequest)
	if err != nil || !fresh || first.ID != 1 || first.Version != 1 {
		t.Fatalf("first create = %+v fresh=%v err=%v", first, fresh, err)
	}
	replayed, fresh, err := NewWelcomeMessageStore(pool).CreateWelcomeMessage(ctx, firstRequest)
	if err != nil || fresh || replayed.ID != first.ID {
		t.Fatalf("restart replay = %+v fresh=%v err=%v", replayed, fresh, err)
	}
	conflict := makeRequest(firstRequest.RandomID, "conflict")
	if _, _, err := store.CreateWelcomeMessage(ctx, conflict); !errors.Is(err, domain.ErrWelcomeMessageRandomIDConflict) {
		t.Fatalf("conflicting replay err=%v", err)
	}

	edited, err := store.EditWelcomeMessage(ctx, domain.EditWelcomeMessageRequest{
		Peer: peer, ID: first.ID, EditDate: 1700000002,
		Fields: domain.WelcomeMessageEditFields{SetMessage: true, Message: "edited", SetEntities: true},
	})
	if err != nil || edited.Content.Message != "edited" || edited.Version != 2 || edited.EditDate != 1700000002 {
		t.Fatalf("edit = %+v err=%v", edited, err)
	}
	replayed, fresh, err = store.CreateWelcomeMessage(ctx, firstRequest)
	if err != nil || fresh || replayed.Content.Message != "edited" || replayed.Version != 2 {
		t.Fatalf("create replay after edit = %+v fresh=%v err=%v", replayed, fresh, err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	ids := []int{first.ID}
	limitErrors := 0
	unexpected := []error{}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			message, _, err := NewWelcomeMessageStore(pool).CreateWelcomeMessage(ctx, makeRequest(int64(6000+index), fmt.Sprintf("parallel-%d", index)))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ids = append(ids, message.ID)
			case errors.Is(err, domain.ErrWelcomeMessageLimit):
				limitErrors++
			default:
				unexpected = append(unexpected, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	sort.Ints(ids)
	if len(unexpected) != 0 || len(ids) != domain.MaxWelcomeMessagesPerPeer || limitErrors != 4 {
		t.Fatalf("parallel ids=%v limitErrors=%d unexpected=%v", ids, limitErrors, unexpected)
	}
	for index, id := range ids {
		if id != index+1 {
			t.Fatalf("parallel ids=%v, want monotonic 1..5", ids)
		}
	}

	afterRestart, err := NewWelcomeMessageStore(pool).ListWelcomeMessages(ctx, peer, 0)
	if err != nil || len(afterRestart.Messages) != domain.MaxWelcomeMessagesPerPeer || afterRestart.Hash <= initial.Hash {
		t.Fatalf("restart list = hash:%d messages:%d err=%v", afterRestart.Hash, len(afterRestart.Messages), err)
	}
	if has, err := store.HasWelcomeMessages(ctx, peer); err != nil || !has {
		t.Fatalf("has welcome messages=%v,%v", has, err)
	}
	if ok, err := store.DeleteWelcomeMessage(ctx, peer, first.ID); err != nil || !ok {
		t.Fatalf("delete=%v,%v", ok, err)
	}
	if ok, err := store.DeleteWelcomeMessage(ctx, peer, first.ID); err != nil || !ok {
		t.Fatalf("idempotent delete=%v,%v", ok, err)
	}
	if _, err := store.DeleteWelcomeMessage(ctx, peer, 99); !errors.Is(err, domain.ErrWelcomeMessageNotFound) {
		t.Fatalf("future delete err=%v", err)
	}
	if ok, err := store.DeleteAllWelcomeMessages(ctx, peer); err != nil || !ok {
		t.Fatalf("delete all=%v,%v", ok, err)
	}
	empty, err := store.ListWelcomeMessages(ctx, peer, 0)
	if err != nil || len(empty.Messages) != 0 {
		t.Fatalf("empty after delete all=%+v err=%v", empty, err)
	}
	if ok, err := store.DeleteAllWelcomeMessages(ctx, peer); err != nil || !ok {
		t.Fatalf("idempotent delete all=%v,%v", ok, err)
	}
	if has, err := store.HasWelcomeMessages(ctx, peer); err != nil || has {
		t.Fatalf("has after delete all=%v,%v", has, err)
	}
	if got := welcomeSideEffectCounts(t, ctx, pool, owner.ID, peer.ID); got != countsBefore {
		t.Fatalf("welcome mutations changed PTS/event/outbox counts: before=%+v after=%+v", countsBefore, got)
	}
}

func TestWelcomeMessageJoinDeliveryIsTransactionalLeasedAndTTLBounded(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)
	users := NewUserStore(pool)
	owner := createTestUser(t, ctx, users, "+1778"+suffix+"01", "WelcomeOwner", "")
	member := createTestUser(t, ctx, users, "+1778"+suffix+"02", "WelcomeMember", "")
	secondMember := createTestUser(t, ctx, users, "+1778"+suffix+"03", "WelcomeSecond", "")
	channels := NewChannelStore(pool)
	created, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID, Title: "Welcome Delivery " + suffix, Megagroup: true, Date: 1700000100,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channelID := created.Channel.ID
	peer := domain.Peer{Type: domain.PeerTypeChannel, ID: channelID}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id = $1", channelID)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1)", []int64{owner.ID, member.ID, secondMember.ID})
	})

	welcomeStore := NewWelcomeMessageStore(pool)
	contents := []domain.WelcomeMessageContent{{Message: "hello new member"}, {Message: "read the rules"}}
	for index, content := range contents {
		randomID := int64(9001 + index)
		fingerprint, err := domain.WelcomeCreateFingerprint(peer, owner.ID, randomID, content)
		if err != nil {
			t.Fatal(err)
		}
		if _, fresh, err := welcomeStore.CreateWelcomeMessage(ctx, domain.CreateWelcomeMessageRequest{
			Peer: peer, CreatorUserID: owner.ID, Date: 1700000101, RandomID: randomID,
			Content: content, CreateFingerprint: fingerprint,
		}); err != nil || !fresh {
			t.Fatalf("create welcome template %d fresh=%v err=%v", index, fresh, err)
		}
	}

	firstJoin := 1700000102
	if _, err := channels.InviteToChannel(ctx, channelID, owner.ID, []int64{member.ID, secondMember.ID}, firstJoin); err != nil {
		t.Fatalf("batch invite members: %v", err)
	}
	var batchRows, batchUsers, batchEvents int
	if err := pool.QueryRow(ctx, `
SELECT count(*), count(DISTINCT target_user_id), count(DISTINCT join_event_id)
FROM welcome_message_deliveries
WHERE channel_id=$1 AND target_user_id=ANY($2::bigint[])`, channelID, []int64{member.ID, secondMember.ID}).
		Scan(&batchRows, &batchUsers, &batchEvents); err != nil {
		t.Fatal(err)
	}
	if batchRows != len(contents)*2 || batchUsers != 2 || batchEvents != 2 {
		t.Fatalf("batch rows=%d users=%d events=%d", batchRows, batchUsers, batchEvents)
	}
	if _, err := channels.EditChannelBanned(ctx, domain.EditChannelBannedRequest{
		UserID: owner.ID, ChannelID: channelID,
		Participant:  domain.Peer{Type: domain.PeerTypeUser, ID: secondMember.ID},
		BannedRights: domain.ChannelBannedRights{ViewMessages: true}, Date: firstJoin + 1,
	}); err != nil {
		t.Fatalf("kick second member: %v", err)
	}
	var secondAfterLeave int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM welcome_message_deliveries WHERE channel_id=$1 AND target_user_id=$2`, channelID, secondMember.ID).Scan(&secondAfterLeave); err != nil || secondAfterLeave != 0 {
		t.Fatalf("second member deliveries after kick=%d err=%v", secondAfterLeave, err)
	}
	var firstEvent int64
	var firstJoined int
	var expiresInSeconds int64
	var firstTemplateCount int
	if err := pool.QueryRow(ctx, `
SELECT min(join_event_id), min(joined_at), min(EXTRACT(EPOCH FROM (expires_at - created_at)))::bigint, count(*)
FROM welcome_message_deliveries
WHERE channel_id=$1 AND target_user_id=$2 AND delivered_at IS NULL`, channelID, member.ID).
		Scan(&firstEvent, &firstJoined, &expiresInSeconds, &firstTemplateCount); err != nil {
		t.Fatalf("read first delivery: %v", err)
	}
	if firstEvent <= 0 || firstJoined != firstJoin || firstTemplateCount != len(contents) || expiresInSeconds != int64(domain.WelcomeMessageDeliveryTTL/time.Second) {
		t.Fatalf("first delivery event=%d joined=%d templates=%d ttl_seconds=%d", firstEvent, firstJoined, firstTemplateCount, expiresInSeconds)
	}

	// A waiter must take its DELETE snapshot only after the previous activation
	// commits; otherwise two concurrent epochs could survive the delete+insert pair.
	tx1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	firstConcurrent := domain.ChannelMember{ChannelID: channelID, UserID: member.ID, Status: domain.ChannelMemberActive, JoinedAt: firstJoin + 10}
	if err := enqueueWelcomeMessageDeliveriesTx(ctx, tx1, channelID, []domain.ChannelMember{firstConcurrent}); err != nil {
		_ = tx1.Rollback(ctx)
		t.Fatal(err)
	}
	pidReady := make(chan int)
	concurrentDone := make(chan error, 1)
	go func() {
		tx2, err := pool.Begin(ctx)
		if err != nil {
			pidReady <- 0
			concurrentDone <- err
			return
		}
		defer tx2.Rollback(ctx) // no-op after Commit
		var pid int
		if err := tx2.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
			pidReady <- 0
			concurrentDone <- err
			return
		}
		pidReady <- pid
		secondConcurrent := firstConcurrent
		secondConcurrent.JoinedAt = firstJoin + 20
		if err := enqueueWelcomeMessageDeliveriesTx(ctx, tx2, channelID, []domain.ChannelMember{secondConcurrent}); err != nil {
			concurrentDone <- err
			return
		}
		concurrentDone <- tx2.Commit(ctx)
	}()
	waitingPID := <-pidReady
	if waitingPID == 0 {
		_ = tx1.Rollback(ctx)
		t.Fatal(<-concurrentDone)
	}
	waitDeadline := time.Now().Add(2 * time.Second)
	for {
		var waitType string
		if err := pool.QueryRow(ctx, `SELECT COALESCE(wait_event_type, '') FROM pg_stat_activity WHERE pid=$1`, waitingPID).Scan(&waitType); err != nil {
			_ = tx1.Rollback(ctx)
			t.Fatal(err)
		}
		if waitType == "Lock" {
			break
		}
		if time.Now().After(waitDeadline) {
			_ = tx1.Rollback(ctx)
			t.Fatalf("concurrent enqueue PID %d did not wait for advisory lock", waitingPID)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := tx1.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-concurrentDone; err != nil {
		t.Fatal(err)
	}
	var concurrentRows, concurrentEvents, latestJoined int
	if err := pool.QueryRow(ctx, `
SELECT count(*), count(DISTINCT join_event_id), max(joined_at)
FROM welcome_message_deliveries WHERE channel_id=$1 AND target_user_id=$2`, channelID, member.ID).
		Scan(&concurrentRows, &concurrentEvents, &latestJoined); err != nil {
		t.Fatal(err)
	}
	if concurrentRows != len(contents) || concurrentEvents != 1 || latestJoined != firstJoin+20 {
		t.Fatalf("concurrent rows=%d events=%d latest_joined=%d", concurrentRows, concurrentEvents, latestJoined)
	}

	// A new active epoch supersedes the prior pending row instead of releasing
	// both when the member next opens a compatible client.
	if _, err := channels.LeaveChannel(ctx, channelID, member.ID, firstJoin+2); err != nil {
		t.Fatalf("leave channel: %v", err)
	}
	var afterLeave int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM welcome_message_deliveries WHERE channel_id=$1 AND target_user_id=$2`, channelID, member.ID).Scan(&afterLeave); err != nil || afterLeave != 0 {
		t.Fatalf("deliveries after leave=%d err=%v", afterLeave, err)
	}
	secondJoin := firstJoin + 3
	if _, err := channels.InviteToChannel(ctx, channelID, owner.ID, []int64{member.ID}, secondJoin); err != nil {
		t.Fatalf("reinvite member: %v", err)
	}
	var pendingCount int
	var secondEvent int64
	var secondJoined int
	var secondEvents int
	if err := pool.QueryRow(ctx, `
SELECT count(*), count(DISTINCT join_event_id), max(join_event_id), max(joined_at)
FROM welcome_message_deliveries
WHERE channel_id=$1 AND target_user_id=$2 AND delivered_at IS NULL`, channelID, member.ID).
		Scan(&pendingCount, &secondEvents, &secondEvent, &secondJoined); err != nil {
		t.Fatal(err)
	}
	if pendingCount != len(contents) || secondEvents != 1 || secondEvent == firstEvent || secondJoined != secondJoin {
		t.Fatalf("pending=%d events=%d first_event=%d second_event=%d joined=%d", pendingCount, secondEvents, firstEvent, secondEvent, secondJoined)
	}

	now := time.Now()
	// limit counts join events, not template rows: one event claims both templates
	// so the dispatcher can project and encode exactly once.
	claimed, err := welcomeStore.ClaimWelcomeMessageDeliveries(ctx, "test-worker", now, 1, 15*time.Second)
	if err != nil || len(claimed) != len(contents) {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	ids := make([]int64, len(claimed))
	for index, delivery := range claimed {
		ids[index] = delivery.ID
		if delivery.JoinEventID != secondEvent || delivery.JoinedAt != secondJoin || delivery.Content.Message != contents[index].Message || delivery.AttemptCount != 1 {
			t.Fatalf("delivery[%d]=%+v", index, delivery)
		}
	}
	retryAt := now.Add(10 * time.Second)
	if updated, err := welcomeStore.RetryWelcomeMessageDeliveries(ctx, "test-worker", ids, retryAt, "offline"); err != nil || updated != len(ids) {
		t.Fatalf("retry updated=%d err=%v", updated, err)
	}
	if early, err := welcomeStore.ClaimWelcomeMessageDeliveries(ctx, "other-worker", now.Add(time.Second), 1, 15*time.Second); err != nil || len(early) != 0 {
		t.Fatalf("early claim=%+v err=%v", early, err)
	}
	reclaimed, err := welcomeStore.ClaimWelcomeMessageDeliveries(ctx, "other-worker", retryAt, 1, 15*time.Second)
	if err != nil || len(reclaimed) != len(contents) || reclaimed[0].AttemptCount != 2 || reclaimed[1].AttemptCount != 2 {
		t.Fatalf("reclaim=%+v err=%v", reclaimed, err)
	}
	if acked, err := welcomeStore.AckWelcomeMessageDeliveries(ctx, "other-worker", ids, retryAt); err != nil || acked != len(ids) {
		t.Fatalf("ack=%d err=%v", acked, err)
	}
	if _, err := channels.LeaveChannel(ctx, channelID, member.ID, secondJoin+1); err != nil {
		t.Fatalf("leave after delivered welcome: %v", err)
	}
	var deliveredAfterLeave int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM welcome_message_deliveries WHERE channel_id=$1 AND target_user_id=$2`, channelID, member.ID).Scan(&deliveredAfterLeave); err != nil || deliveredAfterLeave != 0 {
		t.Fatalf("delivered rows after leave=%d err=%v", deliveredAfterLeave, err)
	}
	thirdJoin := secondJoin + 2
	if _, err := channels.InviteToChannel(ctx, channelID, owner.ID, []int64{member.ID}, thirdJoin); err != nil {
		t.Fatalf("third join: %v", err)
	}
	expiryNow := time.Now().Add(time.Second)
	claimStart := make(chan struct{})
	claimResults := make([][]domain.WelcomeMessageDelivery, 2)
	claimErrors := make([]error, 2)
	var claimWG sync.WaitGroup
	for i := range claimResults {
		claimWG.Add(1)
		go func(index int) {
			defer claimWG.Done()
			<-claimStart
			claimResults[index], claimErrors[index] = welcomeStore.ClaimWelcomeMessageDeliveries(
				ctx, fmt.Sprintf("expiry-worker-%d", index), expiryNow, 1, 15*time.Second,
			)
		}(i)
	}
	close(claimStart)
	claimWG.Wait()
	if claimErrors[0] != nil || claimErrors[1] != nil || len(claimResults[0])+len(claimResults[1]) != len(contents) ||
		(len(claimResults[0]) != 0 && len(claimResults[0]) != len(contents)) ||
		(len(claimResults[1]) != 0 && len(claimResults[1]) != len(contents)) {
		t.Fatalf("concurrent group claims=%v/%v errors=%v/%v", claimResults[0], claimResults[1], claimErrors[0], claimErrors[1])
	}
	expiryClaim := claimResults[0]
	if len(expiryClaim) == 0 {
		expiryClaim = claimResults[1]
	}
	expiryIDs := make([]int64, len(expiryClaim))
	for i, delivery := range expiryClaim {
		expiryIDs[i] = delivery.ID
	}

	// Both pending and delivered records are retention-bounded. Move this row's
	// whole TTL window into the past, then verify physical deletion.
	if _, err := pool.Exec(ctx, `
UPDATE welcome_message_deliveries
SET created_at=$2, expires_at=$3
WHERE id=ANY($1::bigint[])`, expiryIDs, expiryNow.Add(-25*time.Hour), expiryNow.Add(-time.Hour)); err != nil {
		t.Fatalf("age delivery: %v", err)
	}
	deleted, err := welcomeStore.DeleteExpiredWelcomeMessageDeliveries(ctx, expiryNow, 10)
	if err != nil || deleted != len(expiryIDs) {
		t.Fatalf("delete expired=%d err=%v", deleted, err)
	}
}

type welcomeEffectCounts struct {
	ChannelEvents int
	UserEvents    int
	Outbox        int
}

func welcomeSideEffectCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID, channelID int64) welcomeEffectCounts {
	t.Helper()
	var result welcomeEffectCounts
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM channel_update_events WHERE channel_id = $1`, channelID).Scan(&result.ChannelEvents); err != nil {
		t.Fatalf("count channel events: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_update_events WHERE user_id = $1`, userID).Scan(&result.UserEvents); err != nil {
		t.Fatalf("count user events: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM dispatch_outbox WHERE target_user_id = $1`, userID).Scan(&result.Outbox); err != nil {
		t.Fatalf("count dispatch outbox: %v", err)
	}
	return result
}
