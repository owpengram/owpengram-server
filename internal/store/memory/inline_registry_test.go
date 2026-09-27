package memory

import (
	"context"
	"testing"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

func TestInlineRegistryPendingRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := NewInlineRegistryStore()
	pending := store.InlinePending{QueryID: 1, BotUserID: 10, UserID: 100}
	if err := s.PutInlinePending(ctx, pending, time.Minute); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, found, err := s.GetInlinePending(ctx, 1)
	if err != nil || !found || got.BotUserID != 10 {
		t.Fatalf("Get = %+v, %v, %v, want the stored pending", got, found, err)
	}
	if err := s.DeleteInlinePending(ctx, 1); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, _ := s.GetInlinePending(ctx, 1); found {
		t.Fatal("pending still found after delete")
	}
}

func TestInlineRegistryResultExpires(t *testing.T) {
	ctx := context.Background()
	s := NewInlineRegistryStore()
	results := domain.BotInlineResults{QueryID: 5, BotUserID: 10, UserID: 100}
	if err := s.PutInlineResult(ctx, results, 5*time.Millisecond); err != nil {
		t.Fatalf("Put: %v", err)
	}
	time.Sleep(15 * time.Millisecond)
	if _, found, _ := s.GetInlineResult(ctx, 5); found {
		t.Fatal("result survived past its ttl")
	}
}

func TestInlineRegistryCacheReturnsRemainingTTL(t *testing.T) {
	ctx := context.Background()
	s := NewInlineRegistryStore()
	key := store.InlineCacheKey{BotUserID: 10, UserID: 100, Query: "q"}
	if err := s.PutInlineCache(ctx, key, domain.BotInlineResults{QueryID: 999, BotUserID: 10, UserID: 100}, 200*time.Millisecond); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, found, ttl, err := s.GetInlineCache(ctx, key)
	if err != nil || !found {
		t.Fatalf("Get = found %v err %v", found, err)
	}
	if got.QueryID != 0 {
		t.Fatalf("cached QueryID = %d, want 0 -- a cache entry has no single owning query", got.QueryID)
	}
	if ttl <= 0 || ttl > time.Minute {
		t.Fatalf("remaining ttl = %v, want a small positive duration", ttl)
	}
}

func TestInlineRegistryWebDocumentPreservesBytesOnMetadataUpdate(t *testing.T) {
	ctx := context.Background()
	s := NewInlineRegistryStore()
	doc := domain.BotInlineWebDocument{URL: "https://example/x", AccessHash: 1, MimeType: "image/png"}
	key := store.InlineWebDocumentKey{URL: doc.URL, AccessHash: doc.AccessHash}
	if err := s.PutInlineWebDocument(ctx, doc, time.Minute); err != nil {
		t.Fatalf("PutInlineWebDocument: %v", err)
	}
	if err := s.PutInlineWebDocumentBytes(ctx, key, []byte("payload"), "image/png", time.Minute); err != nil {
		t.Fatalf("PutInlineWebDocumentBytes: %v", err)
	}
	// Re-putting the metadata (e.g. a second inline result referencing the
	// same URL) must not drop the bytes already attached.
	if err := s.PutInlineWebDocument(ctx, doc, time.Minute); err != nil {
		t.Fatalf("PutInlineWebDocument (again): %v", err)
	}
	entry, found, err := s.GetInlineWebDocument(ctx, key)
	if err != nil || !found {
		t.Fatalf("Get = found %v err %v", found, err)
	}
	if string(entry.Bytes) != "payload" {
		t.Fatalf("bytes = %q, want %q to survive a metadata-only re-Put", entry.Bytes, "payload")
	}
}

func TestInlineRegistryWebDocumentBytesRequireExistingDocument(t *testing.T) {
	ctx := context.Background()
	s := NewInlineRegistryStore()
	key := store.InlineWebDocumentKey{URL: "https://example/none", AccessHash: 1}
	if err := s.PutInlineWebDocumentBytes(ctx, key, []byte("x"), "image/png", time.Minute); err == nil {
		t.Fatal("PutInlineWebDocumentBytes succeeded with no prior PutInlineWebDocument, want an error")
	}
}

func TestInlineRegistryPreparedMessageRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := NewInlineRegistryStore()
	msg := store.PreparedInlineMessage{ID: "abc", BotUserID: 10, UserID: 100,
		Results: domain.BotInlineResults{Results: []domain.BotInlineResult{{ID: "r1"}}}}
	if err := s.PutPreparedInlineMessage(ctx, msg, time.Minute); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, found, err := s.GetPreparedInlineMessage(ctx, "abc")
	if err != nil || !found || len(got.Results.Results) != 1 {
		t.Fatalf("Get = %+v, %v, %v", got, found, err)
	}
}

func TestInlineRegistryWebViewSessionDualIndex(t *testing.T) {
	ctx := context.Background()
	s := NewInlineRegistryStore()
	session := store.WebViewSession{QueryID: 1, BotQueryID: "bq1", BotUserID: 10, UserID: 100, Peer: domain.Peer{Type: domain.PeerTypeUser, ID: 100}}
	if err := s.PutWebViewSession(ctx, session, time.Minute); err != nil {
		t.Fatalf("Put: %v", err)
	}
	byQuery, found, err := s.GetWebViewSession(ctx, 1)
	if err != nil || !found || byQuery.BotQueryID != "bq1" {
		t.Fatalf("GetWebViewSession = %+v, %v, %v", byQuery, found, err)
	}
	byBotQuery, found, err := s.GetWebViewSessionByBotQuery(ctx, "bq1")
	if err != nil || !found || byBotQuery.QueryID != 1 {
		t.Fatalf("GetWebViewSessionByBotQuery = %+v, %v, %v", byBotQuery, found, err)
	}
	if err := s.DeleteWebViewSession(ctx, 1, "bq1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, _ := s.GetWebViewSession(ctx, 1); found {
		t.Fatal("session still found by query id after delete")
	}
	if _, found, _ := s.GetWebViewSessionByBotQuery(ctx, "bq1"); found {
		t.Fatal("session still found by bot query id after delete -- both indexes must be deleted together")
	}
}

func TestInlineRegistryPublishSubscribe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s := NewInlineRegistryStore()

	received := make(chan store.BotInlineQueryPush, 1)
	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()
	go func() {
		_ = s.SubscribeBotInlineQueries(subCtx, func(_ context.Context, push store.BotInlineQueryPush) {
			received <- push
		})
	}()
	time.Sleep(20 * time.Millisecond)

	event := store.BotInlineQueryPush{SourceID: "srv1", QueryID: 1, BotUserID: 10, UserID: 100}
	if err := s.PublishBotInlineQuery(ctx, event); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case push := <-received:
		if push.QueryID != 1 {
			t.Fatalf("received %+v, want QueryID 1", push)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber never received the published query")
	}
}

func TestInlineRegistryPublishRejectsMissingIdentity(t *testing.T) {
	ctx := context.Background()
	s := NewInlineRegistryStore()
	if err := s.PublishBotInlineQuery(ctx, store.BotInlineQueryPush{}); err == nil {
		t.Fatal("Publish with a zero-value event succeeded, want it rejected")
	}
}

func TestInlineRegistrySweepCoversEverySubResource(t *testing.T) {
	ctx := context.Background()
	s := NewInlineRegistryStore()
	if err := s.PutInlinePending(ctx, store.InlinePending{QueryID: 1, BotUserID: 1, UserID: 1}, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := s.PutInlineResult(ctx, domain.BotInlineResults{QueryID: 1, BotUserID: 1, UserID: 1}, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if removed := s.sweep(time.Now()); removed < 2 {
		t.Fatalf("sweep removed %d entries across sub-resources, want at least 2", removed)
	}
}
