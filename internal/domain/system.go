package domain

import "telesrv/internal/branding"

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

// OfficialSystemUser 返回第一阶段内置的官方系统账号。
func OfficialSystemUser() User {
	u := User{
		ID:         OfficialSystemUserID,
		AccessHash: 6599886787491911851,
		Phone:      "42777",
		FirstName:  branding.ProductName,
		Username:   branding.ProductUsername,
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
	}
	return User{}, false
}

func IsSystemUserID(id int64) bool {
	_, ok := SystemUserByID(id)
	return ok
}

func SystemUserByPhone(phone string) (User, bool) {
	phone = NormalizePhone(phone)
	for _, id := range []int64{OfficialSystemUserID, BotFatherUserID, StickersBotUserID, ChatBotUserID} {
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
