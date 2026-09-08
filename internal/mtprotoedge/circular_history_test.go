package mtprotoedge

import "testing"

func TestInboundSeenHistoryUsesStableCircularBacking(t *testing.T) {
	state := newConnState()
	for id := int64(1); id <= maxTrackedClientMsgIDs; id++ {
		state.trackInbound(id, int32(id*2+1), true, false, msgStateReceived)
	}
	if len(state.order) != maxTrackedClientMsgIDs || len(state.seen) != maxTrackedClientMsgIDs {
		t.Fatalf("initial seen history = order:%d map:%d", len(state.order), len(state.seen))
	}
	backing := &state.order[0]
	for id := int64(maxTrackedClientMsgIDs + 1); id <= 4*maxTrackedClientMsgIDs; id++ {
		state.trackInbound(id, int32(id*2+1), true, false, msgStateReceived)
	}
	if &state.order[0] != backing {
		t.Fatal("full seen history replaced its circular backing")
	}
	if len(state.order) != maxTrackedClientMsgIDs || len(state.seen) != maxTrackedClientMsgIDs {
		t.Fatalf("steady seen history = order:%d map:%d", len(state.order), len(state.seen))
	}
	if _, ok := state.seenRecord(1); ok {
		t.Fatal("seen history retained its oldest ID")
	}
	if _, ok := state.seenRecord(4 * maxTrackedClientMsgIDs); !ok {
		t.Fatal("seen history lost its newest ID")
	}
}
