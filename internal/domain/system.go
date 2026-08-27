package domain

import (
	"strings"

	"telesrv/internal/branding"
)

const (
	// OfficialSystemUserID 是 Telegram 兼容客户端识别的官方系统账号。
	OfficialSystemUserID int64 = 777000
	// OfficialSystemUserPhotoID/AccessHash 是该账号头像 photo 的固定 id，
	// 与 files.Service.SeedOfficialSystemAvatar 种子写入的行保持一致，
	// 确保跨重启后 OfficialSystemUser() 引用的 photo id 稳定不变。
	OfficialSystemUserPhotoID         int64 = 7770000001
	OfficialSystemUserPhotoAccessHash int64 = 5837219004471160321

	// BotFatherUserID 是内置 BotFather 账号，与官方 @BotFather 同 ID。
	BotFatherUserID int64 = 93372553
	// BotFatherAccessHash 固定不变；与迁移 0090 的种子行双写，必须保持一致。
	BotFatherAccessHash int64 = 7421896403922962293
	// BotFatherUserPhotoID/AccessHash 是 BotFather 头像 photo 的固定 id，
	// 与 files.Service.SeedBotFatherAvatar 种子写入的行保持一致。
	BotFatherUserPhotoID         int64 = 933725530001
	BotFatherUserPhotoAccessHash int64 = 3198475620194837201

	// StickersBotUserID 是内置 @Stickers 账号。它是 server 内置 service bot，
	// 不走外部 Bot API 进程。
	StickersBotUserID int64 = 1063110917
	// StickersBotAccessHash 固定不变；与 postgres 种子行双写，必须保持一致。
	StickersBotAccessHash int64 = 5213187021149032991
	// StickersBotUserPhotoID/AccessHash 是 @Stickers 头像 photo 的固定 id，
	// 与 files.Service.SeedStickersBotAvatar 种子写入的行保持一致。
	StickersBotUserPhotoID         int64 = 10631109170001
	StickersBotUserPhotoAccessHash int64 = 4636293356791048892

	// ChatBotUserID 是内置 @ChatBot 账号。它把私聊文本转给 server AI provider 链。
	ChatBotUserID int64 = 1250000007
	// ChatBotAccessHash 固定不变；与 postgres 种子行双写，必须保持一致。
	ChatBotAccessHash int64 = 6332902371644871201
	// ChatBotUserPhotoID/AccessHash 是 @ChatBot 头像 photo 的固定 id，
	// 与 files.Service.SeedChatBotAvatar 种子写入的行保持一致。
	ChatBotUserPhotoID         int64 = 12500000070001
	ChatBotUserPhotoAccessHash int64 = 8748578814399338333

	// VerifyBotUserID is the built-in @verifybot: it collects official platform
	// verification applications and reports decisions back to the applicant. The
	// id is reserved and stable, so a restart never re-creates the account under a
	// different identity.
	VerifyBotUserID int64 = 1250000011
	// VerifyBotAccessHash is fixed and double-written with the seed row in
	// migration 0153; the two must never drift.
	VerifyBotAccessHash int64 = 7802113947355620887
	// VerifyBotUserPhotoID/AccessHash are the fixed id of @verifybot's avatar
	// photo, matching the row files.Service.SeedVerifyBotAvatar seeds.
	VerifyBotUserPhotoID         int64 = 12500000110001
	VerifyBotUserPhotoAccessHash int64 = 975869468725402752

	// VerifierBotUserID is the built-in @marksbot: the first THIRD-PARTY
	// verifier of a deployment (core.telegram.org/api/bots/verification). It
	// collects applications for its own icon+description mark and reports the
	// operator's decision back to the applicant. The id is reserved and stable, so
	// a restart never re-creates the account under a different identity.
	//
	// It is not a second route to the platform checkmark: that badge is granted by
	// the operator alone and collected by VerifyBotUserID above. Named "Marks Bot"
	// rather than anything containing "Verif*" specifically so it can never be
	// misread as a second copy of @verifybot -- the two front doors must stay
	// visually distinct at a glance, not just distinct in the underlying mechanism.
	VerifierBotUserID int64 = 1250000013
	// VerifierBotAccessHash is fixed and double-written with the seed row in
	// migration 0156; the two must never drift.
	VerifierBotAccessHash int64 = 6913402578811563729
	// VerifierBotDefaultIconDocumentID is the custom-emoji document (a ✅ from the
	// default "Topics" emoji set, data/sticker-seed/telegram_emoji_export) reused
	// as @marksbot's out-of-the-box icon, so the reference verifier has something
	// to grant immediately after first boot instead of an empty catalogue. It is
	// bundled media that the ordinary sticker-seed import already writes on every
	// startup -- not a document minted specifically for this feature -- so it
	// resolves the same way any other seeded custom emoji does.
	VerifierBotDefaultIconDocumentID int64 = 5237699328843200968

	// GifBotUserID is the built-in @gif inline bot: it answers
	// messages.getInlineBotResults synchronously (no MTProto session, no Bot API
	// process -- see rpc.ServiceBotInlineResults) with the admin-curated GIF
	// catalog, the same role Telegram's own @gif plays for the client's GIF
	// picker "trending"/search panel. The id is reserved and stable, so a
	// restart never re-creates the account under a different identity.
	GifBotUserID int64 = 1250000015
	// GifBotAccessHash is fixed and double-written with the seed row in this
	// feature's migration; the two must never drift.
	GifBotAccessHash int64 = 7233282977235616768
)

// officialSystemUserPhotoDCID/Stripped 由 files.Service.SeedOfficialSystemAvatar
// 在启动时通过 SetOfficialSystemUserAvatar 写入一次；写入前 OfficialSystemUser()
// 不带头像（PhotoID==0），与其它未设置头像的账号行为一致。
var (
	officialSystemUserPhotoDCID     int
	officialSystemUserPhotoStripped []byte
)

// SetOfficialSystemUserAvatar 记录官方系统账号头像所在的 DC 与内联缩略图字节。
// 只应在启动阶段、头像 seed 完成后调用一次。
func SetOfficialSystemUserAvatar(dcID int, stripped []byte) {
	officialSystemUserPhotoDCID = dcID
	officialSystemUserPhotoStripped = stripped
}

// officialSystemUserDisplayName overrides OfficialSystemUser's FirstName --
// empty means "use branding.ProductName" (the compile-time default), set
// once at startup from the operator's Server Settings -> Server identity
// name, if any. Deliberately only the display name, not Username: the
// account's @username is a stable, addressable identifier other things may
// already reference, unlike the display name shown in chat headers.
var officialSystemUserDisplayName string

// SetOfficialSystemUserDisplayName records the operator's custom server
// name for the official system account (777000), read once at startup from
// Server Settings -> Server identity. Pass "" to fall back to
// branding.ProductName -- the same "unset -> default" contract the avatar
// override above uses.
func SetOfficialSystemUserDisplayName(name string) {
	officialSystemUserDisplayName = strings.TrimSpace(name)
}

// botFatherPhotoDCID/Stripped 由 files.Service.SeedBotFatherAvatar 在启动时
// 通过 SetBotFatherAvatar 写入一次；写入前 BotFatherUser() 不带头像（PhotoID==0）。
var (
	botFatherPhotoDCID     int
	botFatherPhotoStripped []byte
)

// SetBotFatherAvatar 记录 BotFather 头像所在的 DC 与内联缩略图字节。
// 只应在启动阶段、头像 seed 完成后调用一次。
func SetBotFatherAvatar(dcID int, stripped []byte) {
	botFatherPhotoDCID = dcID
	botFatherPhotoStripped = stripped
}

// stickersBotPhotoDCID/Stripped 由 files.Service.SeedStickersBotAvatar 在启动时
// 通过 SetStickersBotAvatar 写入一次；写入前 StickersBotUser() 不带头像（PhotoID==0）。
var (
	stickersBotPhotoDCID     int
	stickersBotPhotoStripped []byte
)

// SetStickersBotAvatar 记录 @Stickers 头像所在的 DC 与内联缩略图字节。
// 只应在启动阶段、头像 seed 完成后调用一次。
func SetStickersBotAvatar(dcID int, stripped []byte) {
	stickersBotPhotoDCID = dcID
	stickersBotPhotoStripped = stripped
}

// chatBotPhotoDCID/Stripped 由 files.Service.SeedChatBotAvatar 在启动时
// 通过 SetChatBotAvatar 写入一次；写入前 ChatBotUser() 不带头像（PhotoID==0）。
var (
	chatBotPhotoDCID     int
	chatBotPhotoStripped []byte
)

// SetChatBotAvatar 记录 @ChatBot 头像所在的 DC 与内联缩略图字节。
// 只应在启动阶段、头像 seed 完成后调用一次。
func SetChatBotAvatar(dcID int, stripped []byte) {
	chatBotPhotoDCID = dcID
	chatBotPhotoStripped = stripped
}

// verifyBotPhotoDCID/Stripped are written once at startup by
// files.Service.SeedVerifyBotAvatar via SetVerifyBotAvatar; before that,
// VerifyBotUser() carries no photo (PhotoID==0), same as every other
// not-yet-seeded built-in account.
var (
	verifyBotPhotoDCID     int
	verifyBotPhotoStripped []byte
)

// SetVerifyBotAvatar records the DC and inline thumbnail bytes for
// @verifybot's avatar. Should only be called once, at startup, after the
// avatar seed completes.
func SetVerifyBotAvatar(dcID int, stripped []byte) {
	verifyBotPhotoDCID = dcID
	verifyBotPhotoStripped = stripped
}

// OfficialSystemUser 返回第一阶段内置的官方系统账号。
// No Username: the real Telegram service account (777000) isn't
// @-addressable either, and reserving one here would need it re-blocked in
// config.ReservedUsernames (which it now is, by default, precisely because
// nothing keeps another account from claiming it once this one has none).
func OfficialSystemUser() User {
	name := branding.ProductName
	if officialSystemUserDisplayName != "" {
		name = officialSystemUserDisplayName
	}
	u := User{
		ID:         OfficialSystemUserID,
		AccessHash: 6599886787491911851,
		Phone:      "42777",
		FirstName:  name,
		Verified:   true,
		Support:    true,
	}
	if officialSystemUserPhotoDCID != 0 {
		u.PhotoID = OfficialSystemUserPhotoID
		u.PhotoDCID = officialSystemUserPhotoDCID
		u.PhotoStripped = officialSystemUserPhotoStripped
	}
	return u
}

// BotFatherUser 返回内置 BotFather 账号。username 不以 bot 结尾属种子例外（与官方一致）。
func BotFatherUser() User {
	u := User{
		ID:             BotFatherUserID,
		AccessHash:     BotFatherAccessHash,
		FirstName:      "BotFather",
		Username:       "BotFather",
		Verified:       true,
		Bot:            true,
		BotInfoVersion: 1,
	}
	if botFatherPhotoDCID != 0 {
		u.PhotoID = BotFatherUserPhotoID
		u.PhotoDCID = botFatherPhotoDCID
		u.PhotoStripped = botFatherPhotoStripped
	}
	return u
}

// StickersBotUser 返回内置 @Stickers 账号。username 不以 bot 结尾属种子例外（与官方一致）。
func StickersBotUser() User {
	u := User{
		ID:             StickersBotUserID,
		AccessHash:     StickersBotAccessHash,
		FirstName:      "Stickers",
		Username:       "Stickers",
		Verified:       true,
		Bot:            true,
		BotInfoVersion: 2,
	}
	if stickersBotPhotoDCID != 0 {
		u.PhotoID = StickersBotUserPhotoID
		u.PhotoDCID = stickersBotPhotoDCID
		u.PhotoStripped = stickersBotPhotoStripped
	}
	return u
}

// ChatBotUser 返回内置 @ChatBot 账号。
func ChatBotUser() User {
	u := User{
		ID:             ChatBotUserID,
		AccessHash:     ChatBotAccessHash,
		FirstName:      "ChatBot",
		Username:       "ChatBot",
		Verified:       true,
		Bot:            true,
		BotInfoVersion: 1,
	}
	if chatBotPhotoDCID != 0 {
		u.PhotoID = ChatBotUserPhotoID
		u.PhotoDCID = chatBotPhotoDCID
		u.PhotoStripped = chatBotPhotoStripped
	}
	return u
}

// VerifyBotUser returns the built-in @verifybot account. It is verified itself,
// so the applicant sees the same badge on the account that grants it.
func VerifyBotUser() User {
	u := User{
		ID:             VerifyBotUserID,
		AccessHash:     VerifyBotAccessHash,
		FirstName:      "Verify Bot",
		Username:       "verifybot",
		Verified:       true,
		Bot:            true,
		BotInfoVersion: 1,
	}
	if verifyBotPhotoDCID != 0 {
		u.PhotoID = VerifyBotUserPhotoID
		u.PhotoDCID = verifyBotPhotoDCID
		u.PhotoStripped = verifyBotPhotoStripped
	}
	return u
}

// VerifierBotUser returns the built-in @marksbot account.
//
// Verified is true: this deployment carries the platform checkmark on its own
// service bots (see e.g. VerifyBotUser, BotFatherUser), and @marksbot is one of
// them -- a legitimate first-party account, just one that happens to also grant
// a *different*, third-party mark to other peers. The checkmark here says
// "this account is who it claims to be", not "this account's grants are
// official"; that distinction is what @marksbot's own messages explain to every
// applicant, and it does not depend on this bot's own badge being off. What
// makes the account a verifier at all is the operator-granted
// BotVerifierSettings row, not this seed -- the two remain fully independent.
func VerifierBotUser() User {
	return User{
		ID:             VerifierBotUserID,
		AccessHash:     VerifierBotAccessHash,
		FirstName:      "Marks Bot",
		Username:       "marksbot",
		Verified:       true,
		Bot:            true,
		BotInfoVersion: 1,
	}
}

// GifBotUser returns the built-in @gif account.
func GifBotUser() User {
	return User{
		ID:             GifBotUserID,
		AccessHash:     GifBotAccessHash,
		FirstName:      "GIFs",
		Username:       "gif",
		Verified:       true,
		Bot:            true,
		BotInfoVersion: 1,
	}
}

// SystemUserByID 返回内置系统账号；非系统账号返回 ok=false。
// 所有对 777000 的硬编码注入点统一经此函数，新增内置账号只改这里。
func SystemUserByID(id int64) (User, bool) {
	switch id {
	case OfficialSystemUserID:
		return OfficialSystemUser(), true
	case BotFatherUserID:
		return BotFatherUser(), true
	case StickersBotUserID:
		return StickersBotUser(), true
	case ChatBotUserID:
		return ChatBotUser(), true
	case VerifyBotUserID:
		return VerifyBotUser(), true
	case VerifierBotUserID:
		return VerifierBotUser(), true
	case GifBotUserID:
		return GifBotUser(), true
	}
	return User{}, false
}

func IsSystemUserID(id int64) bool {
	_, ok := SystemUserByID(id)
	return ok
}

// SystemUserIDs returns every built-in account id, in a stable order.
//
// It is the one list to extend when a service account is added, so a caller that
// has to enumerate them -- a SQL predicate excluding infrastructure, say -- cannot
// silently miss one the way an inline literal would.
func SystemUserIDs() []int64 {
	return []int64{
		OfficialSystemUserID,
		BotFatherUserID,
		StickersBotUserID,
		ChatBotUserID,
		VerifyBotUserID,
		VerifierBotUserID,
		GifBotUserID,
	}
}

// SystemUserByUsername resolves a case-insensitive exact username match
// against every built-in account, e.g. for username-lookup paths that
// otherwise enforce a minimum length shorter than some reserved system
// handles need (@gif is 3 characters -- shorter than
// MinCollectibleUsernameLength's 4-character floor for an ordinary lookup).
func SystemUserByUsername(username string) (User, bool) {
	username = NormalizeUsername(username)
	if username == "" {
		return User{}, false
	}
	for _, id := range SystemUserIDs() {
		u, ok := SystemUserByID(id)
		if ok && strings.EqualFold(u.Username, username) {
			return u, true
		}
	}
	return User{}, false
}

func SystemUserByPhone(phone string) (User, bool) {
	phone = NormalizePhone(phone)
	for _, id := range SystemUserIDs() {
		u, ok := SystemUserByID(id)
		if !ok || u.Phone == "" {
			continue
		}
		if NormalizePhone(u.Phone) == phone {
			return u, true
		}
	}
	return User{}, false
}
