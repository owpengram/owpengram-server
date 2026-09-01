package loadharness

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/iamxvbaba/td/tgerr"
)

func TestRPCWithFloodWaitPolicyRetriesTheSameCall(t *testing.T) {
	attempts := 0
	waits := make([]time.Duration, 0, 1)
	policy := floodWaitPolicy{
		maxRetries: 2,
		maxWait:    5 * time.Second,
		padding:    100 * time.Millisecond,
		wait: func(_ context.Context, delay time.Duration) error {
			waits = append(waits, delay)
			return nil
		},
	}
	result, err := rpcWithFloodWaitPolicy(context.Background(), time.Second, policy, func(context.Context) (int, error) {
		attempts++
		if attempts == 1 {
			return 0, tgerr.New(420, "FLOOD_WAIT_2")
		}
		return 42, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != 42 || attempts != 2 {
		t.Fatalf("result=%d attempts=%d", result, attempts)
	}
	if len(waits) != 1 || waits[0] != 2100*time.Millisecond {
		t.Fatalf("waits=%v", waits)
	}
}

func TestRPCWithFloodWaitPolicyDoesNotRetryOrdinaryError(t *testing.T) {
	want := errors.New("boom")
	attempts := 0
	policy := floodWaitPolicy{maxRetries: 2, maxWait: time.Second, wait: waitForContext}
	_, err := rpcWithFloodWaitPolicy(context.Background(), time.Second, policy, func(context.Context) (int, error) {
		attempts++
		return 0, want
	})
	if !errors.Is(err, want) || attempts != 1 {
		t.Fatalf("err=%v attempts=%d", err, attempts)
	}
}

func TestRPCWithFloodWaitPolicyRejectsExcessiveWait(t *testing.T) {
	policy := floodWaitPolicy{maxRetries: 2, maxWait: time.Second, wait: waitForContext}
	_, err := rpcWithFloodWaitPolicy(context.Background(), time.Second, policy, func(context.Context) (int, error) {
		return 0, tgerr.New(420, "FLOOD_WAIT_2")
	})
	if err == nil {
		t.Fatal("excessive FLOOD_WAIT unexpectedly retried")
	}
}
