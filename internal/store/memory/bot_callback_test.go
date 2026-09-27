package memory

import (
	"context"
	"testing"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

func TestBotCallbackPutRefusesToOverwritePending(t *testing.T) {
	ctx := context.Background()
	s := NewBotCallbackRegistryStore()
	pending := store.BotCallbackPending{QueryID: 1, BotUserID: 10, UserID: 100}
	ok, err := s.PutBotCallbackPending(ctx, pending, time.Minute)
	if err != nil || !ok {
		t.Fatalf("first Put = %v, %v, want true, nil", ok, err)
	}
	ok, err = s.PutBotCallbackPending(ctx, pending, time.Minute)
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}
	if ok {
		t.Fatal("second Put on the same query id succeeded, want it refused -- query ids must not be overwritten")
	}
}

func TestBotCallbackResolveIsSingleWinner(t *testing.T) {
	ctx := context.Background()
	s := NewBotCallbackRegistryStore()
	pending := store.BotCallbackPending{QueryID: 1, BotUserID: 10, UserID: 100}
	if _, err := s.PutBotCallbackPending(ctx, pending, time.Minute); err != nil {
		t.Fatalf("Put: %v", err)
	}

	const n = 50
	wins := make(chan bool, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			ok, err := s.ResolveBotCallback(ctx, 10, 1, domain.BotCallbackAnswer{Message: "answer"})
			if err != nil {
				t.Errorf("ResolveBotCallback: %v", err)
			}
			wins <- ok
		}(i)
	}
	winners := 0
	for i := 0; i < n; i++ {
		if <-wins {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("%d concurrent resolvers won, want exactly 1 -- at most one answer may win", winners)
	}
}

func TestBotCallbackResolveRefusesWrongBot(t *testing.T) {
	ctx := context.Background()
	s := NewBotCallbackRegistryStore()
	if _, err := s.PutBotCallbackPending(ctx, store.BotCallbackPending{QueryID: 1, BotUserID: 10, UserID: 100}, time.Minute); err != nil {
		t.Fatalf("Put: %v", err)
	}
	ok, err := s.ResolveBotCallback(ctx, 99, 1, domain.BotCallbackAnswer{})
	if err != nil {
		t.Fatalf("ResolveBotCallback: %v", err)
	}
	if ok {
		t.Fatal("a different bot resolved someone else's callback query")
	}
}

func TestBotCallbackGetAndDelete(t *testing.T) {
	ctx := context.Background()
	s := NewBotCallbackRegistryStore()
	if _, err := s.PutBotCallbackPending(ctx, store.BotCallbackPending{QueryID: 1, BotUserID: 10, UserID: 100}, time.Minute); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, found, err := s.GetBotCallbackAnswer(ctx, 10, 1); err != nil || found {
		t.Fatalf("GetBotCallbackAnswer before resolve = found %v err %v, want not found", found, err)
	}
	if _, err := s.ResolveBotCallback(ctx, 10, 1, domain.BotCallbackAnswer{Message: "hi"}); err != nil {
		t.Fatalf("ResolveBotCallback: %v", err)
	}
	answer, found, err := s.GetBotCallbackAnswer(ctx, 10, 1)
	if err != nil || !found || answer.Message != "hi" {
		t.Fatalf("GetBotCallbackAnswer = %+v, %v, %v, want {hi} true nil", answer, found, err)
	}
	if err := s.DeleteBotCallbackPending(ctx, 10, 1); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, _ := s.GetBotCallbackAnswer(ctx, 10, 1); found {
		t.Fatal("answer still found after delete")
	}
}

func TestBotCallbackSubscribeReceivesResolvedAnswer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s := NewBotCallbackRegistryStore()
	if _, err := s.PutBotCallbackPending(ctx, store.BotCallbackPending{QueryID: 1, BotUserID: 10, UserID: 100}, time.Minute); err != nil {
		t.Fatalf("Put: %v", err)
	}

	received := make(chan store.BotCallbackAnswerPush, 1)
	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()
	go func() {
		_ = s.SubscribeBotCallbackAnswers(subCtx, func(_ context.Context, push store.BotCallbackAnswerPush) {
			received <- push
		})
	}()
	// Give the subscriber goroutine a moment to register before resolving --
	// this is a fire-and-forget accelerator, not a durable queue, so a
	// resolve that races ahead of Subscribe is expected to be missed (see
	// the doc comment on publish).
	time.Sleep(20 * time.Millisecond)

	if _, err := s.ResolveBotCallback(ctx, 10, 1, domain.BotCallbackAnswer{Message: "pushed"}); err != nil {
		t.Fatalf("ResolveBotCallback: %v", err)
	}

	select {
	case push := <-received:
		if push.QueryID != 1 || push.BotUserID != 10 || push.Answer.Message != "pushed" {
			t.Fatalf("received push %+v, want QueryID 1 BotUserID 10 Answer.Message pushed", push)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber never received the resolved answer")
	}
}
