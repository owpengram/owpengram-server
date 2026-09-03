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

// SeedStickersBotAvatar seeds the built-in @Stickers account's profile photo
// from the bundled avatar, mirroring SeedOfficialSystemAvatar: writes it
// under the fixed domain.StickersBotUserPhotoID so the photo/blob layer and
// the pure domain.StickersBotUser() struct literal stay in sync across
// restarts, and registers it as the account's *current* profile photo so
// users.getFullUser resolves it too. Deliberately re-upserts the photo row
// AND its file_blobs bytes on every boot (not just when the photos row is
// missing) -- see SeedBotFatherAvatar's doc comment for why a "skip if the
// row exists" check is wrong here (storage retention/manual-purge only ever
// deletes file_blobs bytes, never the photos row). Returns true if it
// actually (re)wrote the photo.
func (s *Service) SeedStickersBotAvatar(ctx context.Context) (bool, error) {
	photoID := domain.StickersBotUserPhotoID
	sizes, err := s.putPhotoStaticSizes(ctx, photoID, stickersBotAvatarPNG, photoSizeSpecsForAvatar(stickersBotAvatarPNG))
	if err != nil {
		return false, err
	}
	newPhoto := domain.Photo{
		ID:            photoID,
		AccessHash:    domain.StickersBotUserPhotoAccessHash,
		FileReference: randomFileReference(),
		Date:          int(time.Now().Unix()),
		DCID:          s.dc,
		Sizes:         sizes,
	}
	if err := s.media.PutPhoto(ctx, newPhoto); err != nil {
		return false, err
	}
	wrote := true
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
