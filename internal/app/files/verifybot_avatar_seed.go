package files

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"telesrv/internal/domain"
)

//go:embed seedassets/verifybot_avatar.png
var verifyBotAvatarPNG []byte

// SeedVerifyBotAvatar seeds the built-in @verifybot account's profile photo
// from the bundled avatar, mirroring SeedOfficialSystemAvatar: writes it
// under the fixed domain.VerifyBotUserPhotoID so the photo/blob layer and
// the pure domain.VerifyBotUser() struct literal stay in sync across
// restarts, and registers it as the account's *current* profile photo so
// users.getFullUser resolves it too. Deliberately re-upserts the photo row
// AND its file_blobs bytes on every boot (not just when the photos row is
// missing) -- see SeedBotFatherAvatar's doc comment for why a "skip if the
// row exists" check is wrong here (storage retention/manual-purge only ever
// deletes file_blobs bytes, never the photos row). Returns true if it
// actually (re)wrote the photo.
func (s *Service) SeedVerifyBotAvatar(ctx context.Context) (bool, error) {
	photoID := domain.VerifyBotUserPhotoID
	sizes, err := s.putPhotoStaticSizes(ctx, photoID, verifyBotAvatarPNG, photoSizeSpecsForAvatar(verifyBotAvatarPNG))
	if err != nil {
		return false, err
	}
	newPhoto := domain.Photo{
		ID:            photoID,
		AccessHash:    domain.VerifyBotUserPhotoAccessHash,
		FileReference: randomFileReference(),
		Date:          int(time.Now().Unix()),
		DCID:          s.dc,
		Sizes:         sizes,
	}
	if err := s.media.PutPhoto(ctx, newPhoto); err != nil {
		return false, err
	}
	wrote := true
	photo, ok, err := s.SetCurrentProfilePhoto(ctx, domain.PeerTypeUser, domain.VerifyBotUserID, photoID, int(time.Now().Unix()))
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("verifybot avatar photo %d not found after seeding", photoID)
	}
	domain.SetVerifyBotAvatar(photo.DCID, domain.StrippedFromSizes(photo.Sizes))
	return wrote, nil
}
