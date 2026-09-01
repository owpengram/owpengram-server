package mtprotoedge

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/iamxvbaba/td/proto"
	"go.uber.org/zap"
)

// storeLogicalRPCResultForTest publishes the same production invariant as the
// outbound actor: the exact rpc_result frame enters the logical-session outbox
// before its metadata receipt becomes visible. Tests that call the receipt
// ledger directly must not invent the old impossible state where a replay row
// exists without an unacknowledged server frame.
func storeLogicalRPCResultForTest(
	t *testing.T,
	s *Server,
	c *Conn,
	reqMsgID int64,
	encoded *encodedOutboundMessage,
) {
	t.Helper()
	if s == nil || c == nil || encoded == nil || len(encoded.body) == 0 {
		t.Fatal("invalid logical rpc_result fixture")
	}
	if c.outboundState == nil {
		s.conns.attachLogicalSession(c, s.outboundTrackedBudget)
	} else {
		s.conns.adoptLogicalSession(c)
	}
	physicalReqMsgID := encoded.writtenRequestID()
	if physicalReqMsgID == 0 {
		physicalReqMsgID = reqMsgID
	}
	frameBody := encoded.body
	if physicalReqMsgID != reqMsgID && encoded.typeID == proto.ResultTypeID {
		physical, err := cloneRPCResultForRequest(encoded, physicalReqMsgID, true)
		if err != nil {
			t.Fatalf("retarget logical rpc_result fixture: %v", err)
		}
		frameBody = physical.body
	}
	state := c.outboundState
	state.mu.Lock()
	if state.budget == nil || !state.budget.reserve(len(frameBody)) {
		state.mu.Unlock()
		t.Fatal("reserve logical rpc_result fixture")
	}
	candidate := reqMsgID*4 + 1
	if candidate <= 0 {
		candidate = 1
	}
	frame := &outboundFrame{
		msgID: state.reserveMsgID(candidate), seqNo: state.peekSeqNo(true),
		typeID: proto.ResultTypeID, body: frameBody, reqMsgID: physicalReqMsgID,
		priority: encoded.priority, delivery: encoded.delivery,
		compressed: encoded.compressed, uncompressedBytes: encoded.uncompressedBytes,
		layer: encoded.layer, layerInvariant: encoded.layerInvariant,
		reservedBytes: len(frameBody), reservationBudget: state.budget,
	}
	if err := state.admitReserved(frame); err != nil {
		frame.releaseReservation(state.budget)
		state.mu.Unlock()
		t.Fatalf("admit logical rpc_result fixture: %v", err)
	}
	encoded.replayMsgID = frame.msgID
	encoded.replaySeqNo = frame.seqNo
	state.mu.Unlock()
	s.rpcResults.Complete(c.authKeyID, c.sessionID, reqMsgID, encoded, true)
}

func acknowledgeLogicalRPCResultForTest(t *testing.T, s *Server, c *Conn, reqMsgID int64) {
	t.Helper()
	if s == nil || c == nil || c.outboundState == nil {
		t.Fatal("missing logical rpc_result fixture")
	}
	state := c.outboundState
	state.mu.Lock()
	msgID := state.byRequest[reqMsgID]
	requestIDs := state.ack([]int64{msgID})
	state.mu.Unlock()
	if len(requestIDs) != 1 || requestIDs[0] != reqMsgID {
		t.Fatalf("logical ACK resolved request ids %v", requestIDs)
	}
	if !s.rpcResults.Acknowledge(c.authKeyID, c.sessionID, reqMsgID) {
		t.Fatal("logical ACK did not release result receipt")
	}
}

func TestLogicalSessionOwnsExactResultAcrossPhysicalReconnect(t *testing.T) {
	manager := NewSessionManager(zap.NewNop())
	budget := newOutboundTrackedBudget(1024)
	authKeyID := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	first := &Conn{authKeyID: authKeyID, sessionID: 42}
	manager.attachLogicalSession(first, budget)

	body := append([]byte{0xf3, 0x5c, 0x6d, 0xf3}, make([]byte, 28)...)
	binary.LittleEndian.PutUint64(body[4:12], uint64(77))
	if !budget.reserve(len(body)) {
		t.Fatal("reserve result body")
	}
	frame := &outboundFrame{
		msgID: 101, seqNo: 1, typeID: proto.ResultTypeID, body: body,
		reqMsgID: 77, reservedBytes: len(body), reservationBudget: budget,
	}
	first.outboundState.mu.Lock()
	if err := first.outboundState.admitReserved(frame); err != nil {
		first.outboundState.mu.Unlock()
		t.Fatalf("admit result: %v", err)
	}
	first.outboundState.mu.Unlock()

	second := &Conn{authKeyID: authKeyID, sessionID: 42}
	manager.attachLogicalSession(second, budget)
	if first.outboundState != second.outboundState {
		t.Fatal("physical reconnect did not reuse logical outbound state")
	}
	replay, ok := manager.rpcResult(authKeyID, 42, 77)
	if !ok {
		t.Fatal("logical result not found after reconnect")
	}
	if replay.replayMsgID != frame.msgID || replay.replaySeqNo != frame.seqNo || !bytes.Equal(replay.body, body) {
		t.Fatalf("replay identity/body = msg:%d seq:%d bytes:%d", replay.replayMsgID, replay.replaySeqNo, len(replay.body))
	}
	if &replay.body[0] != &frame.body[0] {
		t.Fatal("replay created a second payload owner")
	}
	attempt, err := cloneRPCResultForRequest(replay, replay.reqMsgID, false)
	if err != nil {
		t.Fatalf("clone same-request replay descriptor: %v", err)
	}
	if attempt.replayMsgID != frame.msgID || attempt.replaySeqNo != frame.seqNo ||
		!sameBacking(attempt.body, frame.body) {
		t.Fatal("queued duplicate lost stable logical frame identity")
	}
}

func TestLateCompletionAdoptionDoesNotClearOfflineRetention(t *testing.T) {
	manager := NewSessionManager(zap.NewNop())
	budget := newOutboundTrackedBudget(1024)
	key := sessionKey{authKeyID: [8]byte{1, 3, 5, 7}, sessionID: 421}
	c := &Conn{authKeyID: key.authKeyID, sessionID: key.sessionID}
	manager.attachLogicalSession(c, budget)
	offlineAt := time.Unix(1_800_000_000, 0)
	manager.mu.Lock()
	manager.markLogicalSessionOfflineLocked(key, offlineAt)
	manager.mu.Unlock()

	manager.adoptLogicalSession(c)
	snapshot := manager.runtimeSnapshot()
	if snapshot.logical != 1 || snapshot.offlineLogical != 1 {
		t.Fatalf("late completion snapshot = logical:%d offline:%d, want 1/1", snapshot.logical, snapshot.offlineLogical)
	}
	manager.sweepLogicalSessions(offlineAt.Add(logicalSessionOfflineTTL + time.Second))
	snapshot = manager.runtimeSnapshot()
	if snapshot.logical != 0 || snapshot.offlineLogical != 0 {
		t.Fatalf("post-TTL snapshot = logical:%d offline:%d, want 0/0", snapshot.logical, snapshot.offlineLogical)
	}
}

func TestRetiredLateCompletionCannotRecreateDestroyedLogicalSession(t *testing.T) {
	manager := NewSessionManager(zap.NewNop())
	budget := newOutboundTrackedBudget(1024)
	key := sessionKey{authKeyID: [8]byte{2, 4, 6, 8}, sessionID: 422}
	c := &Conn{authKeyID: key.authKeyID, sessionID: key.sessionID}
	manager.attachLogicalSession(c, budget)
	c.retire()
	manager.mu.Lock()
	state := manager.destroyLogicalSessionLocked(key)
	manager.mu.Unlock()
	manager.releaseLogicalSession(key, state)

	manager.adoptLogicalSession(c)
	if snapshot := manager.runtimeSnapshot(); snapshot.logical != 0 {
		t.Fatalf("retired late completion recreated %d logical sessions", snapshot.logical)
	}
}

func TestLogicalSessionACKReleasesPayloadAndReceipt(t *testing.T) {
	manager := NewSessionManager(zap.NewNop())
	budget := newOutboundTrackedBudget(1024)
	authKeyID := [8]byte{8, 7, 6, 5, 4, 3, 2, 1}
	c := &Conn{authKeyID: authKeyID, sessionID: 43}
	manager.attachLogicalSession(c, budget)

	body := make([]byte, 64)
	if !budget.reserve(len(body)) {
		t.Fatal("reserve result body")
	}
	frame := &outboundFrame{
		msgID: 201, seqNo: 1, typeID: proto.ResultTypeID, body: body,
		reqMsgID: 88, reservedBytes: len(body), reservationBudget: budget,
	}
	c.outboundState.mu.Lock()
	if err := c.outboundState.admitReserved(frame); err != nil {
		c.outboundState.mu.Unlock()
		t.Fatalf("admit result: %v", err)
	}
	c.outboundState.mu.Unlock()

	ledger := newRPCExecutionLedger(time.Now, rpcExecutionLedgerCapacity{
		maxPending: 16, maxPendingPerAuth: 16,
		globalMaxEntries: 16, authMaxEntries: 16, sessionMaxEntries: 16,
		replayStore: manager,
	})
	claim, err := ledger.Acquire(authKeyID, 43, 88)
	if err != nil || claim.state != rpcResultAcquireOwner {
		t.Fatalf("acquire owner = %#v, %v", claim, err)
	}
	claim.owner.CompleteExecution(true)
	claim.owner.HandOff()
	ledger.Complete(authKeyID, 43, 88, &encodedOutboundMessage{body: body, typeID: proto.ResultTypeID, reqMsgID: 88}, true)

	key := rpcExecutionKey{authKeyID: authKeyID, sessionID: 43, reqMsgID: 88}
	shard := ledger.shard(key)
	shard.mu.Lock()
	entry := shard.byKey[key].Value.(*rpcExecutionReceipt)
	if entry.unavailable {
		shard.mu.Unlock()
		t.Fatal("logical outbox receipt was marked unavailable")
	}
	shard.mu.Unlock()
	if got := ledger.receiptBudgetBytes(); got != rpcExecutionReceiptBudgetBytes {
		t.Fatalf("receipt budget bytes = %d, want %d", got, rpcExecutionReceiptBudgetBytes)
	}

	c.outboundState.mu.Lock()
	acked := c.outboundState.ack([]int64{201})
	c.outboundState.mu.Unlock()
	if len(acked) != 1 || acked[0] != 88 {
		t.Fatalf("acked request ids = %v", acked)
	}
	if !ledger.Acknowledge(authKeyID, 43, 88) {
		t.Fatal("ledger did not observe ACK")
	}
	if got := budget.snapshot(); got != 0 {
		t.Fatalf("tracked payload bytes after ACK = %d", got)
	}
	shard.mu.Lock()
	_, retained := shard.byKey[key]
	shard.mu.Unlock()
	if retained {
		t.Fatal("ACK retained a completed receipt")
	}
}

func TestLogicalSessionCapacityNeverEvictsUnackedFrame(t *testing.T) {
	budget := newOutboundTrackedBudget(1024)
	state := newOutboundStateWithLimits(budget, 1, 16)
	firstBody := make([]byte, 8)
	if !budget.reserve(len(firstBody)) {
		t.Fatal("reserve first body")
	}
	first := &outboundFrame{
		msgID: 1, seqNo: 1, typeID: proto.ResultTypeID, body: firstBody,
		reqMsgID: 11, reservedBytes: len(firstBody), reservationBudget: budget,
	}
	if err := state.admitReserved(first); err != nil {
		t.Fatalf("admit first: %v", err)
	}
	second := &outboundFrame{msgID: 2, seqNo: 3, typeID: proto.ResultTypeID, body: make([]byte, 8), reqMsgID: 12}
	if err := state.admitReserved(second); err == nil {
		t.Fatal("capacity admitted a second unacknowledged frame")
	}
	if state.pending[first.msgID] != first || state.byRequest[first.reqMsgID] != first.msgID {
		t.Fatal("capacity failure evicted or rewired the existing frame")
	}
	state.releaseAll()
}

func TestLogicalSessionDestroyReleasesPayloadAndCompletedReceipt(t *testing.T) {
	s := New(Options{})
	authKeyID := [8]byte{9, 1}
	c := &Conn{authKeyID: authKeyID, sessionID: 91}
	claim, err := s.rpcResults.Acquire(authKeyID, 91, 901)
	if err != nil || claim.owner == nil {
		t.Fatalf("acquire owner: %#v err=%v", claim, err)
	}
	claim.owner.CompleteExecution(true)
	storeLogicalRPCResultForTest(t, s, c, 901, &encodedOutboundMessage{
		body: make([]byte, 32), typeID: proto.ResultTypeID, reqMsgID: 901,
	})
	if _, ok := s.rpcResults.Replay(authKeyID, 91, 901); !ok {
		t.Fatal("logical result fixture was not published")
	}
	if removed := s.conns.DestroySessionForAuthKey(authKeyID, 91); removed {
		t.Fatal("construction-only logical session unexpectedly reported active")
	}
	if _, ok := s.rpcResults.Replay(authKeyID, 91, 901); ok {
		t.Fatal("destroy retained completed receipt")
	}
	if got := s.outboundTrackedBudget.snapshot(); got != 0 {
		t.Fatalf("destroy retained %d payload bytes", got)
	}
}

func TestBusinessAuthRevocationReleasesOfflineTempLogicalSession(t *testing.T) {
	s := New(Options{})
	rawAuthKeyID := [8]byte{9, 2}
	businessAuthKeyID := [8]byte{9, 3}
	c := &Conn{authKeyID: rawAuthKeyID, sessionID: 92}
	c.SetBusinessAuthKeyID(businessAuthKeyID)
	claim, err := s.rpcResults.Acquire(rawAuthKeyID, 92, 902)
	if err != nil || claim.owner == nil {
		t.Fatalf("acquire owner: %#v err=%v", claim, err)
	}
	claim.owner.CompleteExecution(true)
	storeLogicalRPCResultForTest(t, s, c, 902, &encodedOutboundMessage{
		body: make([]byte, 48), typeID: proto.ResultTypeID, reqMsgID: 902,
	})
	if closed := s.conns.CloseSessionsForBusinessAuthKey(businessAuthKeyID); closed != 0 {
		t.Fatalf("offline logical revocation closed %d physical conns", closed)
	}
	if _, ok := s.rpcResults.Replay(rawAuthKeyID, 92, 902); ok {
		t.Fatal("business auth revocation retained temp-key receipt")
	}
	if got := s.outboundTrackedBudget.snapshot(); got != 0 {
		t.Fatalf("business auth revocation retained %d payload bytes", got)
	}
}
