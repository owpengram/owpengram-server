package files

import (
	"context"
	_ "embed"
	"time"

	"telesrv/internal/domain"
)

//go:embed seedassets/owpengram_system_avatar.png
var officialSystemAvatarPNG []byte

// SeedOfficialSystemAvatar idempotently seeds the built-in official system
// account's (777000) profile photo from the bundled brand logo, writing it
// under the fixed domain.OfficialSystemUserPhotoID so the photo/blob layer
// and the pure domain.OfficialSystemUser() struct literal stay in sync
// across restarts. Returns true if it actually wrote a new photo.
func (s *Service) SeedOfficialSystemAvatar(ctx context.Context) (bool, error) {
	photoID := domain.OfficialSystemUserPhotoID
	if existing, found, err := s.media.GetPhoto(ctx, photoID); err != nil {
		return false, err
	} else if found {
		domain.SetOfficialSystemUserAvatar(existing.DCID, domain.StrippedFromSizes(existing.Sizes))
		return false, nil
	}
	sizes, err := s.putPhotoStaticSizes(ctx, photoID, officialSystemAvatarPNG, photoSizeSpecsForAvatar(officialSystemAvatarPNG))
	if err != nil {
		return false, err
	}
	photo := domain.Photo{
		ID:            photoID,
		AccessHash:    domain.OfficialSystemUserPhotoAccessHash,
		FileReference: randomFileReference(),
		Date:          int(time.Now().Unix()),
		DCID:          s.dc,
		Sizes:         sizes,
	}
	if err := s.media.PutPhoto(ctx, photo); err != nil {
		return false, err
	}
	domain.SetOfficialSystemUserAvatar(photo.DCID, domain.StrippedFromSizes(photo.Sizes))
	return true, nil
}
