package mtprotoedge

import (
	"testing"
	"time"
)

func TestBulkRPCSchedulerKeepsOverflowBehindGate(t *testing.T) {
	scheduler := newBulkRPCScheduler(2)
	first := scheduler.reserve()
	second := scheduler.reserve()
	third := scheduler.reserve()
	woken := make(chan bool, 1)
	third.subscribe(func(success bool) { woken <- success })
	select {
	case <-woken:
		t.Fatal("overflow bulk handler became runnable before a slot was released")
	default:
	}
	first.release()
	select {
	case success := <-woken:
		if !success {
			t.Fatal("overflow bulk handler was canceled instead of admitted")
		}
	case <-time.After(time.Second):
		t.Fatal("overflow bulk handler did not become runnable")
	}
	second.release()
	third.release()
}

func TestBulkRPCSchedulerCloseCancelsWaiters(t *testing.T) {
	scheduler := newBulkRPCScheduler(1)
	active := scheduler.reserve()
	waiting := scheduler.reserve()
	woken := make(chan bool, 1)
	waiting.subscribe(func(success bool) { woken <- success })
	scheduler.close()
	select {
	case success := <-woken:
		if success {
			t.Fatal("closed bulk scheduler granted a waiting handler")
		}
	case <-time.After(time.Second):
		t.Fatal("closed bulk scheduler did not cancel waiter")
	}
	active.release()
}

func TestBulkRPCAdmissionDoesNotReserveGlobalSlotBeforeSessionCredit(t *testing.T) {
	scheduler := newBulkRPCScheduler(1)
	state := newOutboundStateWithLimits(newOutboundTrackedBudget(1<<20), 128, 1<<20)
	state.bulkMax = 1
	activeCredit := state.reserveBulkCredit()
	waitingCredit := state.reserveBulkCredit()
	admission := newBulkRPCAdmission(scheduler)
	woken := make(chan bool, 1)
	admission.subscribeAfter(waitingCredit.credit, func(success bool) { woken <- success })

	scheduler.mu.Lock()
	inUseBeforeCredit := scheduler.inUse
	scheduler.mu.Unlock()
	if inUseBeforeCredit != 0 {
		t.Fatalf("credit-blocked request reserved %d global slots, want 0", inUseBeforeCredit)
	}
	select {
	case <-woken:
		t.Fatal("credit-blocked request became runnable")
	default:
	}

	activeCredit.releaseIfOwned()
	select {
	case success := <-woken:
		if !success {
			t.Fatal("request was canceled after session credit became available")
		}
	case <-time.After(time.Second):
		t.Fatal("request did not enter global scheduler after session credit")
	}
	scheduler.mu.Lock()
	inUseAfterCredit := scheduler.inUse
	scheduler.mu.Unlock()
	if inUseAfterCredit != 1 {
		t.Fatalf("admitted request reserved %d global slots, want 1", inUseAfterCredit)
	}

	admission.release()
	waitingCredit.releaseIfOwned()
	state.closeBulkWindow()
}
