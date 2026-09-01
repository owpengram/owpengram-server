package loadharness

import (
	"context"
	"fmt"
	"time"

	"github.com/iamxvbaba/td/tgerr"
)

const (
	maxDatasetFloodWaitRetries = 16
	maxDatasetFloodWait        = 2 * time.Minute
	datasetFloodWaitPadding    = time.Second
)

type floodWaitPolicy struct {
	maxRetries int
	maxWait    time.Duration
	padding    time.Duration
	wait       func(context.Context, time.Duration) error
}

func defaultFloodWaitPolicy() floodWaitPolicy {
	return floodWaitPolicy{
		maxRetries: maxDatasetFloodWaitRetries,
		maxWait:    maxDatasetFloodWait,
		padding:    datasetFloodWaitPadding,
		wait:       waitForContext,
	}
}

// rpcWithFloodWaitRetry is reserved for dataset preparation. Startup-run must
// observe FLOOD_WAIT as a measured failure instead of hiding it behind a retry.
func rpcWithFloodWaitRetry[T any](
	ctx context.Context,
	timeout time.Duration,
	call func(context.Context) (T, error),
) (T, error) {
	return rpcWithFloodWaitPolicy(ctx, timeout, defaultFloodWaitPolicy(), call)
}

func rpcWithFloodWaitPolicy[T any](
	ctx context.Context,
	timeout time.Duration,
	policy floodWaitPolicy,
	call func(context.Context) (T, error),
) (T, error) {
	var zero T
	if timeout <= 0 {
		return zero, fmt.Errorf("RPC timeout must be positive")
	}
	if policy.maxRetries < 0 || policy.maxWait < 0 || policy.padding < 0 || policy.wait == nil {
		return zero, fmt.Errorf("invalid FLOOD_WAIT retry policy")
	}
	for retry := 0; ; retry++ {
		rpcCtx, cancel := context.WithTimeout(ctx, timeout)
		result, err := call(rpcCtx)
		cancel()
		if err == nil {
			return result, nil
		}
		wait, ok := tgerr.AsFloodWait(err)
		if !ok {
			return zero, err
		}
		if retry >= policy.maxRetries {
			return zero, fmt.Errorf("FLOOD_WAIT retry limit %d exhausted: %w", policy.maxRetries, err)
		}
		wait += policy.padding
		if wait > policy.maxWait {
			return zero, fmt.Errorf("FLOOD_WAIT %s exceeds dataset preparation limit %s: %w", wait, policy.maxWait, err)
		}
		if err := policy.wait(ctx, wait); err != nil {
			return zero, err
		}
	}
}

func waitForContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
