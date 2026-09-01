package postgres

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
)

// userLaneActor moves same-user admission waits out of PostgreSQL. It grants a
// request only when all of its user lanes are free, while allowing unrelated
// requests to bypass a blocked waiter. PostgreSQL advisory locks remain the
// cross-process correctness fence after admission.
type userLaneActor struct {
	nextID   atomic.Uint64
	commands chan any
}

type userLaneAcquire struct {
	id      uint64
	userIDs []int64
	granted chan struct{}
}

type userLaneCancel struct {
	id   uint64
	done chan struct{}
}

type userLaneRelease struct{ id uint64 }

var defaultPrivateSendLaneActor = newUserLaneActor()

func newUserLaneActor() *userLaneActor {
	actor := &userLaneActor{commands: make(chan any, 1024)}
	go actor.run()
	return actor
}

func (a *userLaneActor) acquire(ctx context.Context, userIDs ...int64) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	keys := normalizedUserLaneIDs(userIDs)
	if len(keys) == 0 {
		return func() {}, nil
	}
	request := userLaneAcquire{
		id:      a.nextID.Add(1),
		userIDs: keys,
		granted: make(chan struct{}),
	}
	select {
	case a.commands <- request:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-request.granted:
		var once sync.Once
		return func() {
			once.Do(func() { a.commands <- userLaneRelease{id: request.id} })
		}, nil
	case <-ctx.Done():
		canceled := userLaneCancel{id: request.id, done: make(chan struct{})}
		a.commands <- canceled
		<-canceled.done
		return nil, ctx.Err()
	}
}

func normalizedUserLaneIDs(userIDs []int64) []int64 {
	unique := make([]int64, 0, len(userIDs))
	seen := make(map[int64]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID <= 0 {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		unique = append(unique, userID)
	}
	sort.Slice(unique, func(i, j int) bool { return unique[i] < unique[j] })
	return unique
}

func (a *userLaneActor) run() {
	held := make(map[int64]uint64)
	granted := make(map[uint64][]int64)
	pending := make([]userLaneAcquire, 0, 256)

	release := func(id uint64) {
		for _, userID := range granted[id] {
			if held[userID] == id {
				delete(held, userID)
			}
		}
		delete(granted, id)
	}
	drain := func() {
		for index := 0; index < len(pending); {
			request := pending[index]
			available := true
			for _, userID := range request.userIDs {
				if _, busy := held[userID]; busy {
					available = false
					break
				}
			}
			if !available {
				index++
				continue
			}
			for _, userID := range request.userIDs {
				held[userID] = request.id
			}
			granted[request.id] = request.userIDs
			close(request.granted)
			copy(pending[index:], pending[index+1:])
			pending = pending[:len(pending)-1]
		}
	}

	for command := range a.commands {
		switch value := command.(type) {
		case userLaneAcquire:
			pending = append(pending, value)
			drain()
		case userLaneRelease:
			release(value.id)
			drain()
		case userLaneCancel:
			found := false
			for index := range pending {
				if pending[index].id != value.id {
					continue
				}
				copy(pending[index:], pending[index+1:])
				pending = pending[:len(pending)-1]
				found = true
				break
			}
			if !found {
				release(value.id)
			}
			close(value.done)
			drain()
		}
	}
}
