package files

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"telesrv/internal/domain"
)

//go:embed seedassets/chatbot_avatar.png
var chatBotAvatarPNG []byte

// SeedChatBotAvatar idempotently seeds the built-in @ChatBot account's
// profile photo from the bundled avatar, mirroring SeedBotFatherAvatar:
// writes it under the fixed domain.ChatBotUserPhotoID so the photo/blob
// layer and the pure domain.ChatBotUser() struct literal stay in sync
// across restarts, and registers it as the account's *current* profile photo
// so users.getFullUser resolves it too. Returns true if it actually wrote a
// new photo.
func (s *Service) SeedChatBotAvatar(ctx context.Context) (bool, error) {
	photoID := domain.ChatBotUserPhotoID
	wrote := false
	if _, found, err := s.media.GetPhoto(ctx, photoID); err != nil {
		return false, err
	} else if !found {
		sizes, err := s.putPhotoStaticSizes(ctx, photoID, chatBotAvatarPNG, photoSizeSpecsForAvatar(chatBotAvatarPNG))
		if err != nil {
			return false, err
		}
		photo := domain.Photo{
			ID:            photoID,
			AccessHash:    domain.ChatBotUserPhotoAccessHash,
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
	photo, ok, err := s.SetCurrentProfilePhoto(ctx, domain.PeerTypeUser, domain.ChatBotUserID, photoID, int(time.Now().Unix()))
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("chatbot avatar photo %d not found after seeding", photoID)
	}
	domain.SetChatBotAvatar(photo.DCID, domain.StrippedFromSizes(photo.Sizes))
	return wrote, nil
}
