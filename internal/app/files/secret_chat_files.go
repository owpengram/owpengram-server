package files

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"telesrv/internal/domain"
)

// DeleteEncryptedFileBlob deletes a secret-chat encrypted file's blob bytes
// (location_key "enc:<id>") once the recipient has fully downloaded it --
// see internal/rpc/upload.go's onUploadGetFile, which calls this after a
// getFile response whose bytes reach the end of the file. Secret chats are
// single-device on both ends (no multi-device sync, unlike ordinary chats),
// so once the one recipient device that will ever ask for this ciphertext
// has it, the server has no further reason to keep it -- unlike ordinary
// message media, there is no "another device might still need to fetch
// this" case to protect against.
//
// No-op unless TELESRV_SECRET_CHAT_DELETE_FILE_AFTER_DOWNLOAD is enabled
// (WithSecretChatDeleteFileAfterDownload, default true). Best-effort: the
// caller is a fire-and-forget goroutine off the download response, so
// errors are only logged, never propagated -- a failed cleanup here must
// never turn into a failed or delayed file download.
func (s *Service) DeleteEncryptedFileBlob(ctx context.Context, locationKey string) error {
	if !s.secretChatDeleteFileAfterDownload {
		return nil
	}
	store, ok := s.media.(mediaRetentionStore)
	if !ok {
		return nil
	}
	blob, found, err := s.media.GetFileBlob(ctx, locationKey)
	if err != nil {
		return fmt.Errorf("get encrypted file blob: %w", err)
	}
	if !found {
		// Already deleted (e.g. a racing duplicate getFile request for the
		// same final chunk) -- nothing left to do.
		return nil
	}
	if err := s.media.DeleteFileBlobRow(ctx, locationKey); err != nil {
		return fmt.Errorf("delete encrypted file blob row: %w", err)
	}
	s.blobCache.delete(locationKey)
	s.deleteOrphanedBlobs(ctx, store, []domain.FileBlob{blob})
	s.log.Info("secret chat file deleted after download",
		zap.String("location_key", locationKey), zap.Int64("size", blob.Size))
	return nil
}
