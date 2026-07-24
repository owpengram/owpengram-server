package files

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"telesrv/internal/domain"
)

//go:embed seedassets/stickers_avatar.png
var stickersBotAvatarPNG []byte

// SeedStickersBotAvatar idempotently seeds the built-in @Stickers account's
// profile photo from the bundled avatar, mirroring SeedBotFatherAvatar:
// writes it under the fixed domain.StickersBotUserPhotoID so the photo/blob
// layer and the pure domain.StickersBotUser() struct literal stay in sync
// across restarts, and registers it as the account's *current* profile photo
// so users.getFullUser resolves it too. Returns true if it actually wrote a
// new photo.
func (s *Service) SeedStickersBotAvatar(ctx context.Context) (bool, error) {
	photoID := domain.StickersBotUserPhotoID
	wrote := false
	if _, found, err := s.media.GetPhoto(ctx, photoID); err != nil {
		return false, err
	} else if !found {
		sizes, err := s.putPhotoStaticSizes(ctx, photoID, stickersBotAvatarPNG, photoSizeSpecsForAvatar(stickersBotAvatarPNG))
		if err != nil {
			return false, err
		}
		photo := domain.Photo{
			ID:            photoID,
			AccessHash:    domain.StickersBotUserPhotoAccessHash,
			FileReference: randomFileReference(),
			Date:          int(time.Now().Unix()),
			DCID:          s.dc,
			Sizes:         sizes,
		}
		if err := s.media.PutPhoto(ctx, photo); err != nil {
			return false, err
		}
		wrote = true
	}
	photo, ok, err := s.SetCurrentProfilePhoto(ctx, domain.PeerTypeUser, domain.StickersBotUserID, photoID, int(time.Now().Unix()))
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("stickers bot avatar photo %d not found after seeding", photoID)
	}
	domain.SetStickersBotAvatar(photo.DCID, domain.StrippedFromSizes(photo.Sizes))
	return wrote, nil
}
