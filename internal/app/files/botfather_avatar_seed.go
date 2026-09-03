package files

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"telesrv/internal/domain"
)

//go:embed seedassets/botfather_avatar.jpg
var botFatherAvatarJPG []byte

// SeedBotFatherAvatar seeds the built-in BotFather account's profile photo
// from the bundled avatar, mirroring SeedOfficialSystemAvatar: writes it
// under the fixed domain.BotFatherUserPhotoID so the photo/blob layer and
// the pure domain.BotFatherUser() struct literal stay in sync across
// restarts, and registers it as the account's *current* profile photo so
// users.getFullUser resolves it too. Deliberately re-upserts the photo row
// AND its file_blobs bytes on every boot (not just when the photos row is
// missing) -- storage retention/manual-purge only ever deletes file_blobs
// bytes, never the photos row itself, so a "skip if the row exists" check
// used to leave this bot's avatar permanently blank after any purge that
// swept the Avatar category, even though the row it checked for was still
// right there. Returns true if it actually (re)wrote the photo.
func (s *Service) SeedBotFatherAvatar(ctx context.Context) (bool, error) {
	photoID := domain.BotFatherUserPhotoID
	sizes, err := s.putPhotoStaticSizes(ctx, photoID, botFatherAvatarJPG, photoSizeSpecsForAvatar(botFatherAvatarJPG))
	if err != nil {
		return false, err
	}
	newPhoto := domain.Photo{
		ID:            photoID,
		AccessHash:    domain.BotFatherUserPhotoAccessHash,
		FileReference: randomFileReference(),
		Date:          int(time.Now().Unix()),
		DCID:          s.dc,
		Sizes:         sizes,
	}
	if err := s.media.PutPhoto(ctx, newPhoto); err != nil {
		return false, err
	}
	wrote := true
	photo, ok, err := s.SetCurrentProfilePhoto(ctx, domain.PeerTypeUser, domain.BotFatherUserID, photoID, int(time.Now().Unix()))
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("botfather avatar photo %d not found after seeding", photoID)
	}
	domain.SetBotFatherAvatar(photo.DCID, domain.StrippedFromSizes(photo.Sizes))
	return wrote, nil
}
