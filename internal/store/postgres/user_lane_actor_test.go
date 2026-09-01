package postgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestUserLaneActorSerializesOverlapsAndAllowsUnrelatedBypass(t *testing.T) {
	actor := newUserLaneActor()
	releaseA, err := actor.acquire(context.Background(), 1, 2)
	if err != nil {
		t.Fatal(err)
	}

	overlapGranted := make(chan func(), 1)
	go func() {
		release, err := actor.acquire(context.Background(), 2, 3)
		if err == nil {
			overlapGranted <- release
		}
	}()
	select {
	case release := <-overlapGranted:
		release()
		t.Fatal("overlapping request was granted while lane 2 was held")
	case <-time.After(20 * time.Millisecond):
	}

	unrelatedCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	releaseUnrelated, err := actor.acquire(unrelatedCtx, 4, 5)
	if err != nil {
		t.Fatalf("unrelated request did not bypass blocked waiter: %v", err)
	}
	releaseUnrelated()
	releaseA()
	select {
	case release := <-overlapGranted:
		release()
	case <-time.After(time.Second):
		t.Fatal("overlapping request did not resume after release")
	}
}

func TestUserLaneActorCancellationRemovesPendingRequest(t *testing.T) {
	actor := newUserLaneActor()
	release, err := actor.acquire(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := actor.acquire(ctx, 10, 11)
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquire error = %v", err)
	}
	release()
	availableCtx, availableCancel := context.WithTimeout(context.Background(), time.Second)
	defer availableCancel()
	releaseNext, err := actor.acquire(availableCtx, 11)
	if err != nil {
		t.Fatalf("canceled waiter retained lane 11: %v", err)
	}
	releaseNext()
}
