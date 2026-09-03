package files

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"telesrv/internal/domain"
)

// RetentionPurgeMessageEditor is the private-message capability the storage
// retention sweep needs to turn a hard-retention/eviction blob purge into a
// visible messageActionCustomAction notice. Satisfied by
// internal/app/messages.Service (GetMessages resolves the box's peer --
// EditMessageRequest requires it, and message_box ref_key only encodes
// owner_user_id+box_id -- then EditMessage performs the actual in-place
// edit with RetentionPurge set).
type RetentionPurgeMessageEditor interface {
	GetMessages(ctx context.Context, userID int64, ids []int) (domain.MessageList, error)
	EditMessage(ctx context.Context, userID int64, req domain.EditMessageRequest) (domain.EditMessageResult, error)
}

// RetentionPurgeChannelEditor is the channel-message counterpart of
// RetentionPurgeMessageEditor. Satisfied by internal/app/channels.Service.
// Unlike private messages, EditChannelMessageRequest needs no peer lookup --
// channelMessageRefKey already encodes the channel id directly. Uses
// EditChannelMessageInternal (not the ordinary EditMessage) since this is a
// server-internal edit with no acting user/channel member behind it.
type RetentionPurgeChannelEditor interface {
	EditChannelMessageInternal(ctx context.Context, req domain.EditChannelMessageRequest) (domain.EditChannelMessageResult, error)
}

// SetRetentionPurgeNotifier wires the private-message/channel-message edit
// capability the storage retention sweep needs to turn a hard-retention/
// eviction blob purge into a visible messageActionCustomAction notice on
// every message still embedding the purged document/photo. Both app-layer
// services are constructed after this Service in cmd/telesrv/main.go, so
// this is a post-construction setter rather than a NewService option --
// cmd/telesrv/main.go deliberately calls this BEFORE starting the
// retentionWorker.Run goroutine, since a sweep tick that raced ahead of this
// call would find both fields nil and permanently lose the notice for
// whatever it purged that tick (a purged document/photo never again matches
// the hard-retention candidate query, so there's no later tick to retry the
// notice on). If both fields are still nil here for some other reason (e.g.
// a future caller reordering), the notice is best-effort and silently
// skipped, same as any other per-reference failure below -- the underlying
// blob purge has already committed and must never be undone or retried
// because of a notice failure.
func (s *Service) SetRetentionPurgeNotifier(messages RetentionPurgeMessageEditor, channels RetentionPurgeChannelEditor) {
	s.retentionNotifyMu.Lock()
	defer s.retentionNotifyMu.Unlock()
	s.retentionMessages = messages
	s.retentionChannels = channels
}

func (s *Service) retentionNotifier() (RetentionPurgeMessageEditor, RetentionPurgeChannelEditor) {
	s.retentionNotifyMu.RLock()
	defer s.retentionNotifyMu.RUnlock()
	return s.retentionMessages, s.retentionChannels
}

// notifyRetentionPurge turns a just-completed hard-retention/eviction blob
// purge of a document/photo into a visible service-message notice on every
// message that still embeds it, then reaps the documents/photos metadata row
// itself if that leaves nothing referencing it at all. profile_photo/
// sticker_set/gift references have no message to edit and are skipped.
// Best-effort throughout: a lookup or edit failure is logged and never
// propagated -- the underlying blob purge has already committed and must not
// be undone or retried because of this.
func (s *Service) notifyRetentionPurge(ctx context.Context, kind domain.MediaKind, mediaID int64) {
	messages, channels := s.retentionNotifier()
	if messages == nil && channels == nil {
		return
	}
	store, ok := s.media.(mediaRetentionStore)
	if !ok {
		return
	}
	refs, err := store.ListMediaReferences(ctx, kind, mediaID)
	if err != nil {
		s.log.Warn("list media references for retention purge notice failed",
			zap.String("media_kind", string(kind)), zap.Int64("media_id", mediaID), zap.Error(err))
		return
	}
	for _, ref := range refs {
		switch ref.RefKind {
		case domain.MediaRefKindMessageBox:
			s.notifyRetentionPurgeMessageBox(ctx, messages, ref.RefKey)
		case domain.MediaRefKindChannelMessage:
			s.notifyRetentionPurgeChannelMessage(ctx, channels, ref.RefKey)
		case domain.MediaRefKindProfilePhoto, domain.MediaRefKindStickerSet, domain.MediaRefKindGift:
			// No message to edit for these ref kinds.
		}
	}
	// Every message edited above just had its media replaced with the purge
	// notice, which -- like any other media-changing edit -- drops its
	// media_references row (see replaceMessageBoxMediaIndexTx /
	// replaceChannelMediaIndexTx). If that leaves zero references of ANY
	// kind (no live profile_photo/sticker_set/gift reference either, and no
	// edit above failed partway through), the documents/photos row is now
	// pure dead weight -- nothing anywhere still displays its filename,
	// dimensions, or mime type -- so delete it for real instead of keeping
	// it forever. deleteDocumentNowIfUnreferenced/deletePhotoNowIfUnreferenced
	// re-check media_references themselves before touching anything, so a
	// stale/failed edit above simply leaves a live reference behind and this
	// becomes a safe no-op -- the exact same check ordinary orphan-mode
	// deletion already relies on.
	switch kind {
	case domain.MediaKindDocument:
		if _, err := s.deleteDocumentNowIfUnreferenced(ctx, mediaID); err != nil {
			s.log.Warn("delete now-unreferenced document after retention purge failed",
				zap.Int64("document_id", mediaID), zap.Error(err))
		}
	case domain.MediaKindPhoto:
		if _, err := s.deletePhotoNowIfUnreferenced(ctx, mediaID); err != nil {
			s.log.Warn("delete now-unreferenced photo after retention purge failed",
				zap.Int64("photo_id", mediaID), zap.Error(err))
		}
	}
}

// notifyRetentionPurgeMessageBox parses a message_box ref_key (format
// "user:<owner_user_id>:box:<box_id>", see postgres.messageBoxRefKey -- kept
// in sync by convention, not a shared symbol, since the ref_key encoding is
// an internal storage detail of that package) and edits the box in place.
func (s *Service) notifyRetentionPurgeMessageBox(ctx context.Context, messages RetentionPurgeMessageEditor, refKey string) {
	if messages == nil {
		return
	}
	ownerUserID, boxID, ok := parseMessageBoxRefKey(refKey)
	if !ok {
		s.log.Warn("unparseable message_box retention purge ref_key", zap.String("ref_key", refKey))
		return
	}
	// EditMessageRequest requires the peer explicitly (an ordinary edit's RPC
	// caller always supplies it) -- resolve it from the box row itself since
	// this is a server-internal edit with no client request behind it.
	list, err := messages.GetMessages(ctx, ownerUserID, []int{boxID})
	if err != nil {
		s.log.Warn("resolve message_box peer for retention purge notice failed",
			zap.Int64("owner_user_id", ownerUserID), zap.Int("box_id", boxID), zap.Error(err))
		return
	}
	if len(list.Messages) == 0 {
		// Box no longer visible to its owner (deleted) -- nothing to notice.
		return
	}
	peer := list.Messages[0].Peer
	_, err = messages.EditMessage(ctx, ownerUserID, domain.EditMessageRequest{
		OwnerUserID:    ownerUserID,
		Peer:           peer,
		ID:             boxID,
		SetRichMessage: true,
		RichMessage:    nil,
		Media:          retentionPurgeNoticeMedia(),
		RetentionPurge: true,
	})
	if err != nil && !errors.Is(err, domain.ErrMessageNotModified) && !errors.Is(err, domain.ErrMessageIDInvalid) {
		s.log.Warn("retention purge notice edit failed (message_box)",
			zap.Int64("owner_user_id", ownerUserID), zap.Int("box_id", boxID), zap.Error(err))
	}
}

// notifyRetentionPurgeChannelMessage parses a channel_message ref_key
// (format "channel:<channel_id>:msg:<message_id>", see
// postgres.channelMessageRefKey) and edits the message in place. Unlike
// private messages, no peer lookup is needed: EditChannelMessageRequest only
// needs the channel id, already encoded in the ref_key.
func (s *Service) notifyRetentionPurgeChannelMessage(ctx context.Context, channels RetentionPurgeChannelEditor, refKey string) {
	if channels == nil {
		return
	}
	channelID, messageID, ok := parseChannelMessageRefKey(refKey)
	if !ok {
		s.log.Warn("unparseable channel_message retention purge ref_key", zap.String("ref_key", refKey))
		return
	}
	_, err := channels.EditChannelMessageInternal(ctx, domain.EditChannelMessageRequest{
		ChannelID:            channelID,
		ID:                   messageID,
		RetentionPurge:       true,
		RetentionPurgeAction: &domain.ChannelMessageAction{Type: domain.ChannelActionCustomText, Text: domain.RetentionPurgeNoticeText},
	})
	if err != nil && !errors.Is(err, domain.ErrMessageNotModified) && !errors.Is(err, domain.ErrMessageIDInvalid) {
		s.log.Warn("retention purge notice edit failed (channel_message)",
			zap.Int64("channel_id", channelID), zap.Int("message_id", messageID), zap.Error(err))
	}
}

// retentionPurgeNoticeMedia builds the messageActionCustomAction service
// media payload private-message edits replace a purged message's media with.
func retentionPurgeNoticeMedia() *domain.MessageMedia {
	return &domain.MessageMedia{
		Kind: domain.MessageMediaKindService,
		ServiceAction: &domain.MessageServiceAction{
			Kind: domain.MessageServiceActionCustomText,
			Text: domain.RetentionPurgeNoticeText,
		},
	}
}

// parseMessageBoxRefKey parses "user:<owner_user_id>:box:<box_id>".
// fmt.Sscanf reports success and silently ignores unconsumed trailing input
// (e.g. "user:1:box:2garbage" would still scan fine), so a bare cnt/err check
// isn't enough -- round-trip the parsed ids back through the same format
// (messageBoxRefKey's own layout) and require an exact match to reject any
// malformed/truncated/trailing-garbage key. Defensive only: ref_key is
// server-written and never externally supplied, but a bug or future
// encoding change should fail closed here, not silently misdirect an edit at
// the wrong box.
func parseMessageBoxRefKey(refKey string) (ownerUserID int64, boxID int, ok bool) {
	cnt, err := fmt.Sscanf(refKey, "user:%d:box:%d", &ownerUserID, &boxID)
	if err != nil || cnt != 2 || ownerUserID == 0 || boxID == 0 {
		return 0, 0, false
	}
	if fmt.Sprintf("user:%d:box:%d", ownerUserID, boxID) != refKey {
		return 0, 0, false
	}
	return ownerUserID, boxID, true
}

// parseChannelMessageRefKey parses "channel:<channel_id>:msg:<message_id>".
// See parseMessageBoxRefKey's doc comment -- same round-trip defense against
// trailing-garbage input Sscanf alone would silently accept.
func parseChannelMessageRefKey(refKey string) (channelID int64, messageID int, ok bool) {
	cnt, err := fmt.Sscanf(refKey, "channel:%d:msg:%d", &channelID, &messageID)
	if err != nil || cnt != 2 || channelID == 0 || messageID == 0 {
		return 0, 0, false
	}
	if fmt.Sprintf("channel:%d:msg:%d", channelID, messageID) != refKey {
		return 0, 0, false
	}
	return channelID, messageID, true
}
