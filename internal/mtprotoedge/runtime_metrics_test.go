package mtprotoedge

import (
	"testing"
	"time"
)

func TestRuntimeSnapshotIsNilSafeAndReportsConfiguredLimits(t *testing.T) {
	if got := (*Server)(nil).RuntimeSnapshot(); got != (RuntimeSnapshot{}) {
		t.Fatalf("nil server snapshot = %#v, want zero", got)
	}
	if got := (&Server{}).RuntimeSnapshot(); got != (RuntimeSnapshot{}) {
		t.Fatalf("partial server snapshot = %#v, want zero", got)
	}

	server := New(Options{})
	snapshot := server.RuntimeSnapshot()
	if snapshot.RawConnectionLimit <= 0 || snapshot.HandshakeLimit <= 0 {
		t.Fatalf("admission limits not reported: %#v", snapshot)
	}
	if snapshot.InboundRPCMaxTasks != rpcResultFlightDefaultMaxPending || snapshot.InboundRPCMaxBytes <= 0 {
		t.Fatalf("inbound RPC limits not reported: %#v", snapshot)
	}
	if snapshot.RPCDeliveryHookWorkers != defaultRPCDeliveryHookWorkers ||
		snapshot.RPCDeliveryHookCapacity != defaultRPCDeliveryHookMaxPending {
		t.Fatalf("delivery hook limits not reported: %#v", snapshot)
	}
	if snapshot.InboundFrameMaxBytes <= 0 || snapshot.OutboundTrackedMaxBytes <= 0 || snapshot.OutboundWriteMaxBytes <= 0 {
		t.Fatalf("byte limits not reported: %#v", snapshot)
	}
	if snapshot.RawConnections != 0 || snapshot.ActiveSessions != 0 || snapshot.LogicalOutboxBytes != 0 {
		t.Fatalf("fresh server reported live ownership: %#v", snapshot)
	}
}

func TestRuntimeSnapshotSeparatesExecutionOwnersReceiptsAndBudget(t *testing.T) {
	server := New(Options{})
	server.rpcResults = newRPCExecutionLedgerForTest(time.Now, 4)
	auth := [8]byte{1, 2, 3, 4}
	claim, err := server.rpcResults.Acquire(auth, 5, 6)
	if err != nil || claim.state != rpcResultAcquireOwner {
		t.Fatalf("owner = %#v, %v", claim, err)
	}
	pending := server.RuntimeSnapshot()
	if pending.RPCExecutionOwners != 1 || pending.RPCExecutionReservedEntries != 1 ||
		pending.RPCExecutionReceipts != 0 || pending.RPCExecutionReceiptBudgetBytes != 0 {
		t.Fatalf("pending execution snapshot = %#v", pending)
	}
	server.rpcResults.completeReplayableForTest(auth, 5, 6, &encodedOutboundMessage{body: make([]byte, 4<<20)})
	completed := server.RuntimeSnapshot()
	if completed.RPCExecutionOwners != 0 || completed.RPCExecutionReservedEntries != 1 ||
		completed.RPCExecutionReceipts != 1 || completed.RPCExecutionReceiptBudgetBytes != rpcExecutionReceiptBudgetBytes {
		t.Fatalf("completed execution snapshot = %#v", completed)
	}
	server.rpcResults.Acknowledge(auth, 5, 6)
	released := server.RuntimeSnapshot()
	if released.RPCExecutionReservedEntries != 0 || released.RPCExecutionReceipts != 0 ||
		released.RPCExecutionReceiptBudgetBytes != 0 {
		t.Fatalf("released execution snapshot = %#v", released)
	}
}
