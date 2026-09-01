package postgres

import (
	"context"
	"testing"
	"time"

	"telesrv/internal/domain"
)

func TestBootstrapUpdateJobPostgresSameAuthKeyReconnectTakesOverPendingSession(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	user := createLoginCodeDeliveryTestUser(t, ctx, pool, "bootstrap-reconnect")
	msg, err := NewMessageStore(pool).Create(ctx, domain.Message{
		OwnerUserID: user.ID,
		Peer:        domain.Peer{Type: domain.PeerTypeUser, ID: domain.OfficialSystemUserID},
		From:        domain.Peer{Type: domain.PeerTypeUser, ID: domain.OfficialSystemUserID},
		Date:        int(time.Now().Unix()),
		Body:        "Login code: 12345",
	})
	if err != nil {
		t.Fatalf("create bootstrap message: %v", err)
	}
	bootstrap := NewBootstrapUpdateJobStore(pool)
	authKeyID := [8]byte{1, 3, 5, 7}
	const (
		oldSessionID = int64(11001)
		newSessionID = int64(22002)
	)
	job, err := bootstrap.EnqueueLoginMessage(ctx, domain.BootstrapUpdateJob{
		Kind: domain.BootstrapUpdateJobLoginMessage, UserID: user.ID,
		AuthKeyID: authKeyID, SessionID: oldSessionID, MessageBoxID: msg.ID,
	})
	if err != nil {
		t.Fatalf("enqueue bootstrap: %v", err)
	}
	if ready, err := bootstrap.MarkReadyForSession(ctx, user.ID, [8]byte{9}, newSessionID); err != nil || ready != 0 {
		t.Fatalf("different-auth ready=%d err=%v, want 0/nil", ready, err)
	}
	ready, err := bootstrap.MarkReadyForSession(ctx, user.ID, authKeyID, newSessionID)
	if err != nil || ready != 1 {
		t.Fatalf("same-auth reconnect ready=%d err=%v, want 1/nil", ready, err)
	}
	var status string
	var sessionID int64
	if err := pool.QueryRow(ctx, `SELECT status, session_id FROM bootstrap_update_jobs WHERE id = $1`, job.ID).Scan(&status, &sessionID); err != nil {
		t.Fatalf("load bootstrap job: %v", err)
	}
	if status != string(domain.BootstrapUpdateJobReady) || sessionID != newSessionID {
		t.Fatalf("bootstrap status/session = %s/%d, want ready/%d", status, sessionID, newSessionID)
	}
}

func TestBootstrapUpdateJobPostgresMarksReadinessBatchByOrdinal(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	user := createLoginCodeDeliveryTestUser(t, ctx, pool, "bootstrap-batch")
	messages := NewMessageStore(pool)
	bootstrap := NewBootstrapUpdateJobStore(pool)
	authKeyID := [8]byte{2, 4, 6, 8}
	for index := 0; index < 2; index++ {
		msg, err := messages.Create(ctx, domain.Message{
			OwnerUserID: user.ID,
			Peer:        domain.Peer{Type: domain.PeerTypeUser, ID: domain.OfficialSystemUserID},
			From:        domain.Peer{Type: domain.PeerTypeUser, ID: domain.OfficialSystemUserID},
			Date:        int(time.Now().Unix()) + index,
			Body:        "Login code batch",
		})
		if err != nil {
			t.Fatalf("create bootstrap message %d: %v", index, err)
		}
		if _, err := bootstrap.EnqueueLoginMessage(ctx, domain.BootstrapUpdateJob{
			Kind: domain.BootstrapUpdateJobLoginMessage, UserID: user.ID,
			AuthKeyID: authKeyID, SessionID: int64(100 + index), MessageBoxID: msg.ID,
		}); err != nil {
			t.Fatalf("enqueue bootstrap %d: %v", index, err)
		}
	}

	results, err := bootstrap.markReadyForSessions(ctx, []bootstrapReadyBatchRequest{
		{userID: user.ID + 1, authKeyID: authKeyID, sessionID: 700},
		{userID: user.ID, authKeyID: [8]byte{9}, sessionID: 701},
		{userID: user.ID, authKeyID: authKeyID, sessionID: 702},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || results[0] != 0 || results[1] != 0 || results[2] != 2 {
		t.Fatalf("batch results = %#v, want [0 0 2]", results)
	}
	var count int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM bootstrap_update_jobs
WHERE user_id = $1 AND auth_key_id = $2 AND status = 'ready' AND session_id = $3`,
		user.ID, authKeyIDToInt64(authKeyID), int64(702)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("ready jobs = %d, want 2", count)
	}

	if _, err := bootstrap.markReadyForSessions(ctx, []bootstrapReadyBatchRequest{
		{userID: user.ID, authKeyID: authKeyID, sessionID: 1},
		{userID: user.ID, authKeyID: authKeyID, sessionID: 2},
	}); err == nil {
		t.Fatal("duplicate fence accepted in one batch")
	}
}
