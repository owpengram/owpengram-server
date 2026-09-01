package postgres

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

func TestAuthKeySessionLayerTransactionAndRestartPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	temp := randomLayerTestAuthKeyID(t)
	perm := randomLayerTestAuthKeyID(t)
	for perm == temp {
		perm = randomLayerTestAuthKeyID(t)
	}
	const sessionID = int64(87001)
	t.Cleanup(func() {
		_ = NewAuthKeyStore(pool).Delete(ctx, perm)
		_ = NewAuthKeyStore(pool).Delete(ctx, temp)
	})

	keys := NewAuthKeyStore(pool)
	expiresAt := int(time.Now().Add(time.Hour).Unix())
	if err := keys.Save(ctx, store.AuthKeyData{ID: temp, ExpiresAt: expiresAt}); err != nil {
		t.Fatal(err)
	}
	if err := keys.Save(ctx, store.AuthKeyData{ID: perm, ExpiresAt: 0}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	firstMsgID := authKeySessionLayerTestMsgID(now, 1)
	sameLayerMsgID := authKeySessionLayerTestMsgID(now, 2)
	newerMsgID := authKeySessionLayerTestMsgID(now, 3)
	concurrentLowMsgID := authKeySessionLayerTestMsgID(now, 4)
	concurrentHighMsgID := authKeySessionLayerTestMsgID(now, 5)
	for _, invalidMsgID := range []int64{
		authKeySessionLayerTestMsgID(now.Add(-302*time.Second), 1),
		authKeySessionLayerTestMsgID(now.Add(31*time.Second), 1),
		firstMsgID + 1,
	} {
		if _, _, err := keys.AdvanceSessionLayer(ctx, temp, sessionID, 220, invalidMsgID); !errors.Is(err, store.ErrAuthKeySessionLayerInvalid) {
			t.Fatalf("invalid msg_id %d advance err = %v", invalidMsgID, err)
		}
	}
	if _, found, err := keys.GetSessionLayer(ctx, temp, sessionID); err != nil || found {
		t.Fatalf("rejected evidence created session row: found=%v err=%v", found, err)
	}
	first, applied, err := keys.AdvanceSessionLayer(ctx, temp, sessionID, 220, firstMsgID)
	if err != nil || !applied || !first.SharedDefault || first.ObservationID <= 0 {
		t.Fatalf("first advance = (%+v,%v,%v)", first, applied, err)
	}
	if err := NewTempAuthKeyBindingStore(pool).Save(ctx, domain.TempAuthKeyBinding{
		TempAuthKeyID:    temp,
		PermAuthKeyID:    int64(binary.LittleEndian.Uint64(perm[:])),
		Nonce:            87,
		TempSessionID:    sessionID,
		ExpiresAt:        expiresAt,
		EncryptedMessage: []byte{8, 7},
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range [][8]byte{temp, perm} {
		got, found, err := NewAuthKeyStore(pool).Get(ctx, id)
		if err != nil || !found || got.Layer != 220 || got.LayerObservationID != first.ObservationID {
			t.Fatalf("bound default %x = (%+v,%v,%v)", id, got, found, err)
		}
	}
	futureSameLayerMsgID := authKeySessionLayerTestMsgID(now.Add(31*time.Second), 1)
	if _, _, err := NewAuthKeyStore(pool).AdvanceSessionLayer(ctx, temp, sessionID, 220, futureSameLayerMsgID); !errors.Is(err, store.ErrAuthKeySessionLayerInvalid) {
		t.Fatalf("future same-Layer fast advance err = %v", err)
	}
	if got, found, err := NewAuthKeyStore(pool).GetSessionLayer(ctx, temp, sessionID); err != nil || !found || got.MessageID != firstMsgID || got.ObservationID != first.ObservationID {
		t.Fatalf("rejected future same-Layer advance changed row = (%+v,%v,%v)", got, found, err)
	}
	sameLayer, applied, err := NewAuthKeyStore(pool).AdvanceSessionLayer(ctx, temp, sessionID, 220, sameLayerMsgID)
	if err != nil || !applied || !sameLayer.SharedDefault || sameLayer.MessageID != sameLayerMsgID ||
		sameLayer.ObservationID != first.ObservationID {
		t.Fatalf("same-Layer high-water advance = (%+v,%v,%v)", sameLayer, applied, err)
	}
	for _, id := range [][8]byte{temp, perm} {
		got, found, err := NewAuthKeyStore(pool).Get(ctx, id)
		if err != nil || !found || got.Layer != 220 || got.LayerObservationID != first.ObservationID {
			t.Fatalf("same-Layer default rewrite %x = (%+v,%v,%v)", id, got, found, err)
		}
	}

	newer, applied, err := NewAuthKeyStore(pool).AdvanceSessionLayer(ctx, temp, sessionID, 227, newerMsgID)
	if err != nil || !applied || !newer.SharedDefault || newer.ObservationID <= first.ObservationID {
		t.Fatalf("newer advance = (%+v,%v,%v)", newer, applied, err)
	}
	old, applied, err := NewAuthKeyStore(pool).AdvanceSessionLayer(ctx, temp, sessionID, 220, firstMsgID)
	if err != nil || applied || old.Layer != 227 || old.MessageID != newerMsgID || !old.SharedDefault {
		t.Fatalf("old replay = (%+v,%v,%v)", old, applied, err)
	}
	if _, _, err := NewAuthKeyStore(pool).AdvanceSessionLayer(ctx, temp, sessionID, 220, newerMsgID); !errors.Is(err, store.ErrAuthKeySessionLayerConflict) {
		t.Fatalf("same-msg conflict = %v", err)
	}

	// Two independent store instances model two server processes. The raw-key
	// row lock and session CAS must converge on the greater selector msg_id.
	type candidate struct {
		layer int
		msgID int64
	}
	candidates := []candidate{{layer: 225, msgID: concurrentLowMsgID}, {layer: 227, msgID: concurrentHighMsgID}}
	start := make(chan struct{})
	errs := make(chan error, len(candidates))
	var wg sync.WaitGroup
	for _, item := range candidates {
		item := item
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := NewAuthKeyStore(pool).AdvanceSessionLayer(ctx, temp, sessionID, item.layer, item.msgID)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	restarted := NewAuthKeyStore(pool)
	current, found, err := restarted.GetSessionLayer(ctx, temp, sessionID)
	if err != nil || !found || current.Layer != 227 || current.MessageID != concurrentHighMsgID || !current.SharedDefault {
		t.Fatalf("restart authoritative row = (%+v,%v,%v)", current, found, err)
	}
	for _, id := range [][8]byte{temp, perm} {
		got, found, err := restarted.Get(ctx, id)
		if err != nil || !found || got.Layer != 227 || got.LayerObservationID != current.ObservationID {
			t.Fatalf("transactional shared default %x = (%+v,%v,%v)", id, got, found, err)
		}
	}

	// Expiry ends the old row's ordering authority. A still-fresh selector with
	// a lower msg_id may replace it and must publish one new shared observation.
	if _, err := pool.Exec(ctx, `
UPDATE auth_key_session_layers
SET expires_at = now() - interval '1 second'
WHERE raw_auth_key_id = $1 AND session_id = $2
`, authKeyIDToInt64(temp), sessionID); err != nil {
		t.Fatalf("expire session Layer row: %v", err)
	}
	replacement, applied, err := restarted.AdvanceSessionLayer(ctx, temp, sessionID, 225, firstMsgID)
	if err != nil || !applied || replacement.Layer != 225 || replacement.MessageID != firstMsgID ||
		!replacement.SharedDefault || replacement.ObservationID <= current.ObservationID {
		t.Fatalf("expired-row replacement = (%+v,%v,%v)", replacement, applied, err)
	}
	for _, id := range [][8]byte{temp, perm} {
		got, found, err := restarted.Get(ctx, id)
		if err != nil || !found || got.Layer != 225 || got.LayerObservationID != replacement.ObservationID {
			t.Fatalf("replacement shared default %x = (%+v,%v,%v)", id, got, found, err)
		}
	}
}

func authKeySessionLayerTestMsgID(at time.Time, order uint32) int64 {
	return int64((uint64(at.Unix()) << 32) | uint64(order)<<2)
}

func randomLayerTestAuthKeyID(t *testing.T) [8]byte {
	t.Helper()
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	return id
}
