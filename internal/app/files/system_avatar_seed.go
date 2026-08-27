package files

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"telesrv/internal/domain"
)

//go:embed seedassets/owpengram_system_avatar.png
var officialSystemAvatarPNG []byte

// SeedOfficialSystemAvatar seeds the built-in official system account's
// (777000) profile photo, writing it under the fixed
// domain.OfficialSystemUserPhotoID so the photo/blob layer and the pure
// domain.OfficialSystemUser() struct literal stay in sync across restarts.
// It also registers the photo as the account's *current* profile photo (the
// profile_photos association) — without this, list views render the avatar
// from the hardcoded User struct fields, but users.getFullUser (triggered on
// chat open) reads only the association, finds nothing, and the client
// wipes the avatar it just showed.
//
// customIcon, when non-empty, is the operator's own Server Settings ->
// Server identity icon (any of the formats Server Settings accepts --
// putPhotoStaticSizes stores it as-is, no re-encoding, so format doesn't
// matter here); it replaces the bundled default OwpenGram logo. This
// deliberately re-upserts the same fixed photo ID on *every* boot (not just
// the first) rather than skipping once a row exists, so switching the
// custom icon on/off in the admin panel is reflected here on the next
// restart, the same "changes take effect on next Restart/Update" contract
// identity's other settings already have. Returns true if a custom icon is
// in effect (for the startup log line) -- not whether anything on disk
// actually changed since the last boot.
func (s *Service) SeedOfficialSystemAvatar(ctx context.Context, customIcon []byte) (bool, error) {
	photoID := domain.OfficialSystemUserPhotoID
	data := officialSystemAvatarPNG
	usingCustom := len(customIcon) > 0
	if usingCustom {
		data = customIcon
	}
	sizes, err := s.putPhotoStaticSizes(ctx, photoID, data, photoSizeSpecsForAvatar(data))
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
	current, ok, err := s.SetCurrentProfilePhoto(ctx, domain.PeerTypeUser, domain.OfficialSystemUserID, photoID, int(time.Now().Unix()))
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("official system avatar photo %d not found after seeding", photoID)
	}
	domain.SetOfficialSystemUserAvatar(current.DCID, domain.StrippedFromSizes(current.Sizes))
	return usingCustom, nil
}
