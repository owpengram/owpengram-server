package rpcresult

import (
	"context"
	"sync"

	"github.com/iamxvbaba/td/bin"
)

// ReplaySource rebuilds the exact inner result bytes for one already-executed
// RPC from immutable data. Implementations must not rerun authorization,
// metadata lookup, or business handlers.
type ReplaySource interface {
	EncodeInner(context.Context, *bin.Buffer) error
	RetainedBytes() int
}

// Carrier is implemented by a generated result wrapper when a fresh execution
// proved that its result can be replayed from immutable data.
type Carrier interface {
	ExactReplaySource() ReplaySource
}

// ValueSource is the RPC-side half of a replay source. It may use canonical
// RPC values internally; the edge sees only the encoded ReplaySource above.
type ValueSource interface {
	Value(context.Context) (any, error)
	RetainedBytes() int
}

type captureKey struct{}

type Capture struct {
	mu     sync.Mutex
	source ValueSource
}

func WithCapture(ctx context.Context) (context.Context, *Capture) {
	capture := &Capture{}
	return context.WithValue(ctx, captureKey{}, capture), capture
}

// Publish records the one immutable source produced by the admitted terminal.
// Wrapper or nested handlers must not replace a source already published by
// the same one-shot dispatch.
func Publish(ctx context.Context, source ValueSource) bool {
	if source == nil {
		return false
	}
	capture, _ := ctx.Value(captureKey{}).(*Capture)
	if capture == nil {
		return false
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if capture.source != nil {
		return false
	}
	capture.source = source
	return true
}

func (c *Capture) Take() ValueSource {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	source := c.source
	c.source = nil
	c.mu.Unlock()
	return source
}
