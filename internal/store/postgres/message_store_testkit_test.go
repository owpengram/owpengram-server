package postgres

import (
	"context"
	"sync"
	"telesrv/internal/domain"
)

type fixedBoxIDAllocator struct {
	next int
}

func (a fixedBoxIDAllocator) NextBoxID(context.Context, int64) (int, error) {
	return a.next, nil
}

func (a fixedBoxIDAllocator) NextBoxIDs(_ context.Context, userIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(userIDs))
	for _, userID := range userIDs {
		out[userID] = a.next
	}
	return out, nil
}

func (a fixedBoxIDAllocator) CurrentBoxID(context.Context, int64) (int, error) {
	return a.next, nil
}

type perUserCounterAllocator struct {
	mu     sync.Mutex
	values map[int64]int
}

func (a *perUserCounterAllocator) NextBoxID(_ context.Context, userID int64) (int, error) {
	return a.next(userID), nil
}

func (a *perUserCounterAllocator) NextBoxIDs(_ context.Context, userIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(userIDs))
	for _, userID := range userIDs {
		if _, ok := out[userID]; !ok {
			out[userID] = a.next(userID)
		}
	}
	return out, nil
}

func (a *perUserCounterAllocator) CurrentBoxID(_ context.Context, userID int64) (int, error) {
	return a.current(userID), nil
}

func (a *perUserCounterAllocator) next(userID int64) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.values == nil {
		a.values = map[int64]int{}
	}
	a.values[userID]++
	return a.values[userID]
}

func (a *perUserCounterAllocator) current(userID int64) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.values[userID]
}

func messageIDs(messages []domain.Message) []int {
	out := make([]int, 0, len(messages))
	for _, msg := range messages {
		out = append(out, msg.ID)
	}
	return out
}

func sameInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
