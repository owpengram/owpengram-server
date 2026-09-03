package postgres

import (
	"context"
	"strconv"
	"testing"
	"time"

	"telesrv/internal/domain"
)

// TestRetentionPurgeBypassProducesServiceMessage_PrivateMessage exercises
// Part 1 of the storage retention v2 plan end-to-end against a real
// Postgres: EditMessage with RetentionPurge=true bypasses the ordinary
// author check (the storage retention sweep is not the message's author) and
// replaces the message media with a messageActionCustomAction service
// payload, and a second retention-purge edit on the now-already-service
// message is a no-op (ErrMessageNotModified) -- guarding the idempotency
// guard that keeps a shared media_references duplicate (owner+peer both
// referencing the same purged document) from double-editing the same box.
func TestRetentionPurgeBypassProducesServiceMessage_PrivateMessage(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)

	users := NewUserStore(pool)
	sender, err := users.Create(ctx, domain.User{AccessHash: 81, Phone: "+1997" + suffix + "01", FirstName: "RetentionSender"})
	if err != nil {
		t.Fatalf("create sender: %v", err)
	}
	recipient, err := users.Create(ctx, domain.User{AccessHash: 82, Phone: "+1997" + suffix + "02", FirstName: "RetentionRecipient"})
	if err != nil {
		t.Fatalf("create recipient: %v", err)
	}
	ids := []int64{sender.ID, recipient.ID}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM message_boxes WHERE owner_user_id = ANY($1::bigint[])", ids)
		_, _ = pool.Exec(ctx, "DELETE FROM private_messages WHERE sender_user_id = ANY($1::bigint[])", ids)
		_, _ = pool.Exec(ctx, "DELETE FROM dialogs WHERE user_id = ANY($1::bigint[])", ids)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1::bigint[])", ids)
	})

	messages := NewMessageStore(pool, WithMessageAllocators(&perUserCounterAllocator{}))
	sent, err := messages.SendPrivateText(ctx, domain.SendPrivateTextRequest{
		SenderUserID:    sender.ID,
		RecipientUserID: recipient.ID,
		RandomID:        time.Now().UnixNano(),
		Message:         "a file worth keeping",
		Media: &domain.MessageMedia{
			Kind:     domain.MessageMediaKindDocument,
			Document: &domain.Document{ID: time.Now().UnixNano(), AccessHash: 1, MimeType: "application/octet-stream"},
		},
		Date: int(time.Now().Unix()),
	})
	if err != nil {
		t.Fatalf("send private media: %v", err)
	}

	noticeMedia := &domain.MessageMedia{
		Kind: domain.MessageMediaKindService,
		ServiceAction: &domain.MessageServiceAction{
			Kind: domain.MessageServiceActionCustomText,
			Text: domain.RetentionPurgeNoticeText,
		},
	}
	req := domain.EditMessageRequest{
		OwnerUserID:    sender.ID,
		Peer:           domain.Peer{Type: domain.PeerTypeUser, ID: recipient.ID},
		ID:             sent.SenderMessage.ID,
		SetRichMessage: true,
		Media:          noticeMedia,
		RetentionPurge: true,
	}
	res, err := messages.EditMessage(ctx, req)
	if err != nil {
		t.Fatalf("EditMessage(RetentionPurge): %v", err)
	}
	self := res.Self()
	if self.Message.Media == nil || self.Message.Media.Kind != domain.MessageMediaKindService {
		t.Fatalf("edited message media = %+v, want service kind", self.Message.Media)
	}
	if self.Message.Media.ServiceAction == nil || self.Message.Media.ServiceAction.Kind != domain.MessageServiceActionCustomText {
		t.Fatalf("edited message service action = %+v, want custom_text", self.Message.Media.ServiceAction)
	}
	if self.Message.Media.ServiceAction.Text != domain.RetentionPurgeNoticeText {
		t.Fatalf("edited message notice text = %q, want %q", self.Message.Media.ServiceAction.Text, domain.RetentionPurgeNoticeText)
	}

	// A second retention-purge edit is a no-op: the message is already the
	// notice, so this must not keep bumping pts / re-writing it forever.
	if _, err := messages.EditMessage(ctx, req); err != domain.ErrMessageNotModified {
		t.Fatalf("second RetentionPurge edit err = %v, want ErrMessageNotModified", err)
	}

	// A non-author, non-bypass edit on someone else's message must still be
	// rejected -- RetentionPurge only bypasses the author check when it is
	// actually set, not for ordinary edits in general.
	ordinary := req
	ordinary.RetentionPurge = false
	ordinary.OwnerUserID = recipient.ID
	ordinary.Peer = domain.Peer{Type: domain.PeerTypeUser, ID: sender.ID}
	if _, err := messages.EditMessage(ctx, ordinary); err != domain.ErrMessageAuthorRequired {
		t.Fatalf("non-bypass edit by non-author err = %v, want ErrMessageAuthorRequired", err)
	}
}

// TestRetentionPurgeBypassProducesServiceMessage_ChannelMessage is the
// channel counterpart of the private-message test above: channel messages
// store their service action in a structurally separate Action column (see
// tgChannelMessage), so applyRetentionPurgeChannelMessage must clear body/
// media and set Action instead of replacing Media like the private path.
func TestRetentionPurgeBypassProducesServiceMessage_ChannelMessage(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)

	users := NewUserStore(pool)
	owner, err := users.Create(ctx, domain.User{AccessHash: 83, Phone: "+1997" + suffix + "03", FirstName: "RetentionChOwner"})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", owner.ID) })

	channels := NewChannelStore(pool)
	created, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{CreatorUserID: owner.ID, Title: "Retention " + suffix, Megagroup: true, Date: 1700003000})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channelID := created.Channel.ID
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id = $1", channelID) })

	sent, err := channels.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
		UserID: owner.ID, ChannelID: channelID, RandomID: 771122, Message: "old file",
		Media: &domain.MessageMedia{Kind: domain.MessageMediaKindDocument, Document: &domain.Document{ID: time.Now().UnixNano(), AccessHash: 1}},
		Date:  1700003001,
	})
	if err != nil {
		t.Fatalf("send channel message: %v", err)
	}

	req := domain.EditChannelMessageRequest{
		ChannelID:            channelID,
		ID:                   sent.Message.ID,
		RetentionPurge:       true,
		RetentionPurgeAction: &domain.ChannelMessageAction{Type: domain.ChannelActionCustomText, Text: domain.RetentionPurgeNoticeText},
	}
	res, err := channels.EditChannelMessage(ctx, req)
	if err != nil {
		t.Fatalf("EditChannelMessage(RetentionPurge): %v", err)
	}
	if res.Message.Action == nil || res.Message.Action.Type != domain.ChannelActionCustomText {
		t.Fatalf("edited channel message action = %+v, want custom_text", res.Message.Action)
	}
	if res.Message.Action.Text != domain.RetentionPurgeNoticeText {
		t.Fatalf("edited channel message notice text = %q, want %q", res.Message.Action.Text, domain.RetentionPurgeNoticeText)
	}
	if res.Message.Body != "" || !res.Message.Media.IsZero() {
		t.Fatalf("edited channel message body/media not cleared: body=%q media=%+v", res.Message.Body, res.Message.Media)
	}

	// Idempotency guard: a second retention-purge edit on an already-service
	// message must not succeed again.
	if _, err := channels.EditChannelMessage(ctx, req); err != domain.ErrMessageNotModified {
		t.Fatalf("second RetentionPurge channel edit err = %v, want ErrMessageNotModified", err)
	}

	// Read-back via ListChannelHistory renders as a service message too.
	hist, err := channels.ListChannelHistory(ctx, owner.ID, domain.ChannelHistoryFilter{ChannelID: channelID, Limit: 10})
	if err != nil {
		t.Fatalf("list channel history: %v", err)
	}
	var found bool
	for _, m := range hist.Messages {
		if m.ID == sent.Message.ID {
			found = true
			if m.Action == nil || m.Action.Type != domain.ChannelActionCustomText {
				t.Fatalf("history action = %+v, want custom_text", m.Action)
			}
		}
	}
	if !found {
		t.Fatalf("purged message %d not found in channel history", sent.Message.ID)
	}
}

// TestDocumentCategoryPopulatedOnSend guards Part 2's schema/write-path
// change: sending a document message stamps the already-computed
// domain.DocumentMediaCategory value directly onto the documents row, at the
// same point the shared message_box_media index is written -- zero new
// classification logic.
func TestDocumentCategoryPopulatedOnSend(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)

	users := NewUserStore(pool)
	sender, err := users.Create(ctx, domain.User{AccessHash: 84, Phone: "+1997" + suffix + "04", FirstName: "CatSender"})
	if err != nil {
		t.Fatalf("create sender: %v", err)
	}
	recipient, err := users.Create(ctx, domain.User{AccessHash: 85, Phone: "+1997" + suffix + "05", FirstName: "CatRecipient"})
	if err != nil {
		t.Fatalf("create recipient: %v", err)
	}
	ids := []int64{sender.ID, recipient.ID}
	docID := time.Now().UnixNano()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM message_boxes WHERE owner_user_id = ANY($1::bigint[])", ids)
		_, _ = pool.Exec(ctx, "DELETE FROM private_messages WHERE sender_user_id = ANY($1::bigint[])", ids)
		_, _ = pool.Exec(ctx, "DELETE FROM dialogs WHERE user_id = ANY($1::bigint[])", ids)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1::bigint[])", ids)
		_, _ = pool.Exec(ctx, "DELETE FROM documents WHERE id = $1", docID)
		_, _ = pool.Exec(ctx, "DELETE FROM media_references WHERE media_kind = 'document' AND media_id = $1", docID)
	})

	media := NewMediaStore(pool)
	if err := media.PutDocument(ctx, domain.Document{ID: docID, MimeType: "video/mp4", Size: 2048}); err != nil {
		t.Fatalf("PutDocument: %v", err)
	}

	messages := NewMessageStore(pool, WithMessageAllocators(&perUserCounterAllocator{}))
	_, err = messages.SendPrivateText(ctx, domain.SendPrivateTextRequest{
		SenderUserID:    sender.ID,
		RecipientUserID: recipient.ID,
		RandomID:        time.Now().UnixNano(),
		Media: &domain.MessageMedia{
			Kind: domain.MessageMediaKindDocument,
			Document: &domain.Document{
				ID: docID, AccessHash: 1, MimeType: "video/mp4",
				Attributes: []domain.DocumentAttribute{{Kind: domain.DocAttrVideo, W: 640, H: 480, Duration: 5}},
			},
		},
		Date: int(time.Now().Unix()),
	})
	if err != nil {
		t.Fatalf("send video document: %v", err)
	}

	var category int16
	if err := pool.QueryRow(ctx, "SELECT category FROM documents WHERE id = $1", docID).Scan(&category); err != nil {
		t.Fatalf("read document category: %v", err)
	}
	if domain.MediaCategory(category) != domain.MediaCategoryVideo {
		t.Fatalf("document category = %d, want %d (Video)", category, domain.MediaCategoryVideo)
	}
}

// TestHardRetentionCategoryFilterOnlyReturnsMatchingCategory guards Part 2's
// query-layer change: ListDocumentIDsForHardRetentionOlderThan now takes a
// category argument and must only return documents in that exact bucket,
// even when another old, blob-owning document sits in a different category.
func TestHardRetentionCategoryFilterOnlyReturnsMatchingCategory(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	media := NewMediaStore(pool)

	videoID := time.Now().UnixNano()
	musicID := videoID + 1
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM documents WHERE id = ANY($1::bigint[])", []int64{videoID, musicID})
		_, _ = pool.Exec(context.Background(), "DELETE FROM file_blobs WHERE location_key = ANY($1::text[])",
			[]string{"doc:" + strconv.FormatInt(videoID, 10), "doc:" + strconv.FormatInt(musicID, 10)})
	})

	for _, d := range []struct {
		id       int64
		category domain.MediaCategory
	}{{videoID, domain.MediaCategoryVideo}, {musicID, domain.MediaCategoryMusic}} {
		if err := media.PutDocument(ctx, domain.Document{ID: d.id, MimeType: "application/octet-stream", Size: 512}); err != nil {
			t.Fatalf("PutDocument %d: %v", d.id, err)
		}
		if _, err := pool.Exec(ctx, "UPDATE documents SET created_at = now() - interval '100 days', category = $2 WHERE id = $1", d.id, int16(d.category)); err != nil {
			t.Fatalf("backdate/categorize document %d: %v", d.id, err)
		}
		blob := postgresTestBlob("doc:"+strconv.FormatInt(d.id, 10), "cat-filter", 512, "application/octet-stream")
		if err := media.PutFileBlob(ctx, blob); err != nil {
			t.Fatalf("PutFileBlob %d: %v", d.id, err)
		}
	}

	cutoff := time.Now().Add(-24 * time.Hour)
	videoIDs, err := media.ListDocumentIDsForHardRetentionOlderThan(ctx, domain.MediaCategoryVideo, &cutoff, 1000)
	if err != nil {
		t.Fatalf("ListDocumentIDsForHardRetentionOlderThan(Video): %v", err)
	}
	if !containsInt64(videoIDs, videoID) || containsInt64(videoIDs, musicID) {
		t.Fatalf("video-category candidates = %v, want to include %d and exclude %d", videoIDs, videoID, musicID)
	}

	musicIDs, err := media.ListDocumentIDsForHardRetentionOlderThan(ctx, domain.MediaCategoryMusic, &cutoff, 1000)
	if err != nil {
		t.Fatalf("ListDocumentIDsForHardRetentionOlderThan(Music): %v", err)
	}
	if !containsInt64(musicIDs, musicID) || containsInt64(musicIDs, videoID) {
		t.Fatalf("music-category candidates = %v, want to include %d and exclude %d", musicIDs, musicID, videoID)
	}
}

// TestManualPurgeNilCutoffIgnoresAge guards the manual purge admin action's
// query-layer contract: a nil cutoff on the hard-retention List/Count
// queries must mean "no age filter at all" -- a document created just now is
// still a candidate -- while a real, non-nil cutoff still filters normally.
// This is the query-layer half of files.Service.ManualPurge/CountManualPurgeCandidates's
// "no date = everything matching the categories, regardless of age" contract
// (the automatic sweep, by contrast, never passes a nil cutoff).
func TestManualPurgeNilCutoffIgnoresAge(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	media := NewMediaStore(pool)

	freshID := time.Now().UnixNano()
	locationKey := "doc:" + strconv.FormatInt(freshID, 10)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM documents WHERE id = $1", freshID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM file_blobs WHERE location_key = $1", locationKey)
	})

	if err := media.PutDocument(ctx, domain.Document{ID: freshID, MimeType: "application/octet-stream", Size: 128}); err != nil {
		t.Fatalf("PutDocument: %v", err)
	}
	// Deliberately NOT backdated -- created_at stays "just now", unlike every
	// other hard-retention test in this file, since the whole point here is
	// that a nil cutoff must not exempt a fresh document.
	if _, err := pool.Exec(ctx, "UPDATE documents SET category = $2 WHERE id = $1", freshID, int16(domain.MediaCategoryFile)); err != nil {
		t.Fatalf("categorize document: %v", err)
	}
	blob := postgresTestBlob(locationKey, "manual-purge-nil-cutoff", 128, "application/octet-stream")
	if err := media.PutFileBlob(ctx, blob); err != nil {
		t.Fatalf("PutFileBlob: %v", err)
	}

	// nil cutoff: the fresh document IS a candidate.
	ids, err := media.ListDocumentIDsForHardRetentionOlderThan(ctx, domain.MediaCategoryFile, nil, 1000)
	if err != nil {
		t.Fatalf("ListDocumentIDsForHardRetentionOlderThan(nil cutoff): %v", err)
	}
	if !containsInt64(ids, freshID) {
		t.Fatalf("nil-cutoff candidates = %v, want to include fresh document %d", ids, freshID)
	}
	count, err := media.CountDocumentsForHardRetention(ctx, domain.MediaCategoryFile, nil)
	if err != nil {
		t.Fatalf("CountDocumentsForHardRetention(nil cutoff): %v", err)
	}
	if count < 1 {
		t.Fatalf("nil-cutoff count = %d, want >= 1 (must include fresh document)", count)
	}

	// A real cutoff in the past still excludes the fresh document.
	pastCutoff := time.Now().Add(-24 * time.Hour)
	ids, err = media.ListDocumentIDsForHardRetentionOlderThan(ctx, domain.MediaCategoryFile, &pastCutoff, 1000)
	if err != nil {
		t.Fatalf("ListDocumentIDsForHardRetentionOlderThan(past cutoff): %v", err)
	}
	if containsInt64(ids, freshID) {
		t.Fatalf("past-cutoff candidates = %v, want to exclude fresh document %d", ids, freshID)
	}
	count, err = media.CountDocumentsForHardRetention(ctx, domain.MediaCategoryFile, &pastCutoff)
	if err != nil {
		t.Fatalf("CountDocumentsForHardRetention(past cutoff): %v", err)
	}
	// Some other, unrelated old File-category document could in principle
	// exist in this shared test database, so this only asserts the fresh
	// document itself dropped out, via the id-level checks above; the count
	// assertion below just guards against the query going the wrong
	// direction entirely (e.g. cutoff being ignored and still counting
	// everything).
	allCount, err := media.CountDocumentsForHardRetention(ctx, domain.MediaCategoryFile, nil)
	if err != nil {
		t.Fatalf("CountDocumentsForHardRetention(nil, recheck): %v", err)
	}
	if count > allCount {
		t.Fatalf("past-cutoff count %d > nil-cutoff count %d, cutoff filter is backwards", count, allCount)
	}
}

// TestManualPurgePhotoAndAvatarNilCutoffIgnoresAge is the photo/avatar
// counterpart of TestManualPurgeNilCutoffIgnoresAge.
func TestManualPurgePhotoAndAvatarNilCutoffIgnoresAge(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	media := NewMediaStore(pool)

	photoID := time.Now().UnixNano()
	locationKey := "photo:" + strconv.FormatInt(photoID, 10)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM photos WHERE id = $1", photoID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM file_blobs WHERE location_key = $1", locationKey)
	})

	if err := media.PutPhoto(ctx, domain.Photo{ID: photoID, AccessHash: 1}); err != nil {
		t.Fatalf("PutPhoto: %v", err)
	}
	blob := postgresTestBlob(locationKey, "manual-purge-photo-nil-cutoff", 64, "image/jpeg")
	if err := media.PutFileBlob(ctx, blob); err != nil {
		t.Fatalf("PutFileBlob: %v", err)
	}

	ids, err := media.ListPhotoIDsForHardRetentionOlderThan(ctx, nil, 1000)
	if err != nil {
		t.Fatalf("ListPhotoIDsForHardRetentionOlderThan(nil cutoff): %v", err)
	}
	if !containsInt64(ids, photoID) {
		t.Fatalf("nil-cutoff photo candidates = %v, want to include fresh photo %d", ids, photoID)
	}

	pastCutoff := time.Now().Add(-24 * time.Hour)
	ids, err = media.ListPhotoIDsForHardRetentionOlderThan(ctx, &pastCutoff, 1000)
	if err != nil {
		t.Fatalf("ListPhotoIDsForHardRetentionOlderThan(past cutoff): %v", err)
	}
	if containsInt64(ids, photoID) {
		t.Fatalf("past-cutoff photo candidates = %v, want to exclude fresh photo %d", ids, photoID)
	}
}

// TestEvictionListsOldestAcrossDocumentsAndPhotos guards Part 3's eviction
// query layer: ListOldestMediaForEviction must return candidates from both
// tables (interleaving by created_at is done by the caller in Go, see
// files.Service.EvictOldestMediaOverBudget) so the oldest-overall item can be
// picked regardless of which table it lives in.
func TestEvictionListsOldestAcrossDocumentsAndPhotos(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	media := NewMediaStore(pool)

	docID := time.Now().UnixNano()
	photoID := docID + 1
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM documents WHERE id = $1", docID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM photos WHERE id = $1", photoID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM file_blobs WHERE location_key = ANY($1::text[])",
			[]string{"doc:" + strconv.FormatInt(docID, 10), "photo:" + strconv.FormatInt(photoID, 10)})
	})

	if err := media.PutDocument(ctx, domain.Document{ID: docID, MimeType: "application/octet-stream", Size: 256}); err != nil {
		t.Fatalf("PutDocument: %v", err)
	}
	// The document is the older of the two (200 days vs 100 days) -- it must
	// sort before the photo in the merged eviction candidate list.
	if _, err := pool.Exec(ctx, "UPDATE documents SET created_at = now() - interval '200 days' WHERE id = $1", docID); err != nil {
		t.Fatalf("backdate document: %v", err)
	}
	if err := media.PutFileBlob(ctx, postgresTestBlob("doc:"+strconv.FormatInt(docID, 10), "evict-doc", 256, "application/octet-stream")); err != nil {
		t.Fatalf("PutFileBlob doc: %v", err)
	}

	if err := media.PutPhoto(ctx, domain.Photo{ID: photoID, AccessHash: 1}); err != nil {
		t.Fatalf("PutPhoto: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE photos SET created_at = now() - interval '100 days' WHERE id = $1", photoID); err != nil {
		t.Fatalf("backdate photo: %v", err)
	}
	if err := media.PutFileBlob(ctx, postgresTestBlob("photo:"+strconv.FormatInt(photoID, 10), "evict-photo", 256, "image/jpeg")); err != nil {
		t.Fatalf("PutFileBlob photo: %v", err)
	}

	candidates, err := media.ListOldestMediaForEviction(ctx, 1000)
	if err != nil {
		t.Fatalf("ListOldestMediaForEviction: %v", err)
	}
	var doc, photo *domain.EvictionCandidate
	for i := range candidates {
		c := candidates[i]
		switch {
		case c.Kind == domain.MediaKindDocument && c.MediaID == docID:
			doc = &candidates[i]
		case c.Kind == domain.MediaKindPhoto && c.MediaID == photoID:
			photo = &candidates[i]
		}
	}
	if doc == nil || photo == nil {
		t.Fatalf("eviction candidates missing doc/photo: %+v", candidates)
	}
	if !doc.CreatedAt.Before(photo.CreatedAt) {
		t.Fatalf("document created_at %v not before photo created_at %v (document should be older)", doc.CreatedAt, photo.CreatedAt)
	}
}
