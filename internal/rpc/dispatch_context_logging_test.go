package rpc

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/iamxvbaba/td/clock"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type loggingStepClock struct {
	mu   sync.Mutex
	now  time.Time
	step time.Duration
}

func (c *loggingStepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(c.step)
	return c.now
}

func (*loggingStepClock) Timer(d time.Duration) clock.Timer { return clock.System.Timer(d) }

func (*loggingStepClock) Ticker(d time.Duration) clock.Ticker { return clock.System.Ticker(d) }

func TestSlowSuccessfulPreHandlerDoesNotEnterInfoHotPath(t *testing.T) {
	infoCore, infoLogs := observer.New(zap.InfoLevel)
	infoRouter := New(Config{}, Deps{}, zap.New(infoCore), &loggingStepClock{step: 25 * time.Millisecond})
	if _, _, err := infoRouter.prepareRPCDispatchContext(context.Background(), [8]byte{1}, 2, 0, "help.getConfig"); err != nil {
		t.Fatal(err)
	}
	if got := infoLogs.FilterMessage("slow pre-handler").Len(); got != 0 {
		t.Fatalf("slow successful pre-handler emitted %d Info logs, want none", got)
	}

	debugCore, debugLogs := observer.New(zap.DebugLevel)
	debugRouter := New(Config{}, Deps{}, zap.New(debugCore), &loggingStepClock{step: 25 * time.Millisecond})
	if _, _, err := debugRouter.prepareRPCDispatchContext(context.Background(), [8]byte{1}, 2, 0, "help.getConfig"); err != nil {
		t.Fatal(err)
	}
	entries := debugLogs.FilterMessage("slow pre-handler").All()
	if len(entries) != 1 || entries[0].Level != zap.DebugLevel {
		t.Fatalf("slow pre-handler debug entries=%v, want one Debug entry", entries)
	}
}
