package loadharness

import (
	"sync"
	"testing"
	"time"

	"github.com/iamxvbaba/td/proto"
)

func TestLoadMessageIDSourceSkipsEmptyClientFraction(t *testing.T) {
	second := time.Unix(1_800_000_000, 0)
	source := newLoadMessageIDSource(func() time.Time { return second })

	first := source.New(proto.MessageFromClient)
	secondID := source.New(proto.MessageFromClient)
	for index, messageID := range []int64{first, secondID} {
		if uint32(messageID) == 0 {
			t.Fatalf("message id %d lower 32 bits are empty", index)
		}
		if proto.MessageID(messageID).Type() != proto.MessageFromClient {
			t.Fatalf("message id %d type = %v, want client", index, proto.MessageID(messageID).Type())
		}
	}
	if secondID <= first {
		t.Fatalf("message ids are not strictly increasing: %d then %d", first, secondID)
	}
}

func TestLoadMessageIDSourceConcurrentMonotonicUnique(t *testing.T) {
	second := time.Unix(1_800_000_000, 0)
	source := newLoadMessageIDSource(func() time.Time { return second })

	const calls = 1_000
	ids := make(chan int64, calls)
	var workers sync.WaitGroup
	workers.Add(calls)
	for range calls {
		go func() {
			defer workers.Done()
			ids <- source.New(proto.MessageFromClient)
		}()
	}
	workers.Wait()
	close(ids)

	seen := make(map[int64]struct{}, calls)
	for messageID := range ids {
		if uint32(messageID) == 0 || proto.MessageID(messageID).Type() != proto.MessageFromClient {
			t.Fatalf("invalid client message id %d", messageID)
		}
		if _, duplicate := seen[messageID]; duplicate {
			t.Fatalf("duplicate client message id %d", messageID)
		}
		seen[messageID] = struct{}{}
	}
	if len(seen) != calls {
		t.Fatalf("unique message ids = %d, want %d", len(seen), calls)
	}
}
