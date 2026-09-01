package mtprotoedge

// RuntimeSnapshot is a point-in-time, identity-free view of the MTProto edge.
// It deliberately exposes only bounded aggregate values so callers can publish
// it through a metrics endpoint without leaking auth keys, sessions or remote
// addresses. Values from independently locked components can differ by one
// concurrent transition; every individual budget/count remains internally
// consistent.
type RuntimeSnapshot struct {
	RawConnections                 int64
	RawConnectionLimit             int64
	Handshakes                     int64
	HandshakeLimit                 int64
	ActiveSessions                 int64
	ProvisionalSessions            int64
	LogicalSessions                int64
	OfflineLogicalSessions         int64
	LogicalOutboxFrames            int64
	LogicalOutboxBytes             int64
	PendingPushBytes               int64
	InboundRPCTasks                int64
	InboundRPCBytes                int64
	InboundRPCReadyConnections     int64
	InboundRPCMaxTasks             int64
	InboundRPCMaxBytes             int64
	RPCDeliveryHookWorkers         int64
	RPCDeliveryHookCapacity        int64
	RPCDeliveryHookReserved        int64
	RPCDeliveryHookQueued          int64
	RPCDeliveryHookRunning         int64
	RPCDeliveryHookCompleted       uint64
	RPCDeliveryHookRejected        uint64
	RPCDeliveryHookPanics          uint64
	RPCDeliveryHookDurationSeconds float64
	InboundFrameBytes              int64
	InboundFrameMaxBytes           int64
	OutboundTrackedBytes           int64
	OutboundTrackedMaxBytes        int64
	OutboundControlBytes           int64
	OutboundControlMaxBytes        int64
	OutboundWriteBytes             int64
	OutboundWriteMaxBytes          int64
	RPCExecutionOwners             int64
	RPCExecutionReservedEntries    int64
	RPCExecutionReceipts           int64
	RPCExecutionReceiptBudgetBytes int64
	RPCExecutionSubscribers        int64
}

type sessionManagerRuntimeSnapshot struct {
	active         int64
	provisional    int64
	logical        int64
	offlineLogical int64
	frames         int64
	bytes          int64
	pendingBytes   int64
}

func (m *SessionManager) runtimeSnapshot() sessionManagerRuntimeSnapshot {
	if m == nil {
		return sessionManagerRuntimeSnapshot{}
	}

	// Never hold SessionManager.mu while taking an outbound-state mutex. The
	// physical actor can publish/retire a Conn next to an outbox transition, and
	// metrics must not add a new cross-component lock order.
	m.mu.RLock()
	states := make([]*outboundState, 0, len(m.logicalSessions))
	result := sessionManagerRuntimeSnapshot{
		active:      int64(len(m.bySession)),
		provisional: int64(len(m.claims)),
		logical:     int64(len(m.logicalSessions)),
	}
	if m.pendingBudget != nil {
		result.pendingBytes = m.pendingBudget.snapshot()
	}
	for _, logical := range m.logicalSessions {
		if logical == nil {
			continue
		}
		if !logical.offlineAt.IsZero() {
			result.offlineLogical++
		}
		if logical.outbound != nil {
			states = append(states, logical.outbound)
		}
	}
	m.mu.RUnlock()

	for _, state := range states {
		state.mu.Lock()
		result.frames += int64(len(state.pending))
		result.bytes += int64(state.totalBytes)
		state.mu.Unlock()
	}
	return result
}

type admissionRuntimeSnapshot struct {
	connections     int64
	connectionLimit int64
	handshakes      int64
	handshakeLimit  int64
}

func (a *admissionController) runtimeSnapshot() admissionRuntimeSnapshot {
	if a == nil {
		return admissionRuntimeSnapshot{}
	}
	a.mu.Lock()
	result := admissionRuntimeSnapshot{
		connections:     int64(a.connections),
		connectionLimit: int64(a.maxConnections),
	}
	a.mu.Unlock()
	if a.handshakes != nil {
		result.handshakes = int64(len(a.handshakes))
		result.handshakeLimit = int64(cap(a.handshakes))
	}
	return result
}

type inboundRPCRuntimeSnapshot struct {
	tasks int64
	bytes int64
	ready int64
}

func (s *inboundRPCScheduler) runtimeSnapshot() inboundRPCRuntimeSnapshot {
	if s == nil {
		return inboundRPCRuntimeSnapshot{}
	}
	s.budgetMu.Lock()
	result := inboundRPCRuntimeSnapshot{tasks: int64(s.tasks), bytes: s.bytes}
	s.budgetMu.Unlock()
	s.readyMu.Lock()
	result.ready = int64(s.ready.Len())
	s.readyMu.Unlock()
	return result
}

// RuntimeSnapshot returns aggregate MTProto ownership and capacity state.
func (s *Server) RuntimeSnapshot() RuntimeSnapshot {
	if s == nil {
		return RuntimeSnapshot{}
	}
	sessions := s.conns.runtimeSnapshot()
	admission := s.admission.runtimeSnapshot()
	inbound := s.rpcScheduler.runtimeSnapshot()
	deliveryHooks := s.rpcDeliveryHooks.runtimeSnapshot()
	result := RuntimeSnapshot{
		RawConnections:                 admission.connections,
		RawConnectionLimit:             admission.connectionLimit,
		Handshakes:                     admission.handshakes,
		HandshakeLimit:                 admission.handshakeLimit,
		ActiveSessions:                 sessions.active,
		ProvisionalSessions:            sessions.provisional,
		LogicalSessions:                sessions.logical,
		OfflineLogicalSessions:         sessions.offlineLogical,
		LogicalOutboxFrames:            sessions.frames,
		LogicalOutboxBytes:             sessions.bytes,
		PendingPushBytes:               sessions.pendingBytes,
		InboundRPCTasks:                inbound.tasks,
		InboundRPCBytes:                inbound.bytes,
		InboundRPCReadyConnections:     inbound.ready,
		RPCDeliveryHookWorkers:         deliveryHooks.workers,
		RPCDeliveryHookCapacity:        deliveryHooks.capacity,
		RPCDeliveryHookReserved:        deliveryHooks.reserved,
		RPCDeliveryHookQueued:          deliveryHooks.queued,
		RPCDeliveryHookRunning:         deliveryHooks.running,
		RPCDeliveryHookCompleted:       deliveryHooks.completed,
		RPCDeliveryHookRejected:        deliveryHooks.rejected,
		RPCDeliveryHookPanics:          deliveryHooks.panics,
		RPCDeliveryHookDurationSeconds: deliveryHooks.durationSeconds,
	}
	if s.rpcScheduler != nil {
		result.InboundRPCMaxTasks = int64(s.rpcScheduler.maxTasks)
		result.InboundRPCMaxBytes = s.rpcScheduler.maxBytes
	}
	if s.frameBudget != nil {
		result.InboundFrameBytes = s.frameBudget.usedBytes()
		result.InboundFrameMaxBytes = s.frameBudget.max
	}
	if s.outboundTrackedBudget != nil {
		result.OutboundTrackedBytes = s.outboundTrackedBudget.snapshot()
		result.OutboundTrackedMaxBytes = s.outboundTrackedBudget.maxBytes
	}
	if s.outboundControlBudget != nil {
		result.OutboundControlBytes = s.outboundControlBudget.snapshot()
		result.OutboundControlMaxBytes = s.outboundControlBudget.maxBytes
	}
	if s.outboundScratchPool != nil && s.outboundScratchPool.budget != nil {
		result.OutboundWriteBytes = s.outboundScratchPool.snapshot()
		result.OutboundWriteMaxBytes = s.outboundScratchPool.budget.maxBytes
	}
	if s.rpcResults != nil {
		result.RPCExecutionOwners = s.rpcResults.flightLimit.snapshot()
		result.RPCExecutionReservedEntries = s.rpcResults.reservedEntries.snapshot()
		result.RPCExecutionReceipts = s.rpcResults.receiptCount.Load()
		result.RPCExecutionReceiptBudgetBytes = s.rpcResults.receiptBudgetBytes()
		if s.rpcResults.subscriberBudget != nil {
			result.RPCExecutionSubscribers = s.rpcResults.subscriberBudget.global.snapshot()
		}
	}
	return result
}
