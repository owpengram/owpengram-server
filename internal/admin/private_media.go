package admin

import (
	"context"
	"errors"
	"regexp"

	"telesrv/internal/domain"
)

// MaxPrivateMediaBytes caps one file the console's chat viewer streams. A
// photo is far below it; a long video is not, and is shown as a file card
// with its size instead of being pulled through the panel whole.
const MaxPrivateMediaBytes = 48 << 20

// ErrPrivateMediaTooLarge reports a stored file above MaxPrivateMediaBytes.
var ErrPrivateMediaTooLarge = errors.New("file is too large to preview")

// privateMediaKey is the only shape of storage key the viewer may ask for: a
// photo size, a document, or a document thumbnail. Anything else (upload
// parts, encrypted files, blobs by object key) is refused outright.
var privateMediaKey = regexp.MustCompile(`^(photo:[0-9]{1,20}:[A-Za-z0-9_]{1,8}|doc:[0-9]{1,20}(:[A-Za-z0-9_]{1,8})?)$`)

// PrivateMediaFile reads one stored file by its storage location key for the
// console's private-content viewer. Which file a viewer may see is decided by
// the panel (it gates the whole viewer behind a password re-check and an
// audit row); this only fetches bytes, and only for photo/document keys.
func (s *Service) PrivateMediaFile(ctx context.Context, locationKey string) ([]byte, bool, error) {
	if s == nil || s.photos == nil || !privateMediaKey.MatchString(locationKey) {
		return nil, false, nil
	}
	chunk, found, err := s.photos.GetFile(ctx, domain.FileDownloadRequest{
		LocationKey: locationKey,
		Limit:       MaxPrivateMediaBytes + 1,
	})
	if err != nil || !found {
		return nil, found, err
	}
	if chunk.Total > MaxPrivateMediaBytes {
		return nil, true, ErrPrivateMediaTooLarge
	}
	if chunk.Total <= 0 || int64(len(chunk.Bytes)) != chunk.Total {
		return nil, false, nil
	}
	return chunk.Bytes, true, nil
}
