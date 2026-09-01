package admin

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"telesrv/internal/domain"
)

const (
	ActionSetAccountFrozen         = "account.set_frozen"
	ActionGrantPremium             = "account.grant_premium"
	ActionSetVerified              = "account.set_verified"
	ActionSetUserFlags             = "account.set_flags"
	ActionSetSupport               = "account.set_support"
	ActionSetUsername              = "account.set_username"
	ActionSetUserColor             = "account.set_color"
	ActionSetUserEmojiStatus       = "account.set_emoji_status"
	ActionSetProfile               = "account.set_profile"
	ActionSetPhone                 = "account.set_phone"
	ActionSetLoginEmail            = "account.set_login_email"
	ActionSetAccountAvatar         = "account.set_avatar"
	ActionSetChannelAvatar         = "channel.set_avatar"
	ActionSetChannelUsername       = "channel.set_username"
	ActionSetChannelSettings       = "channel.set_settings"
	ActionSetChannelColor          = "channel.set_color"
	ActionSetChannelEmojiStatus    = "channel.set_emoji_status"
	ActionSetChannelVerified       = "channel.set_verified"
	ActionSetChannelFlags          = "channel.set_flags"
	ActionRevokeSessions           = "account.revoke_sessions"
	ActionDeletePrivateMessages    = "messages.delete_private_messages"
	ActionDeletePrivateHistory     = "messages.delete_private_history"
	ActionCreateBot                = "bot.create"
	ActionCreateBroadcast          = "broadcast.create"
	ActionDeleteBot                = "bot.delete"
	ActionExportBotToken           = "bot.export_token"
	ActionSetStickerSetArchived    = "stickers.set_archived"
	ActionSetStickerSetSortOrder   = "stickers.set_sort_order"
	ActionRenameStickerSet         = "stickers.rename"
	ActionDeleteStickerSet         = "stickers.delete"
	ActionCreateStickerSet         = "stickers.create"
	ActionAddStickerToSet          = "stickers.add_sticker"
	ActionRemoveStickerFromSet     = "stickers.remove_sticker"
	ActionCreateGifCatalogEntry    = "gif_catalog.create"
	ActionSetGifCatalogEnabled     = "gif_catalog.set_enabled"
	ActionSetGifCatalogSortOrder   = "gif_catalog.set_sort_order"
	ActionSetGifCatalogCategory    = "gif_catalog.set_category"
	ActionAutoCategorizeGifCatalog = "gif_catalog.auto_categorize"
	ActionDeleteUncategorizedGifs  = "gif_catalog.delete_uncategorized"
	ActionDeleteGifCatalogEntry    = "gif_catalog.delete"
	// Collectible (Fragment-style) username lifecycle.
	ActionMintCollectibleUsername     = "usernames.collectible.mint"
	ActionTransferCollectibleUsername = "usernames.collectible.transfer"
	ActionRevokeCollectibleUsername   = "usernames.collectible.revoke"
	ActionDeleteCollectibleUsername   = "usernames.collectible.delete"
	// Official platform verification review. Claim/approve/reject act on one
	// application; revoke acts on a target, because clearing a badge is not a
	// decision on the application that granted it.
	ActionClaimVerification   = "verification.claim"
	ActionApproveVerification = "verification.approve"
	ActionRejectVerification  = "verification.reject"
	ActionRevokeVerification  = "verification.revoke"
	// Third-party bot verification (see botverification.go). A namespace of its own
	// on purpose: these actions write the verifier catalogue and the attributed
	// marks, never the platform checkmark, and the audit trail has to keep the two
	// mechanisms apart at a glance.
	ActionGrantBotVerifier          = "botverification.grant_verifier"
	ActionSetBotVerifierEnabled     = "botverification.set_verifier_enabled"
	ActionRevokeBotVerifier         = "botverification.revoke_verifier"
	ActionUpsertVerificationIcon    = "botverification.upsert_icon"
	ActionSetVerificationIconActive = "botverification.set_icon_active"
	ActionRevokeCustomVerification  = "botverification.revoke_mark"
	ActionApproveBotVerification    = "botverification.approve"
	ActionRejectBotVerification     = "botverification.reject"
	ActionRevokeBotVerification     = "botverification.revoke_request"

	maxCommandIDLength       = 128
	maxActorLength           = 128
	maxReasonLength          = 1000
	maxHistoryBatches        = 100
	maxPremiumMonths         = 120
	maxFreezeAppealURLLength = 2048
)

// Stable admin error codes for the collectible-username and account-rating
// subsystems. The panel switches on the code, so a command failure and a read
// failure describe the same condition with the same token instead of leaking a
// Go error string the UI would have to pattern-match.
const (
	CodeUsernameOccupied           = "USERNAME_OCCUPIED"
	CodeUsernameInvalid            = "USERNAME_INVALID"
	CodeUsernameNotCollectible     = "USERNAME_NOT_COLLECTIBLE"
	CodeCollectibleNotFound        = "COLLECTIBLE_NOT_FOUND"
	CodeCollectibleNotOwned        = "COLLECTIBLE_NOT_OWNED"
	CodeCollectibleBurned          = "COLLECTIBLE_BURNED"
	CodeCollectiblePeerLimit       = "COLLECTIBLE_PEER_LIMIT"
	CodeCollectibleCurrencyInvalid = "COLLECTIBLE_CURRENCY_INVALID"
	CodeCollectibleStateInvalid    = "COLLECTIBLE_STATE_INVALID"
	// Official platform verification review. CodeVerificationConflict is the lost
	// optimistic-locking race -- two reviewers deciding at once -- and is the one
	// the panel must render as "reload and look again" rather than as a bad
	// request.
	CodeVerificationNotFound            = "VERIFICATION_NOT_FOUND"
	CodeVerificationConflict            = "VERIFICATION_CONFLICT"
	CodeVerificationStatusInvalid       = "VERIFICATION_STATUS_INVALID"
	CodeVerificationReasonRequired      = "VERIFICATION_REASON_REQUIRED"
	CodeVerificationTargetInvalid       = "VERIFICATION_TARGET_INVALID"
	CodeVerificationTargetOccupied      = "VERIFICATION_TARGET_OCCUPIED"
	CodeVerificationTargetVerified      = "VERIFICATION_TARGET_ALREADY_VERIFIED"
	CodeVerificationTargetNotPublic     = "VERIFICATION_TARGET_NOT_PUBLIC"
	CodeVerificationTargetRestricted    = "VERIFICATION_TARGET_RESTRICTED"
	CodeVerificationTargetSystem        = "VERIFICATION_TARGET_SYSTEM"
	CodeVerificationNotOwner            = "VERIFICATION_NOT_OWNER"
	CodeVerificationUserTargetsDisabled = "VERIFICATION_USER_TARGETS_DISABLED"
	CodeVerificationInvalid             = "VERIFICATION_INVALID"
)

// Stable admin error codes for third-party bot verification (see
// botverification.go). They are a separate set from the official verification
// codes above: the two mechanisms own separate tables and fail for separate
// reasons, and the panel renders them in separate sections, so one shared token
// would land a message in the wrong place.
//
// CodeCustomVerificationConflict is the lost optimistic-locking race -- two
// operators deciding at once -- and is the one the panel must render as "reload
// and look again" rather than as a bad request.
const (
	CodeBotVerifierNotFound             = "BOTVERIFIER_NOT_FOUND"
	CodeBotVerifierForbidden            = "BOTVERIFIER_FORBIDDEN"
	CodeBotVerifierInvalid              = "BOTVERIFIER_INVALID"
	CodeBotVerifierBotNotFound          = "BOTVERIFIER_BOT_NOT_FOUND"
	CodeBotVerifierDescriptionForbidden = "BOTVERIFIER_DESCRIPTION_FORBIDDEN"

	CodeVerificationIconNotFound = "VERIFICATION_ICON_NOT_FOUND"
	CodeVerificationIconInactive = "VERIFICATION_ICON_INACTIVE"
	CodeVerificationIconInvalid  = "VERIFICATION_ICON_INVALID"

	CodeCustomVerificationNotFound        = "CUSTOM_VERIFICATION_NOT_FOUND"
	CodeCustomVerificationRequestNotFound = "CUSTOM_VERIFICATION_REQUEST_NOT_FOUND"
	CodeCustomVerificationRequestExists   = "CUSTOM_VERIFICATION_REQUEST_EXISTS"
	CodeCustomVerificationConflict        = "CUSTOM_VERIFICATION_CONFLICT"
	CodeCustomVerificationLimit           = "CUSTOM_VERIFICATION_LIMIT"
	CodeCustomVerificationStatusInvalid   = "CUSTOM_VERIFICATION_STATUS_INVALID"
	CodeCustomVerificationReasonRequired  = "CUSTOM_VERIFICATION_REASON_REQUIRED"
	CodeCustomVerificationTargetInvalid   = "CUSTOM_VERIFICATION_TARGET_INVALID"
	CodeCustomVerificationTargetSystem    = "CUSTOM_VERIFICATION_TARGET_SYSTEM"
	CodeCustomVerificationRateLimited     = "CUSTOM_VERIFICATION_RATE_LIMITED"
	CodeCustomVerificationInvalid         = "CUSTOM_VERIFICATION_INVALID"
)

type CommandRepository interface {
	BeginCommand(ctx context.Context, cmd domain.AdminCommand) (domain.AdminCommand, bool, error)
	FinishCommand(ctx context.Context, commandID string, status domain.AdminCommandStatus, resultJSON []byte, errorText string) (domain.AdminCommand, error)
}

type RestrictionStore interface {
	GetAccountFreeze(ctx context.Context, userID int64) (domain.AccountFreeze, bool, error)
	SetAccountFreeze(ctx context.Context, freeze domain.AccountFreeze) (domain.AccountFreeze, error)
}

type accountFreezeBatchStore interface {
	GetAccountFreezes(ctx context.Context, userIDs []int64) (map[int64]domain.AccountFreeze, error)
}

type accountFreezeNotificationStore interface {
	ClaimAccountFreezeNotifications(ctx context.Context, now time.Time, limit int, lease time.Duration) ([]domain.AccountFreezeNotification, error)
	CompleteAccountFreezeNotification(ctx context.Context, id, version int64, now time.Time) error
}

type AuthService interface {
	ListAuthorizations(ctx context.Context, userID int64) ([]domain.Authorization, error)
	ResetAuthorization(ctx context.Context, userID, hash int64) (domain.Authorization, bool, error)
	ResetAuthorizations(ctx context.Context, userID int64, keepAuthKeyID [8]byte) ([]domain.Authorization, error)
}

type AuthKeyRevoker interface {
	RevokeAuthorizationAuthKey(ctx context.Context, authKeyID [8]byte, userID int64) error
}

type UsersService interface {
	AdminUser(ctx context.Context, userID int64) (domain.User, bool, error)
	GrantPremium(ctx context.Context, userID int64, months int) (domain.User, error)
	SetVerified(ctx context.Context, userID int64, verified bool) (domain.User, error)
	SetScamFake(ctx context.Context, userID int64, scam, fake bool) (domain.User, error)
	SetSupport(ctx context.Context, userID int64, support bool) (domain.User, error)
	UpdateUsername(ctx context.Context, userID int64, username string) (domain.User, error)
	UpdateColor(ctx context.Context, userID int64, forProfile bool, color domain.PeerColor) (domain.User, error)
	UpdateEmojiStatus(ctx context.Context, userID int64, status domain.UserEmojiStatus) (domain.User, error)
	UpdateProfile(ctx context.Context, userID int64, update domain.UserProfileUpdate) (domain.User, error)
	// SetPhone force-sets a user's phone number (no code verification).
	SetPhone(ctx context.Context, userID int64, phone string) (domain.User, error)
}

// AccountService carries the login-email factor (account_passwords table),
// a separate concern from UsersService's users-table fields.
type AccountService interface {
	// ValidLoginEmail reports whether email is an acceptable login/signup
	// email address.
	ValidLoginEmail(email string) bool
	// SetLoginEmail force-sets a user's login/signup email, no OTP required.
	SetLoginEmail(ctx context.Context, userID int64, email string) error
	// ClearLoginEmail removes the login email factor entirely.
	ClearLoginEmail(ctx context.Context, userID int64) error
	// LoginEmail returns a user's current login/signup email, if any.
	LoginEmail(ctx context.Context, userID int64) (string, bool, error)
}

// BroadcastService creates and lists system broadcast campaigns (a message
// from domain.OfficialSystemUserID to all or a hand-picked list of users).
// Delivery itself happens out-of-band via a worker draining the durable
// recipient outbox created here -- this interface only enqueues and reads
// back, so CreateBroadcast never blocks on however many recipients there
// are. Resolving "all users" into an explicit id list is the caller's job
// (cmd/telesrv-admin's readstore, the same place every other account list
// query already lives), not this service's -- it always receives an
// already-resolved id list.
type BroadcastService interface {
	Create(ctx context.Context, message string, targetMode domain.BroadcastTargetMode, recipientUserIDs []int64, createdBy string) (domain.Broadcast, error)
	List(ctx context.Context, beforeID int64, limit int) ([]domain.Broadcast, bool, error)
	Get(ctx context.Context, id int64) (domain.Broadcast, bool, error)
}

type UserNotifier interface {
	NotifyUserChanged(ctx context.Context, u domain.User) error
}

type UserModerationNotifier interface {
	NotifyUserModerationFlagsChanged(ctx context.Context, u domain.User) error
}

type AccountFreezeNotifier interface {
	NotifyAccountFreezeChanged(ctx context.Context, freeze domain.AccountFreeze) error
}

type ChannelsService interface {
	GetChannelByID(ctx context.Context, channelID int64) (domain.Channel, error)
	SetVerified(ctx context.Context, channelID int64, verified bool) (domain.Channel, error)
	SetScamFake(ctx context.Context, channelID int64, scam, fake bool) (domain.Channel, error)
	AdminSetSettings(ctx context.Context, channelID int64, patch domain.ChannelAdminSettings) (domain.Channel, error)
	AdminSetUsername(ctx context.Context, channelID int64, username string) (domain.Channel, error)
	AdminSetColor(ctx context.Context, channelID int64, forProfile bool, color domain.ChannelPeerColor) (domain.Channel, error)
	AdminSetEmojiStatus(ctx context.Context, channelID int64, status domain.ChannelEmojiStatus) (domain.Channel, error)
	AdminSetPhoto(ctx context.Context, channelID int64, photo domain.Photo) (domain.Channel, error)
}

type ChannelNotifier interface {
	NotifyChannelChanged(ctx context.Context, ch domain.Channel) error
}

type MessagesService interface {
	GetMessages(ctx context.Context, userID int64, ids []int) (domain.MessageList, error)
	GetHistory(ctx context.Context, userID int64, filter domain.MessageFilter) (domain.MessageList, error)
	DeleteMessages(ctx context.Context, userID int64, req domain.DeleteMessagesRequest) (domain.DeleteMessagesResult, error)
	DeleteHistory(ctx context.Context, userID int64, req domain.DeleteHistoryRequest) (domain.DeleteMessagesResult, error)
}

// AvatarResolver is the same shape as internal/web's ProfilePhotoResolver, kept as its
// own local interface (rather than importing internal/web) since only this narrow slice
// is needed to serve an account's current profile photo in the admin console.
type AvatarResolver interface {
	CurrentProfilePhotoKind(ctx context.Context, ownerType domain.PeerType, ownerID int64, kind domain.ProfilePhotoKind) (domain.Photo, bool, error)
	GetFile(ctx context.Context, req domain.FileDownloadRequest) (domain.FileChunk, bool, error)
	// ValidateAvatarUpload is a pure check (no store writes), used by a dry-run
	// preview before CreateAvatarFromBytes actually materializes the avatar.
	ValidateAvatarUpload(data []byte) bool
	CreateAvatarFromBytes(ctx context.Context, data []byte, ownerUserID int64) (domain.Photo, error)
	SetCurrentProfilePhotoKind(ctx context.Context, ownerType domain.PeerType, ownerID int64, kind domain.ProfilePhotoKind, photoID int64, date int) (domain.Photo, bool, error)
	// GetPhoto looks up a photo by id directly -- used to read a channel's
	// current avatar, which is denormalized on the channel row as a bare
	// photo_id rather than tracked through CurrentProfilePhotoKind.
	GetPhoto(ctx context.Context, id int64) (domain.Photo, bool, error)
}

// StickerSetsService is the admin-console management surface over sticker/custom-emoji
// sets: no ownership check (see internal/app/files.Service.AdminSetStickerSetArchived),
// so it works for seed-imported system/regular packs as well as user-created ones.
type StickerSetsService interface {
	AdminSetStickerSetArchived(ctx context.Context, setID int64, archived bool) (bool, error)
	AdminSetStickerSetSortOrder(ctx context.Context, setID int64, order int) (bool, error)
	AdminRenameStickerSet(ctx context.Context, setID int64, title string) (domain.StickerSet, error)
	AdminDeleteStickerSet(ctx context.Context, setID int64) (domain.StickerSetKind, error)
	// ValidateStickerMaterialUpload is a pure check (no store writes) so a dry-run
	// preview can validate an uploaded file's shape without materializing it.
	ValidateStickerMaterialUpload(fileName string, data []byte) (mimeType string, ok bool)
	// ValidateAdminCreateStickerSet and ValidateAdminAddStickerToSet are pure
	// checks (no store writes), used by a dry-run preview before the
	// corresponding Admin* call actually mutates the pack.
	ValidateAdminCreateStickerSet(ctx context.Context, title, shortName, emoji string, kind domain.StickerSetKind) error
	ValidateAdminAddStickerToSet(ctx context.Context, setID int64, emoji string) error
	AdminUploadStickerMaterial(ctx context.Context, fileName string, data []byte) (domain.Document, error)
	AdminCreateStickerSet(ctx context.Context, req domain.CreateStickerSetRequest) (domain.StickerSet, []domain.Document, error)
	AdminAddStickerToSet(ctx context.Context, setID int64, item domain.StickerSetItemInput) (domain.StickerSet, []domain.Document, error)
	AdminRemoveStickerFromSet(ctx context.Context, setID int64, documentID int64) (domain.StickerSet, []domain.Document, error)
}

// GifCatalogService is the admin-console management surface over the
// admin-curated GIF catalog the built-in @gif inline bot serves for the
// client's GIF picker.
type GifCatalogService interface {
	// ValidateGifUpload is a pure check (no store writes) so a dry-run preview
	// can validate an uploaded file's shape without materializing it.
	ValidateGifUpload(fileName string, data []byte) (mimeType string, ok bool)
	AdminUploadGifMaterial(ctx context.Context, fileName string, data []byte) (domain.Document, error)
	AdminCreateGifCatalogEntry(ctx context.Context, title string, documentID int64) (domain.GifCatalogEntry, error)
	AdminListGifCatalog(ctx context.Context) ([]domain.GifCatalogEntry, error)
	AdminSetGifCatalogEnabled(ctx context.Context, id int64, enabled bool) (bool, error)
	AdminSetGifCatalogSortOrder(ctx context.Context, id int64, order int) (bool, error)
	AdminSetGifCatalogCategory(ctx context.Context, id int64, category string) (bool, error)
	// AdminAutoCategorizeGifCatalog runs files.ClassifyGifCategory over every
	// currently-uncategorized entry's title and returns how many got a
	// category assigned.
	AdminAutoCategorizeGifCatalog(ctx context.Context) (int, error)
	// AdminDeleteUncategorizedGifs removes every catalog entry with no
	// category, plus its document/blob when nothing else references it.
	// Returns (catalog entries deleted, documents actually deleted).
	AdminDeleteUncategorizedGifs(ctx context.Context) (int, int, error)
	AdminDeleteGifCatalogEntry(ctx context.Context, id int64) (bool, error)
}

// BotService creates bot accounts on behalf of the admin. It mirrors the
// owner-scoped /newbot flow: a bot is a users row (is_bot=true) plus a bots row
// owned by ownerUserID, and the returned token is shown once to the operator.
type BotService interface {
	CreateBot(ctx context.Context, ownerUserID int64, name, username string) (domain.User, string, error)
	DeleteBot(ctx context.Context, botUserID int64) (domain.User, error)
	// AdminExportBotToken returns a non-system bot's current token with no
	// ownership check. Used by ExportBotToken; the token never enters the
	// audit/replay record (see CommandResult.transientDetails).
	AdminExportBotToken(ctx context.Context, botUserID int64) (string, error)
}

// EmojiService renders custom-emoji document animations for the admin emoji
// browser (Lottie JSON, TGS transparently decompressed).
type EmojiService interface {
	DocumentAnimationJSON(ctx context.Context, documentID int64) ([]byte, bool, error)
}

type ModerationService interface {
	ListCases(ctx context.Context, filter domain.ModerationCaseFilter) ([]domain.ModerationCase, error)
	Case(ctx context.Context, caseID int64) (domain.ModerationCaseDetail, bool, error)
	Report(ctx context.Context, reportID int64) (domain.ModerationReport, bool, error)
	ClaimCase(ctx context.Context, caseID, expectedVersion int64, actor string, now time.Time) (domain.ModerationCase, error)
	DecideCase(ctx context.Context, request domain.ModerationDecisionRequest) (domain.ModerationCaseDetail, bool, error)
	SubmitAppeal(ctx context.Context, caseID, appellantUserID int64, text string, now time.Time) (domain.ModerationAppeal, bool, error)
	ReviewAppeal(ctx context.Context, request domain.ModerationDecisionRequest) (domain.ModerationCaseDetail, bool, error)
}

// CollectibleUsernamesService is the operator-facing slice of the collectible
// username use cases: the mint/transfer/revoke lifecycle plus the reads the
// admin panel explains an asset with. It is deliberately narrow -- the client
// facing toggle/reorder entry points stay out of the admin surface, because the
// editable slot and the row order belong to the peer, not to the operator.
type CollectibleUsernamesService interface {
	Mint(ctx context.Context, req domain.MintCollectibleUsernameRequest) (domain.CollectibleUsername, bool, error)
	Transfer(ctx context.Context, req domain.TransferCollectibleUsernameRequest) (domain.CollectibleUsername, bool, error)
	Revoke(ctx context.Context, req domain.RevokeCollectibleUsernameRequest) (domain.CollectibleUsername, bool, error)
	Delete(ctx context.Context, req domain.DeleteCollectibleUsernameRequest) (bool, error)
	Collectible(ctx context.Context, username string) (domain.CollectibleUsername, error)
	List(ctx context.Context, filter domain.CollectibleUsernameFilter) ([]domain.CollectibleUsername, error)
	Transfers(ctx context.Context, collectibleID int64, limit int) ([]domain.CollectibleUsernameTransfer, error)
}

// collectibleUsernameByIDLookup is the optional by-identity read. Stores that
// expose it answer a detail request in one round trip; the keyset fallback in
// CollectibleUsernameByID keeps a service without it correct.
type collectibleUsernameByIDLookup interface {
	CollectibleUsernameByID(ctx context.Context, id int64) (domain.CollectibleUsername, error)
}

type Dependencies struct {
	Commands               CommandRepository
	Restrictions           RestrictionStore
	Auth                   AuthService
	Revoker                AuthKeyRevoker
	Users                  UsersService
	UserNotifier           UserNotifier
	UserModerationNotifier UserModerationNotifier
	FreezeNotifier         AccountFreezeNotifier
	Channels               ChannelsService
	ChannelNotifier        ChannelNotifier
	Messages               MessagesService
	Photos                 AvatarResolver
	StickerSets            StickerSetsService
	GifCatalog             GifCatalogService
	Bots                   BotService
	Emoji                  EmojiService
	Moderation             ModerationService
	Usernames              CollectibleUsernamesService
	Verification           VerificationService
	// BotVerification is the third-party mechanism, wired separately from
	// Verification: the two never read each other's state.
	BotVerification BotVerificationService
	// Account carries the login-email factor -- a separate app service from
	// Users, since login email lives in account_passwords, not users.
	Account AccountService
	// Broadcast is the system-broadcast (777000) create/list/get surface.
	Broadcast BroadcastService
	Now       func() time.Time
}

type Service struct {
	commands               CommandRepository
	restrictions           RestrictionStore
	auth                   AuthService
	revoker                AuthKeyRevoker
	users                  UsersService
	userNotifier           UserNotifier
	userModerationNotifier UserModerationNotifier
	freezeNotifier         AccountFreezeNotifier
	channels               ChannelsService
	channelNotifier        ChannelNotifier
	messages               MessagesService
	photos                 AvatarResolver
	stickerSets            StickerSetsService
	gifCatalog             GifCatalogService
	bots                   BotService
	emoji                  EmojiService
	moderation             ModerationService
	usernames              CollectibleUsernamesService
	verification           VerificationService
	botVerification        BotVerificationService
	account                AccountService
	broadcast              BroadcastService
	now                    func() time.Time
}

func NewService(deps Dependencies) *Service {
	s := &Service{now: time.Now}
	return s.Configure(deps)
}

func (s *Service) Configure(deps Dependencies) *Service {
	if deps.Commands != nil {
		s.commands = deps.Commands
	}
	if deps.Restrictions != nil {
		s.restrictions = deps.Restrictions
	}
	if deps.Auth != nil {
		s.auth = deps.Auth
	}
	if deps.Revoker != nil {
		s.revoker = deps.Revoker
	}
	if deps.Users != nil {
		s.users = deps.Users
	}
	if deps.UserNotifier != nil {
		s.userNotifier = deps.UserNotifier
	}
	if deps.UserModerationNotifier != nil {
		s.userModerationNotifier = deps.UserModerationNotifier
	}
	if deps.FreezeNotifier != nil {
		s.freezeNotifier = deps.FreezeNotifier
	}
	if deps.Channels != nil {
		s.channels = deps.Channels
	}
	if deps.ChannelNotifier != nil {
		s.channelNotifier = deps.ChannelNotifier
	}
	if deps.Messages != nil {
		s.messages = deps.Messages
	}
	if deps.Photos != nil {
		s.photos = deps.Photos
	}
	if deps.StickerSets != nil {
		s.stickerSets = deps.StickerSets
	}
	if deps.GifCatalog != nil {
		s.gifCatalog = deps.GifCatalog
	}
	if deps.Bots != nil {
		s.bots = deps.Bots
	}
	if deps.Emoji != nil {
		s.emoji = deps.Emoji
	}
	if deps.Moderation != nil {
		s.moderation = deps.Moderation
	}
	if deps.Usernames != nil {
		s.usernames = deps.Usernames
	}
	if deps.Verification != nil {
		s.verification = deps.Verification
	}
	if deps.BotVerification != nil {
		s.botVerification = deps.BotVerification
	}
	if deps.Account != nil {
		s.account = deps.Account
	}
	if deps.Broadcast != nil {
		s.broadcast = deps.Broadcast
	}
	if deps.Now != nil {
		s.now = deps.Now
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

func (s *Service) ModerationCases(ctx context.Context, filter domain.ModerationCaseFilter) ([]domain.ModerationCase, error) {
	if s == nil || s.moderation == nil {
		return nil, fmt.Errorf("moderation dependency is not configured")
	}
	return s.moderation.ListCases(ctx, filter)
}

func (s *Service) ModerationCase(ctx context.Context, caseID int64) (domain.ModerationCaseDetail, bool, error) {
	if s == nil || s.moderation == nil {
		return domain.ModerationCaseDetail{}, false, fmt.Errorf("moderation dependency is not configured")
	}
	return s.moderation.Case(ctx, caseID)
}

func (s *Service) ModerationReport(ctx context.Context, reportID int64) (domain.ModerationReport, bool, error) {
	if s == nil || s.moderation == nil {
		return domain.ModerationReport{}, false, fmt.Errorf("moderation dependency is not configured")
	}
	return s.moderation.Report(ctx, reportID)
}

func (s *Service) ClaimModerationCase(ctx context.Context, caseID, expectedVersion int64, actor string) (domain.ModerationCase, error) {
	if s == nil || s.moderation == nil {
		return domain.ModerationCase{}, fmt.Errorf("moderation dependency is not configured")
	}
	return s.moderation.ClaimCase(ctx, caseID, expectedVersion, actor, s.now().UTC())
}

func (s *Service) DecideModerationCase(ctx context.Context, request domain.ModerationDecisionRequest) (domain.ModerationCaseDetail, bool, error) {
	if s == nil || s.moderation == nil {
		return domain.ModerationCaseDetail{}, false, fmt.Errorf("moderation dependency is not configured")
	}
	if request.CreatedAt.IsZero() {
		request.CreatedAt = s.now().UTC()
	}
	return s.moderation.DecideCase(ctx, request)
}

func (s *Service) SubmitModerationAppeal(ctx context.Context, caseID, appellantUserID int64, text string) (domain.ModerationAppeal, bool, error) {
	if s == nil || s.moderation == nil {
		return domain.ModerationAppeal{}, false, fmt.Errorf("moderation dependency is not configured")
	}
	return s.moderation.SubmitAppeal(ctx, caseID, appellantUserID, text, s.now().UTC())
}

func (s *Service) ReviewModerationAppeal(ctx context.Context, request domain.ModerationDecisionRequest) (domain.ModerationCaseDetail, bool, error) {
	if s == nil || s.moderation == nil {
		return domain.ModerationCaseDetail{}, false, fmt.Errorf("moderation dependency is not configured")
	}
	if request.CreatedAt.IsZero() {
		request.CreatedAt = s.now().UTC()
	}
	return s.moderation.ReviewAppeal(ctx, request)
}

// CollectibleUsernames is the admin listing read. The filter is passed through
// unchanged: the use-case layer owns normalisation and the page bound, so the
// admin API and the RPC edge page the registry identically.
func (s *Service) CollectibleUsernames(ctx context.Context, filter domain.CollectibleUsernameFilter) ([]domain.CollectibleUsername, error) {
	if s == nil || s.usernames == nil {
		return nil, fmt.Errorf("collectible username dependency is not configured")
	}
	return s.usernames.List(ctx, filter)
}

// CollectibleUsername resolves one asset by name.
func (s *Service) CollectibleUsername(ctx context.Context, username string) (domain.CollectibleUsername, error) {
	if s == nil || s.usernames == nil {
		return domain.CollectibleUsername{}, fmt.Errorf("collectible username dependency is not configured")
	}
	return s.usernames.Collectible(ctx, username)
}

// CollectibleUsernameByID resolves one asset by identity, which is how the admin
// panel links a row to its detail view.
//
// A service exposing the direct by-identity read is used as-is. Otherwise the
// bounded keyset listing answers it: the listing is ordered by descending id, so
// the single row taken before id+1 is the asset itself whenever it exists, and
// any other id proves the asset is gone.
func (s *Service) CollectibleUsernameByID(ctx context.Context, id int64) (domain.CollectibleUsername, error) {
	if s == nil || s.usernames == nil {
		return domain.CollectibleUsername{}, fmt.Errorf("collectible username dependency is not configured")
	}
	if id <= 0 {
		return domain.CollectibleUsername{}, domain.ErrCollectibleUsernameNotFound
	}
	if lookup, ok := s.usernames.(collectibleUsernameByIDLookup); ok {
		return lookup.CollectibleUsernameByID(ctx, id)
	}
	before := int64(0)
	if id < math.MaxInt64 {
		before = id + 1
	}
	items, err := s.usernames.List(ctx, domain.CollectibleUsernameFilter{BeforeID: before, Limit: 1})
	if err != nil {
		return domain.CollectibleUsername{}, err
	}
	if len(items) == 0 || items[0].ID != id {
		return domain.CollectibleUsername{}, domain.ErrCollectibleUsernameNotFound
	}
	return items[0], nil
}

// CollectibleUsernameTransfers returns one asset's provenance log, newest first.
func (s *Service) CollectibleUsernameTransfers(ctx context.Context, collectibleID int64, limit int) ([]domain.CollectibleUsernameTransfer, error) {
	if s == nil || s.usernames == nil {
		return nil, fmt.Errorf("collectible username dependency is not configured")
	}
	return s.usernames.Transfers(ctx, collectibleID, limit)
}

type CommandMeta struct {
	CommandID string `json:"command_id"`
	Actor     string `json:"actor"`
	Reason    string `json:"reason"`
	DryRun    bool   `json:"dry_run"`
}

type CommandResult struct {
	CommandID       string         `json:"command_id"`
	Action          string         `json:"action"`
	Status          string         `json:"status"`
	AlreadyExecuted bool           `json:"already_executed"`
	DryRun          bool           `json:"dry_run"`
	TargetUserID    int64          `json:"target_user_id,omitempty"`
	TargetPeer      domain.Peer    `json:"target_peer,omitempty"`
	Message         string         `json:"message"`
	Details         map[string]any `json:"details,omitempty"`
	Error           string         `json:"error,omitempty"`
	// transientDetails are returned to the initiating caller only. They are
	// deliberately excluded from JSON so credentials can never enter command
	// replay or audit storage.
	transientDetails map[string]any
}

type SetStickerSetArchivedRequest struct {
	CommandMeta
	SetID    int64 `json:"set_id"`
	Archived bool  `json:"archived"`
}

type SetStickerSetSortOrderRequest struct {
	CommandMeta
	SetID     int64 `json:"set_id"`
	SortOrder int   `json:"sort_order"`
}

type RenameStickerSetRequest struct {
	CommandMeta
	SetID int64  `json:"set_id"`
	Title string `json:"title"`
}

type DeleteStickerSetRequest struct {
	CommandMeta
	SetID int64 `json:"set_id"`
}

type CreateStickerSetRequest struct {
	CommandMeta
	Title     string `json:"title"`
	ShortName string `json:"short_name"`
	Kind      string `json:"kind"`
	Emoji     string `json:"emoji"`
	Keywords  string `json:"keywords,omitempty"`
	FileName  string `json:"file_name"`
	Data      []byte `json:"-"`
}

type AddStickerToSetRequest struct {
	CommandMeta
	SetID    int64  `json:"set_id"`
	Emoji    string `json:"emoji"`
	Keywords string `json:"keywords,omitempty"`
	FileName string `json:"file_name"`
	Data     []byte `json:"-"`
}

type RemoveStickerFromSetRequest struct {
	CommandMeta
	SetID      int64 `json:"set_id"`
	DocumentID int64 `json:"document_id"`
}

type CreateGifCatalogEntryRequest struct {
	CommandMeta
	Title         string `json:"title"`
	FileName      string `json:"file_name"`
	Data          []byte `json:"-"`
	ContentSHA256 string `json:"content_sha256,omitempty"`
}

type SetGifCatalogEnabledRequest struct {
	CommandMeta
	ID      int64 `json:"id"`
	Enabled bool  `json:"enabled"`
}

type SetGifCatalogSortOrderRequest struct {
	CommandMeta
	ID        int64 `json:"id"`
	SortOrder int   `json:"sort_order"`
}

type SetGifCatalogCategoryRequest struct {
	CommandMeta
	ID       int64  `json:"id"`
	Category string `json:"category"`
}

type AutoCategorizeGifCatalogRequest struct {
	CommandMeta
}

type DeleteUncategorizedGifsRequest struct {
	CommandMeta
}

type DeleteGifCatalogEntryRequest struct {
	CommandMeta
	ID int64 `json:"id"`
}

type SetAccountFrozenRequest struct {
	CommandMeta
	UserID    int64     `json:"user_id"`
	Frozen    bool      `json:"frozen"`
	Until     time.Time `json:"freeze_until,omitempty"`
	AppealURL string    `json:"freeze_appeal_url,omitempty"`
}

type GrantPremiumRequest struct {
	CommandMeta
	UserID int64 `json:"user_id"`
	Months int   `json:"months"`
}

type SetVerifiedRequest struct {
	CommandMeta
	UserID   int64 `json:"user_id"`
	Verified bool  `json:"verified"`
}

type SetChannelVerifiedRequest struct {
	CommandMeta
	ChannelID int64 `json:"channel_id"`
	Verified  bool  `json:"verified"`
}

type SetUserFlagsRequest struct {
	CommandMeta
	UserID int64 `json:"user_id"`
	Scam   bool  `json:"scam"`
	Fake   bool  `json:"fake"`
}

type SetChannelFlagsRequest struct {
	CommandMeta
	ChannelID int64 `json:"channel_id"`
	Scam      bool  `json:"scam"`
	Fake      bool  `json:"fake"`
}

type SetSupportRequest struct {
	CommandMeta
	UserID  int64 `json:"user_id"`
	Support bool  `json:"support"`
}

type SetUsernameRequest struct {
	CommandMeta
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
}

// SetProfileRequest updates first/last name. Both are always sent (not
// pointer/omitempty): the admin form always shows and submits both fields
// together, so there is no "leave unset" case to represent here.
type SetProfileRequest struct {
	CommandMeta
	UserID    int64  `json:"user_id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type SetPhoneRequest struct {
	CommandMeta
	UserID int64  `json:"user_id"`
	Phone  string `json:"phone"`
}

// SetLoginEmailRequest force-sets (or, if Email is empty, clears) a user's
// login/signup email.
type SetLoginEmailRequest struct {
	CommandMeta
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
}

type SetAccountAvatarRequest struct {
	CommandMeta
	UserID   int64  `json:"user_id"`
	FileName string `json:"file_name"`
	Data     []byte `json:"-"`
}

type SetChannelAvatarRequest struct {
	CommandMeta
	ChannelID int64  `json:"channel_id"`
	FileName  string `json:"file_name"`
	Data      []byte `json:"-"`
}

type SetChannelUsernameRequest struct {
	CommandMeta
	ChannelID int64  `json:"channel_id"`
	Username  string `json:"username"`
}

type PeerColorInput struct {
	ForProfile        bool  `json:"for_profile"`
	HasColor          bool  `json:"has_color"`
	Color             int   `json:"color"`
	BackgroundEmojiID int64 `json:"background_emoji_id,string"`
}

type SetUserColorRequest struct {
	CommandMeta
	UserID int64 `json:"user_id"`
	PeerColorInput
}

type SetChannelColorRequest struct {
	CommandMeta
	ChannelID int64 `json:"channel_id"`
	PeerColorInput
}

type EmojiStatusInput struct {
	DocumentID int64 `json:"document_id,string"`
	Until      int   `json:"until"`
}

type SetUserEmojiStatusRequest struct {
	CommandMeta
	UserID int64 `json:"user_id"`
	EmojiStatusInput
}

type SetChannelEmojiStatusRequest struct {
	CommandMeta
	ChannelID int64 `json:"channel_id"`
	EmojiStatusInput
}

type SetChannelSettingsRequest struct {
	CommandMeta
	ChannelID          int64 `json:"channel_id"`
	Gigagroup          *bool `json:"gigagroup,omitempty"`
	AntiSpam           *bool `json:"antispam,omitempty"`
	ParticipantsHidden *bool `json:"participants_hidden,omitempty"`
	NoForwards         *bool `json:"noforwards,omitempty"`
	JoinToSend         *bool `json:"join_to_send,omitempty"`
	JoinRequest        *bool `json:"join_request,omitempty"`
	SlowmodeSeconds    *int  `json:"slowmode_seconds,omitempty"`
}

type CreateBotRequest struct {
	CommandMeta
	OwnerUserID int64  `json:"owner_user_id"`
	Name        string `json:"name"`
	Username    string `json:"username"`
}

type DeleteBotRequest struct {
	CommandMeta
	BotUserID int64 `json:"bot_user_id"`
}

// CreateBroadcastRequest's UserIDs is always an already-resolved recipient
// list -- for TargetMode "all" the caller (cmd/telesrv-admin's readstore
// proxy) has already turned "every user" into an explicit id list before
// this reaches the admin service, so CreateBroadcast never has to know how
// to enumerate accounts itself.
type CreateBroadcastRequest struct {
	CommandMeta
	Message    string  `json:"message"`
	TargetMode string  `json:"target_mode"`
	UserIDs    []int64 `json:"user_ids"`
}

type ExportBotTokenRequest struct {
	CommandMeta
	BotUserID int64 `json:"bot_user_id"`
}

// MintCollectibleUsernameRequest mints a collectible username asset. At most one
// of OwnerUserID / OwnerChannelID may be set: neither mints into the operator
// vault, one assigns the asset to that holder in the same command.
//
// Amount and CryptoAmount are minor units (nanotons for TON), so they cross the
// JSON boundary as decimal strings and stay exact. PurchaseDate is an optional
// Unix timestamp; zero is stamped with the command clock.
type MintCollectibleUsernameRequest struct {
	CommandMeta
	Username       string `json:"username"`
	OwnerUserID    int64  `json:"owner_user_id,string,omitempty"`
	OwnerChannelID int64  `json:"owner_channel_id,string,omitempty"`
	Currency       string `json:"currency"`
	Amount         int64  `json:"amount,string"`
	CryptoCurrency string `json:"crypto_currency,omitempty"`
	CryptoAmount   int64  `json:"crypto_amount,string,omitempty"`
	URL            string `json:"url,omitempty"`
	PurchaseDate   int64  `json:"purchase_date,omitempty"`
}

// TransferCollectibleUsernameRequest moves an asset out of the vault or between
// holders. Exactly one of ToUserID / ToChannelID identifies the new holder.
type TransferCollectibleUsernameRequest struct {
	CommandMeta
	Username    string `json:"username"`
	ToUserID    int64  `json:"to_user_id,string,omitempty"`
	ToChannelID int64  `json:"to_channel_id,string,omitempty"`
}

// RevokeCollectibleUsernameRequest returns an asset to the vault, or retires it
// permanently when Burn is set.
type RevokeCollectibleUsernameRequest struct {
	CommandMeta
	Username            string `json:"username"`
	ExpectedOwnerUserID int64  `json:"expected_owner_user_id,string,omitempty"`
	Burn                bool   `json:"burn"`
}

// DeleteCollectibleUsernameRequest erases a collectible asset entirely. Unlike a
// burn, which retires the asset and keeps its provenance, this drops the record
// and frees the name for a fresh issue -- the escape hatch for a mistaken mint.
type DeleteCollectibleUsernameRequest struct {
	CommandMeta
	Username string `json:"username"`
}

type RevokeSessionsRequest struct {
	CommandMeta
	UserID    int64 `json:"user_id"`
	Hash      int64 `json:"hash,omitempty"`
	KeepHash  int64 `json:"keep_hash,omitempty"`
	RevokeAll bool  `json:"revoke_all,omitempty"`
}

type DeletePrivateMessagesRequest struct {
	CommandMeta
	OwnerUserID int64       `json:"owner_user_id"`
	Peer        domain.Peer `json:"peer"`
	IDs         []int       `json:"ids"`
	Revoke      bool        `json:"revoke"`
}

type DeletePrivateHistoryRequest struct {
	CommandMeta
	OwnerUserID int64       `json:"owner_user_id"`
	Peer        domain.Peer `json:"peer"`
	MaxID       int         `json:"max_id,omitempty"`
	MinDate     int         `json:"min_date,omitempty"`
	MaxDate     int         `json:"max_date,omitempty"`
	JustClear   bool        `json:"just_clear,omitempty"`
	Revoke      bool        `json:"revoke"`
	MaxBatches  int         `json:"max_batches,omitempty"`
}

// AccountFreeze returns the durable account-level freeze state. A missing row
// is the only non-frozen default; invalid active rows are rejected by the
// store/schema instead of normalized on read.
func (s *Service) AccountFreeze(ctx context.Context, userID int64) (domain.AccountFreeze, bool, error) {
	if s == nil || s.restrictions == nil || userID == 0 {
		return domain.AccountFreeze{}, false, nil
	}
	freeze, found, err := s.restrictions.GetAccountFreeze(ctx, userID)
	if err != nil || !found {
		return freeze, found, err
	}
	if err := validateAccountFreeze(freeze); err != nil {
		return domain.AccountFreeze{}, false, fmt.Errorf("invalid durable account freeze for user %d: %w", userID, err)
	}
	return freeze, true, nil
}

// AccountFreezes is the bounded-query projection API used by user hydration.
// Production stores use array batches; lightweight test stores keep the exact
// same semantics through the single-row fallback.
func (s *Service) AccountFreezes(ctx context.Context, userIDs []int64) (map[int64]domain.AccountFreeze, error) {
	out := make(map[int64]domain.AccountFreeze)
	if s == nil || s.restrictions == nil || len(userIDs) == 0 {
		return out, nil
	}
	ids := uniqueFreezeUserIDs(userIDs)
	if batch, ok := s.restrictions.(accountFreezeBatchStore); ok {
		const batchSize = 1000
		for start := 0; start < len(ids); start += batchSize {
			end := min(start+batchSize, len(ids))
			items, err := batch.GetAccountFreezes(ctx, ids[start:end])
			if err != nil {
				return nil, err
			}
			for id, freeze := range items {
				if err := validateAccountFreeze(freeze); err != nil {
					return nil, fmt.Errorf("invalid durable account freeze for user %d: %w", id, err)
				}
				if freeze.Frozen {
					out[id] = freeze
				}
			}
		}
		return out, nil
	}
	for _, id := range ids {
		freeze, found, err := s.AccountFreeze(ctx, id)
		if err != nil {
			return nil, err
		}
		if found && freeze.Frozen {
			out[id] = freeze
		}
	}
	return out, nil
}

func uniqueFreezeUserIDs(userIDs []int64) []int64 {
	out := make([]int64, 0, len(userIDs))
	seen := make(map[int64]struct{}, len(userIDs))
	for _, id := range userIDs {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func (s *Service) ClaimAccountFreezeNotifications(ctx context.Context, now time.Time, limit int, lease time.Duration) ([]domain.AccountFreezeNotification, error) {
	store, ok := s.restrictions.(accountFreezeNotificationStore)
	if !ok {
		return nil, nil
	}
	return store.ClaimAccountFreezeNotifications(ctx, now, limit, lease)
}

func (s *Service) CompleteAccountFreezeNotification(ctx context.Context, id, version int64, now time.Time) error {
	store, ok := s.restrictions.(accountFreezeNotificationStore)
	if !ok {
		return nil
	}
	return store.CompleteAccountFreezeNotification(ctx, id, version, now)
}

func validateAccountFreeze(freeze domain.AccountFreeze) error {
	if !freeze.Frozen {
		if !freeze.Since.IsZero() || !freeze.Until.IsZero() || freeze.AppealURL != "" {
			return fmt.Errorf("inactive freeze retains client-visible state")
		}
		return nil
	}
	if freeze.Since.IsZero() || freeze.Until.IsZero() || !freeze.Until.After(freeze.Since) ||
		freeze.Since.Unix() <= 0 || freeze.Until.Unix() > math.MaxInt32 {
		return fmt.Errorf("active freeze has invalid since/until")
	}
	if len(freeze.AppealURL) > maxFreezeAppealURLLength {
		return fmt.Errorf("active freeze appeal URL is too long")
	}
	parsed, err := url.ParseRequestURI(freeze.AppealURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("active freeze has invalid appeal URL")
	}
	return nil
}

func (s *Service) CanSendMessages(ctx context.Context, userID int64) error {
	freeze, found, err := s.AccountFreeze(ctx, userID)
	if err != nil {
		return err
	}
	if found && freeze.Frozen {
		return domain.ErrUserFrozen
	}
	return nil
}

func (s *Service) SetAccountFrozen(ctx context.Context, req SetAccountFrozenRequest) (CommandResult, error) {
	if req.UserID <= 0 {
		return CommandResult{}, fmt.Errorf("user_id is required")
	}
	if s == nil || s.restrictions == nil {
		return CommandResult{}, fmt.Errorf("admin restriction store is not configured")
	}
	now := s.now().UTC()
	appealURL := strings.TrimSpace(req.AppealURL)
	if req.Frozen {
		if req.Until.IsZero() || req.Until.Unix() > math.MaxInt32 {
			return CommandResult{}, fmt.Errorf("freeze_until must be a non-zero int32 Unix timestamp")
		}
		if len(appealURL) > maxFreezeAppealURLLength {
			return CommandResult{}, fmt.Errorf("freeze_appeal_url must be <= %d bytes", maxFreezeAppealURLLength)
		}
		parsed, err := url.ParseRequestURI(appealURL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return CommandResult{}, fmt.Errorf("freeze_appeal_url must be an absolute HTTP(S) URL")
		}
	}
	return s.runCommand(ctx, req.CommandMeta, ActionSetAccountFrozen, req.UserID, domain.Peer{}, req, func() (CommandResult, error) {
		// Keep this time-relative check inside runCommand: a completed command ID
		// must remain replayable after its deadline, while a new stale request is
		// recorded as failed and cannot mutate the restriction row.
		if req.Frozen && !req.Until.After(now) {
			return CommandResult{}, fmt.Errorf("freeze_until must be in the future")
		}
		prev, found, err := s.restrictions.GetAccountFreeze(ctx, req.UserID)
		if err != nil {
			return CommandResult{}, err
		}
		next := domain.AccountFreeze{
			UserID:    req.UserID,
			Frozen:    req.Frozen,
			Reason:    req.Reason,
			Actor:     req.Actor,
			CommandID: req.CommandID,
		}
		if req.Frozen {
			next.Since = now
			if found && prev.Frozen {
				next.Since = prev.Since
			}
			next.Until = req.Until.UTC()
			next.AppealURL = appealURL
			if !next.Until.After(next.Since) {
				return CommandResult{}, fmt.Errorf("freeze_until must be after freeze_since")
			}
		}
		wouldChange := !found || prev.Frozen != next.Frozen ||
			!prev.Since.Equal(next.Since) || !prev.Until.Equal(next.Until) ||
			prev.AppealURL != next.AppealURL
		details := map[string]any{
			"previous_frozen": found && prev.Frozen,
			"new_frozen":      req.Frozen,
			"would_change":    wouldChange,
		}
		if req.Frozen {
			details["freeze_since"] = next.Since.Format(time.RFC3339)
			details["freeze_until"] = next.Until.Format(time.RFC3339)
			details["freeze_appeal_url"] = next.AppealURL
		}
		if req.DryRun {
			return CommandResult{Message: "dry-run completed", Details: details}, nil
		}
		updated, err := s.restrictions.SetAccountFreeze(ctx, next)
		if err != nil {
			return CommandResult{}, err
		}
		details["updated_at"] = updated.UpdatedAt.UTC().Format(time.RFC3339)
		details["version"] = updated.Version
		if err := s.notifyAccountFreezeChanged(ctx, updated); err != nil {
			details["notify_error"] = err.Error()
		}
		return CommandResult{Message: "account freeze updated", Details: details}, nil
	})
}

func (s *Service) GrantPremium(ctx context.Context, req GrantPremiumRequest) (CommandResult, error) {
	if req.UserID <= 0 {
		return CommandResult{}, fmt.Errorf("user_id is required")
	}
	if req.Months < 0 || req.Months > maxPremiumMonths {
		return CommandResult{}, fmt.Errorf("months must be between 0 and %d", maxPremiumMonths)
	}
	if s == nil || s.users == nil {
		return CommandResult{}, fmt.Errorf("admin user dependency is not configured")
	}
	return s.runCommand(ctx, req.CommandMeta, ActionGrantPremium, req.UserID, domain.Peer{}, req, func() (CommandResult, error) {
		u, found, err := s.users.AdminUser(ctx, req.UserID)
		if err != nil {
			return CommandResult{}, err
		}
		if !found {
			return CommandResult{}, domain.ErrUserNotFound
		}
		if u.Bot {
			return CommandResult{}, domain.ErrPremiumBotUnsupported
		}
		details := premiumCommandDetails(u, req.Months, s.now())
		if req.DryRun {
			return CommandResult{Message: "dry-run completed", Details: details}, nil
		}
		updated, err := s.users.GrantPremium(ctx, req.UserID, req.Months)
		if err != nil {
			return CommandResult{}, err
		}
		details["updated_premium_until"] = updated.PremiumUntil
		details["updated_premium_active"] = updated.PremiumActiveAt(s.now().Unix())
		if err := s.notifyUserChanged(ctx, updated); err != nil {
			details["notify_error"] = err.Error()
		}
		msg := "premium updated"
		if req.Months == 0 {
			msg = "premium cleared"
		}
		return CommandResult{Message: msg, Details: details}, nil
	})
}

func (s *Service) SetVerified(ctx context.Context, req SetVerifiedRequest) (CommandResult, error) {
	if req.UserID <= 0 {
		return CommandResult{}, fmt.Errorf("user_id is required")
	}
	if s == nil || s.users == nil {
		return CommandResult{}, fmt.Errorf("admin user dependency is not configured")
	}
	return s.runCommand(ctx, req.CommandMeta, ActionSetVerified, req.UserID, domain.Peer{}, req, func() (CommandResult, error) {
		u, found, err := s.users.AdminUser(ctx, req.UserID)
		if err != nil {
			return CommandResult{}, err
		}
		if !found {
			return CommandResult{}, domain.ErrUserNotFound
		}
		details := map[string]any{
			"previous_verified": u.Verified,
			"new_verified":      req.Verified,
			"would_change":      u.Verified != req.Verified,
		}
		if req.DryRun {
			return CommandResult{Message: "dry-run completed", Details: details}, nil
		}
		updated, err := s.users.SetVerified(ctx, req.UserID, req.Verified)
		if err != nil {
			return CommandResult{}, err
		}
		details["updated_verified"] = updated.Verified
		if err := s.notifyUserChanged(ctx, updated); err != nil {
			details["notify_error"] = err.Error()
		}
		return CommandResult{Message: "verified updated", Details: details}, nil
	})
}

// SetUserFlags sets or clears the scam/fake moderation flags on a user (bots
// reuse the same path). Both flags are applied together from the desired state.
func (s *Service) SetUserFlags(ctx context.Context, req SetUserFlagsRequest) (CommandResult, error) {
	if req.UserID <= 0 {
		return CommandResult{}, fmt.Errorf("user_id is required")
	}
	if s == nil || s.users == nil {
		return CommandResult{}, fmt.Errorf("admin user dependency is not configured")
	}
	if req.Scam && req.Fake {
		return CommandResult{}, domain.ErrPeerModerationFlagsInvalid
	}
	return s.runCommand(ctx, req.CommandMeta, ActionSetUserFlags, req.UserID, domain.Peer{}, req, func() (CommandResult, error) {
		u, found, err := s.users.AdminUser(ctx, req.UserID)
		if err != nil {
			return CommandResult{}, err
		}
		if !found {
			return CommandResult{}, domain.ErrUserNotFound
		}
		details := map[string]any{
			"previous_scam": u.Scam, "previous_fake": u.Fake,
			"new_scam": req.Scam, "new_fake": req.Fake,
			"would_change": u.Scam != req.Scam || u.Fake != req.Fake,
		}
		if req.DryRun {
			return CommandResult{Message: "dry-run completed", Details: details}, nil
		}
		updated, err := s.users.SetScamFake(ctx, req.UserID, req.Scam, req.Fake)
		if err != nil {
			return CommandResult{}, err
		}
		details["updated_scam"] = updated.Scam
		details["updated_fake"] = updated.Fake
		if err := s.notifyUserModerationFlagsChanged(ctx, updated); err != nil {
			details["notify_error"] = err.Error()
		}
		return CommandResult{Message: "user flags updated", Details: details}, nil
	})
}

// SetSupport sets or clears the official-support flag on a user.
func (s *Service) SetSupport(ctx context.Context, req SetSupportRequest) (CommandResult, error) {
	if req.UserID <= 0 {
		return CommandResult{}, fmt.Errorf("user_id is required")
	}
	if s == nil || s.users == nil {
		return CommandResult{}, fmt.Errorf("admin user dependency is not configured")
	}
	return s.runCommand(ctx, req.CommandMeta, ActionSetSupport, req.UserID, domain.Peer{}, req, func() (CommandResult, error) {
		u, found, err := s.users.AdminUser(ctx, req.UserID)
		if err != nil {
			return CommandResult{}, err
		}
		if !found {
			return CommandResult{}, domain.ErrUserNotFound
		}
		details := map[string]any{"previous_support": u.Support, "new_support": req.Support, "would_change": u.Support != req.Support}
		if req.DryRun {
			return CommandResult{Message: "dry-run completed", Details: details}, nil
		}
		updated, err := s.users.SetSupport(ctx, req.UserID, req.Support)
		if err != nil {
			return CommandResult{}, err
		}
		details["updated_support"] = updated.Support
		if err := s.notifyUserChanged(ctx, updated); err != nil {
			details["notify_error"] = err.Error()
		}
		return CommandResult{Message: "support updated", Details: details}, nil
	})
}

func (s *Service) SetUsername(ctx context.Context, req SetUsernameRequest) (CommandResult, error) {
	if req.UserID <= 0 {
		return CommandResult{}, fmt.Errorf("user_id is required")
	}
	if s == nil || s.users == nil {
		return CommandResult{}, fmt.Errorf("admin user dependency is not configured")
	}
	username := strings.TrimSpace(strings.TrimPrefix(req.Username, "@"))
	req.Username = username
	return s.runCommand(ctx, req.CommandMeta, ActionSetUsername, req.UserID, domain.Peer{}, req, func() (CommandResult, error) {
		u, found, err := s.users.AdminUser(ctx, req.UserID)
		if err != nil {
			return CommandResult{}, err
		}
		if !found {
			return CommandResult{}, domain.ErrUserNotFound
		}
		details := map[string]any{"previous_username": u.Username, "new_username": username}
		if req.DryRun {
			return CommandResult{Message: "dry-run completed", Details: details}, nil
		}
		updated, err := s.users.UpdateUsername(ctx, req.UserID, username)
		if err != nil {
			return CommandResult{}, err
		}
		details["updated_username"] = updated.Username
		if err := s.notifyUserChanged(ctx, updated); err != nil {
			details["notify_error"] = err.Error()
		}
		return CommandResult{Message: "username updated", Details: details}, nil
	})
}

// SetProfile force-sets a user's first and last name.
func (s *Service) SetProfile(ctx context.Context, req SetProfileRequest) (CommandResult, error) {
	if req.UserID <= 0 {
		return CommandResult{}, fmt.Errorf("user_id is required")
	}
	if domain.IsSystemUserID(req.UserID) {
		return CommandResult{}, fmt.Errorf("system user profile cannot be changed")
	}
	if s == nil || s.users == nil {
		return CommandResult{}, fmt.Errorf("admin user dependency is not configured")
	}
	req.FirstName = strings.TrimSpace(req.FirstName)
	req.LastName = strings.TrimSpace(req.LastName)
	return s.runCommand(ctx, req.CommandMeta, ActionSetProfile, req.UserID, domain.Peer{}, req, func() (CommandResult, error) {
		u, found, err := s.users.AdminUser(ctx, req.UserID)
		if err != nil {
			return CommandResult{}, err
		}
		if !found {
			return CommandResult{}, domain.ErrUserNotFound
		}
		details := map[string]any{
			"previous_first_name": u.FirstName,
			"previous_last_name":  u.LastName,
			"new_first_name":      req.FirstName,
			"new_last_name":       req.LastName,
			"would_change":        u.FirstName != req.FirstName || u.LastName != req.LastName,
		}
		if req.DryRun {
			return CommandResult{Message: "profile update validated", Details: details}, nil
		}
		updated, err := s.users.UpdateProfile(ctx, req.UserID, domain.UserProfileUpdate{
			FirstName: req.FirstName, HasFirstName: true,
			LastName: req.LastName, HasLastName: true,
		})
		if err != nil {
			return CommandResult{Details: details}, err
		}
		if err := s.notifyUserChanged(ctx, updated); err != nil {
			details["notify_error"] = err.Error()
		}
		return CommandResult{Message: "profile updated", Details: details}, nil
	})
}

// SetPhone force-sets a user's phone number. Rejects a collision with
// another account's phone (checked by the users service before writing,
// backed by the users_phone_unique_idx constraint as well).
func (s *Service) SetPhone(ctx context.Context, req SetPhoneRequest) (CommandResult, error) {
	if req.UserID <= 0 {
		return CommandResult{}, fmt.Errorf("user_id is required")
	}
	if s == nil || s.users == nil {
		return CommandResult{}, fmt.Errorf("admin user dependency is not configured")
	}
	req.Phone = domain.NormalizePhone(req.Phone)
	if !domain.ValidPhone(req.Phone) {
		return CommandResult{}, domain.ErrPhoneNumberInvalid
	}
	return s.runCommand(ctx, req.CommandMeta, ActionSetPhone, req.UserID, domain.Peer{}, req, func() (CommandResult, error) {
		u, found, err := s.users.AdminUser(ctx, req.UserID)
		if err != nil {
			return CommandResult{}, err
		}
		if !found {
			return CommandResult{}, domain.ErrUserNotFound
		}
		if u.Bot || domain.IsSystemUserID(u.ID) {
			return CommandResult{}, domain.ErrPhoneChangeForbidden
		}
		details := map[string]any{
			"previous_phone": u.Phone,
			"new_phone":      req.Phone,
			"would_change":   u.Phone != req.Phone,
		}
		if req.DryRun {
			return CommandResult{Message: "phone update validated", Details: details}, nil
		}
		updated, err := s.users.SetPhone(ctx, req.UserID, req.Phone)
		if err != nil {
			return CommandResult{Details: details}, err
		}
		details["changed"] = u.Phone != updated.Phone
		if err := s.notifyUserChanged(ctx, updated); err != nil {
			details["notify_error"] = err.Error()
		}
		return CommandResult{Message: "phone updated", Details: details}, nil
	})
}

// SetLoginEmail force-sets (or, if Email is empty, clears) a user's
// login/signup email. Rejects a collision with another account's login
// email (checked by the account service before writing, backed by the
// account_passwords_login_email_lower_unique_idx constraint as well).
func (s *Service) SetLoginEmail(ctx context.Context, req SetLoginEmailRequest) (CommandResult, error) {
	if req.UserID <= 0 {
		return CommandResult{}, fmt.Errorf("user_id is required")
	}
	if s == nil || s.users == nil || s.account == nil {
		return CommandResult{}, fmt.Errorf("admin account dependencies are not configured")
	}
	req.Email = strings.TrimSpace(req.Email)
	if req.Email != "" && !s.account.ValidLoginEmail(req.Email) {
		return CommandResult{}, domain.ErrEmailInvalid
	}
	return s.runCommand(ctx, req.CommandMeta, ActionSetLoginEmail, req.UserID, domain.Peer{}, req, func() (CommandResult, error) {
		u, found, err := s.users.AdminUser(ctx, req.UserID)
		if err != nil {
			return CommandResult{}, err
		}
		if !found {
			return CommandResult{}, domain.ErrUserNotFound
		}
		if u.Bot || domain.IsSystemUserID(u.ID) {
			return CommandResult{}, domain.ErrEmailInvalid
		}
		previous, _, err := s.account.LoginEmail(ctx, req.UserID)
		if err != nil {
			return CommandResult{}, err
		}
		details := map[string]any{
			"previous_login_email": previous,
			"new_login_email":      req.Email,
			"would_change":         !strings.EqualFold(previous, req.Email),
		}
		if req.DryRun {
			return CommandResult{Message: "login email update validated", Details: details}, nil
		}
		if req.Email == "" {
			err = s.account.ClearLoginEmail(ctx, req.UserID)
		} else {
			err = s.account.SetLoginEmail(ctx, req.UserID, req.Email)
		}
		if err != nil {
			return CommandResult{Details: details}, err
		}
		message := "login email updated"
		if req.Email == "" {
			message = "login email cleared"
		}
		return CommandResult{Message: message, Details: details}, nil
	})
}

// SetAccountAvatar force-sets a user's current profile photo from raw
// uploaded image bytes, reusing the same avatar rendition pipeline
// (s/a/c sizes) as photos.uploadProfilePhoto.
func (s *Service) SetAccountAvatar(ctx context.Context, req SetAccountAvatarRequest) (CommandResult, error) {
	if req.UserID <= 0 {
		return CommandResult{}, fmt.Errorf("user_id is required")
	}
	if domain.IsSystemUserID(req.UserID) {
		return CommandResult{}, fmt.Errorf("system user avatar cannot be changed")
	}
	if s == nil || s.users == nil || s.photos == nil {
		return CommandResult{}, fmt.Errorf("admin avatar dependencies are not configured")
	}
	if len(req.Data) == 0 || len(req.Data) > MaxAccountAvatarBytes || !s.photos.ValidateAvatarUpload(req.Data) {
		return CommandResult{}, domain.ErrPhotoInvalid
	}
	return s.runCommand(ctx, req.CommandMeta, ActionSetAccountAvatar, req.UserID, domain.Peer{}, req, func() (CommandResult, error) {
		u, found, err := s.users.AdminUser(ctx, req.UserID)
		if err != nil {
			return CommandResult{}, err
		}
		if !found {
			return CommandResult{}, domain.ErrUserNotFound
		}
		details := map[string]any{"file_name": req.FileName, "bytes": len(req.Data), "bot": u.Bot}
		if req.DryRun {
			return CommandResult{Message: "avatar update validated", Details: details}, nil
		}
		photo, err := s.photos.CreateAvatarFromBytes(ctx, req.Data, req.UserID)
		if err != nil {
			return CommandResult{Details: details}, err
		}
		if _, _, err := s.photos.SetCurrentProfilePhotoKind(ctx, domain.PeerTypeUser, req.UserID, domain.ProfilePhotoKindProfile, photo.ID, int(s.now().Unix())); err != nil {
			return CommandResult{Details: details}, err
		}
		details["photo_id"] = strconv.FormatInt(photo.ID, 10)
		if err := s.notifyUserChanged(ctx, u); err != nil {
			details["notify_error"] = err.Error()
		}
		return CommandResult{Message: "avatar updated", Details: details}, nil
	})
}

// SetChannelAvatar force-sets a channel's avatar from raw uploaded image
// bytes, reusing the same avatar rendition pipeline (s/a/c sizes) as
// SetAccountAvatar, but attaching the resulting photo directly to the
// channel row (photo_id) through the permission-check-free admin path
// instead of profile_photos history.
func (s *Service) SetChannelAvatar(ctx context.Context, req SetChannelAvatarRequest) (CommandResult, error) {
	if req.ChannelID <= 0 {
		return CommandResult{}, fmt.Errorf("channel_id is required")
	}
	if s == nil || s.channels == nil || s.photos == nil {
		return CommandResult{}, fmt.Errorf("admin channel avatar dependencies are not configured")
	}
	if len(req.Data) == 0 || len(req.Data) > MaxAccountAvatarBytes || !s.photos.ValidateAvatarUpload(req.Data) {
		return CommandResult{}, domain.ErrPhotoInvalid
	}
	target := domain.Peer{Type: domain.PeerTypeChannel, ID: req.ChannelID}
	return s.runCommand(ctx, req.CommandMeta, ActionSetChannelAvatar, 0, target, req, func() (CommandResult, error) {
		channel, err := s.channels.GetChannelByID(ctx, req.ChannelID)
		if err != nil {
			return CommandResult{}, err
		}
		if channel.Deleted || channel.Monoforum || (!channel.Broadcast && !channel.Megagroup) {
			return CommandResult{}, domain.ErrChannelInvalid
		}
		details := map[string]any{
			"file_name":         req.FileName,
			"bytes":             len(req.Data),
			"previous_photo_id": strconv.FormatInt(channel.PhotoID, 10),
		}
		if req.DryRun {
			return CommandResult{Message: "channel avatar update validated", Details: details}, nil
		}
		photo, err := s.photos.CreateAvatarFromBytes(ctx, req.Data, 0)
		if err != nil {
			return CommandResult{Details: details}, err
		}
		updated, err := s.channels.AdminSetPhoto(ctx, req.ChannelID, photo)
		if err != nil {
			return CommandResult{Details: details}, err
		}
		details["photo_id"] = strconv.FormatInt(photo.ID, 10)
		if err := s.notifyChannelChanged(ctx, updated); err != nil {
			details["notify_error"] = err.Error()
		}
		return CommandResult{Message: "avatar updated", Details: details}, nil
	})
}

// ChannelAvatar returns a channel's current avatar bytes and detected MIME
// type. Unlike a user's profile photo (tracked via profile_photos history),
// a channel's current photo is denormalized directly on the channel row as
// photo_id, so this resolves that id through GetPhoto instead of
// CurrentProfilePhotoKind.
func (s *Service) ChannelAvatar(ctx context.Context, channelID int64) ([]byte, string, bool, error) {
	if s == nil || s.photos == nil || s.channels == nil || channelID <= 0 {
		return nil, "", false, nil
	}
	channel, err := s.channels.GetChannelByID(ctx, channelID)
	if err != nil {
		return nil, "", false, err
	}
	if channel.PhotoID == 0 {
		return nil, "", false, nil
	}
	photo, found, err := s.photos.GetPhoto(ctx, channel.PhotoID)
	if err != nil || !found {
		return nil, "", found, err
	}
	return s.avatarBytes(ctx, photo)
}

// SetUserColor force-sets or clears a user's name/profile color.
func (s *Service) SetUserColor(ctx context.Context, req SetUserColorRequest) (CommandResult, error) {
	if req.UserID <= 0 {
		return CommandResult{}, fmt.Errorf("user_id is required")
	}
	if s == nil || s.users == nil {
		return CommandResult{}, fmt.Errorf("admin user dependency is not configured")
	}
	color := domain.PeerColor{HasColor: req.HasColor, Color: req.Color, BackgroundEmojiID: req.BackgroundEmojiID}
	return s.runCommand(ctx, req.CommandMeta, ActionSetUserColor, req.UserID, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{"for_profile": req.ForProfile, "has_color": req.HasColor, "color": req.Color}
		if req.DryRun {
			return CommandResult{Message: "dry-run completed", Details: details}, nil
		}
		updated, err := s.users.UpdateColor(ctx, req.UserID, req.ForProfile, color)
		if err != nil {
			return CommandResult{}, err
		}
		if err := s.notifyUserChanged(ctx, updated); err != nil {
			details["notify_error"] = err.Error()
		}
		return CommandResult{Message: "user color updated", Details: details}, nil
	})
}

// SetUserEmojiStatus force-sets or clears (document_id=0) a user's emoji status.
func (s *Service) SetUserEmojiStatus(ctx context.Context, req SetUserEmojiStatusRequest) (CommandResult, error) {
	if req.UserID <= 0 {
		return CommandResult{}, fmt.Errorf("user_id is required")
	}
	if s == nil || s.users == nil {
		return CommandResult{}, fmt.Errorf("admin user dependency is not configured")
	}
	status := domain.UserEmojiStatus{DocumentID: req.DocumentID, Until: req.Until}
	return s.runCommand(ctx, req.CommandMeta, ActionSetUserEmojiStatus, req.UserID, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{"document_id": strconv.FormatInt(req.DocumentID, 10), "until": req.Until}
		if req.DryRun {
			return CommandResult{Message: "dry-run completed", Details: details}, nil
		}
		updated, err := s.users.UpdateEmojiStatus(ctx, req.UserID, status)
		if err != nil {
			return CommandResult{}, err
		}
		if err := s.notifyUserChanged(ctx, updated); err != nil {
			details["notify_error"] = err.Error()
		}
		return CommandResult{Message: "user emoji status updated", Details: details}, nil
	})
}

// CreateBot provisions a new bot account owned by ownerUserID. The dry-run stage
// only validates the display name and username; the confirm stage creates the
// users+bots rows and returns the freshly minted token in the result details so
// the operator can copy it once.
func (s *Service) CreateBot(ctx context.Context, req CreateBotRequest) (CommandResult, error) {
	if s == nil || s.bots == nil {
		return CommandResult{}, fmt.Errorf("admin bot dependency is not configured")
	}
	if req.OwnerUserID <= 0 {
		return CommandResult{}, fmt.Errorf("owner_user_id is required")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > domain.MaxBotNameLength {
		return CommandResult{}, domain.ErrBotNameInvalid
	}
	username := strings.TrimSpace(strings.TrimPrefix(req.Username, "@"))
	if !domain.ValidBotUsername(username) {
		return CommandResult{}, domain.ErrBotUsernameInvalid
	}
	req.Name = name
	req.Username = username
	return s.runCommand(ctx, req.CommandMeta, ActionCreateBot, req.OwnerUserID, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{
			"owner_user_id": req.OwnerUserID,
			"name":          name,
			"username":      username,
		}
		if req.DryRun {
			return CommandResult{Message: "bot creation validated", Details: details}, nil
		}
		bot, token, err := s.bots.CreateBot(ctx, req.OwnerUserID, name, username)
		if err != nil {
			return CommandResult{Details: details}, err
		}
		details["bot_user_id"] = bot.ID
		if err := s.notifyUserChanged(ctx, bot); err != nil {
			details["notify_error"] = err.Error()
		}
		return CommandResult{
			Message:          "bot created",
			Details:          details,
			transientDetails: map[string]any{"token": token},
		}, nil
	})
}

// DeleteBot permanently removes a user-created bot. The dry-run stage verifies
// the target is a non-system bot; the confirm stage tombstones the account and
// invalidates its token. System bots are rejected outright.
func (s *Service) DeleteBot(ctx context.Context, req DeleteBotRequest) (CommandResult, error) {
	if s == nil || s.bots == nil {
		return CommandResult{}, fmt.Errorf("admin bot dependency is not configured")
	}
	if req.BotUserID <= 0 {
		return CommandResult{}, fmt.Errorf("bot_user_id is required")
	}
	if domain.IsSystemUserID(req.BotUserID) {
		return CommandResult{}, fmt.Errorf("system bots cannot be deleted")
	}
	return s.runCommand(ctx, req.CommandMeta, ActionDeleteBot, req.BotUserID, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{"bot_user_id": req.BotUserID}
		if s.users != nil {
			u, found, err := s.users.AdminUser(ctx, req.BotUserID)
			if err != nil {
				return CommandResult{}, err
			}
			if !found || !u.Bot {
				return CommandResult{}, domain.ErrBotNotFound
			}
			details["username"] = u.Username
			details["name"] = u.FirstName
		}
		if req.DryRun {
			return CommandResult{Message: "bot deletion validated", Details: details}, nil
		}
		deleted, err := s.bots.DeleteBot(ctx, req.BotUserID)
		if err != nil {
			return CommandResult{Details: details}, err
		}
		details["deleted"] = true
		if err := s.notifyUserChanged(ctx, deleted); err != nil {
			details["notify_error"] = err.Error()
		}
		return CommandResult{Message: "bot deleted", Details: details}, nil
	})
}

// CreateBroadcast enqueues a system-broadcast (a message from
// domain.OfficialSystemUserID) to an already-resolved recipient list.
// Delivery happens out-of-band via the broadcast worker draining the durable
// recipient rows this creates -- the command completes as soon as the
// recipient snapshot is written, never waiting on however many sends that
// implies.
func (s *Service) CreateBroadcast(ctx context.Context, req CreateBroadcastRequest) (CommandResult, error) {
	if s == nil || s.broadcast == nil {
		return CommandResult{}, fmt.Errorf("admin broadcast dependency is not configured")
	}
	message := strings.TrimSpace(req.Message)
	if message == "" {
		return CommandResult{}, domain.ErrBroadcastMessageEmpty
	}
	targetMode := domain.BroadcastTargetMode(req.TargetMode)
	if targetMode != domain.BroadcastTargetAll && targetMode != domain.BroadcastTargetSelected {
		return CommandResult{}, domain.ErrBroadcastInvalid
	}
	if len(req.UserIDs) == 0 {
		return CommandResult{}, domain.ErrBroadcastNoRecipients
	}
	return s.runCommand(ctx, req.CommandMeta, ActionCreateBroadcast, 0, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{
			"target_mode":     string(targetMode),
			"recipient_count": len(req.UserIDs),
			"message_preview": truncateBroadcastPreview(message),
		}
		if req.DryRun {
			return CommandResult{Message: "broadcast validated", Details: details}, nil
		}
		created, err := s.broadcast.Create(ctx, message, targetMode, req.UserIDs, req.CommandMeta.Actor)
		if err != nil {
			return CommandResult{Details: details}, err
		}
		details["broadcast_id"] = created.ID
		details["total_count"] = created.TotalCount
		return CommandResult{Message: "broadcast created", Details: details}, nil
	})
}

func truncateBroadcastPreview(message string) string {
	const maxPreview = 120
	r := []rune(message)
	if len(r) <= maxPreview {
		return message
	}
	return string(r[:maxPreview]) + "…"
}

// ExportBotToken returns a non-system bot's current token (unrotated) via the
// audited runCommand wrapper. Like CreateBot's token, it travels only in
// transientDetails -- excluded from the stored/replayed command JSON so it
// never lands in audit storage. The admin console's own UI additionally never
// renders this token on screen; it copies the response straight to the
// clipboard.
func (s *Service) ExportBotToken(ctx context.Context, req ExportBotTokenRequest) (CommandResult, error) {
	if s == nil || s.bots == nil {
		return CommandResult{}, fmt.Errorf("admin bot dependency is not configured")
	}
	if req.BotUserID <= 0 {
		return CommandResult{}, fmt.Errorf("bot_user_id is required")
	}
	if domain.IsSystemUserID(req.BotUserID) {
		return CommandResult{}, fmt.Errorf("system bots have no exportable token")
	}
	return s.runCommand(ctx, req.CommandMeta, ActionExportBotToken, req.BotUserID, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{"bot_user_id": req.BotUserID}
		if req.DryRun {
			return CommandResult{Message: "token export validated", Details: details}, nil
		}
		token, err := s.bots.AdminExportBotToken(ctx, req.BotUserID)
		if err != nil {
			return CommandResult{Details: details}, err
		}
		return CommandResult{
			Message:          "token exported",
			Details:          details,
			transientDetails: map[string]any{"token": token},
		}, nil
	})
}

// MintCollectibleUsername creates a collectible username asset and optionally
// assigns it in the same command. Shape validation runs before the command is
// journalled; occupancy is checked inside it, so a dry-run reports a taken name
// without minting and a replay of a completed command stays idempotent.
func (s *Service) MintCollectibleUsername(ctx context.Context, req MintCollectibleUsernameRequest) (CommandResult, error) {
	if s == nil || s.usernames == nil {
		return CommandResult{}, fmt.Errorf("admin collectible username dependency is not configured")
	}
	req.Username = domain.NormalizeUsername(req.Username)
	if !domain.ValidCollectibleUsername(req.Username) {
		return CommandResult{}, codedError(CodeUsernameInvalid, domain.ErrUsernameInvalid)
	}
	owner, err := collectibleOwnerPeer(req.OwnerUserID, req.OwnerChannelID)
	if err != nil {
		return CommandResult{}, err
	}
	req.Currency = strings.ToUpper(strings.TrimSpace(req.Currency))
	req.CryptoCurrency = strings.ToUpper(strings.TrimSpace(req.CryptoCurrency))
	if err := domain.ValidateCollectibleAmounts(req.Currency, req.Amount, req.CryptoCurrency, req.CryptoAmount); err != nil {
		return CommandResult{}, codedError(CodeCollectibleCurrencyInvalid, err)
	}
	req.URL = strings.TrimSpace(req.URL)
	if len(req.URL) > domain.MaxCollectibleUsernameURLLength {
		return CommandResult{}, fmt.Errorf("url must be <= %d bytes", domain.MaxCollectibleUsernameURLLength)
	}
	purchase, err := collectiblePurchaseDate(req.PurchaseDate, s.now)
	if err != nil {
		return CommandResult{}, err
	}
	req.PurchaseDate = purchase.Unix()
	return s.runCommand(ctx, req.CommandMeta, ActionMintCollectibleUsername, req.OwnerUserID, owner, req, func() (CommandResult, error) {
		details := map[string]any{
			"username":        req.Username,
			"owner_type":      string(owner.Type),
			"owner_id":        strconv.FormatInt(owner.ID, 10),
			"currency":        req.Currency,
			"amount":          strconv.FormatInt(req.Amount, 10),
			"crypto_currency": req.CryptoCurrency,
			"crypto_amount":   strconv.FormatInt(req.CryptoAmount, 10),
			"purchase_date":   purchase.Format(time.RFC3339),
			"url":             req.URL,
		}
		existing, err := s.usernames.Collectible(ctx, req.Username)
		switch {
		case err == nil:
			details["existing_collectible_id"] = strconv.FormatInt(existing.ID, 10)
			details["existing_status"] = string(existing.Status)
			return CommandResult{Details: details}, codedError(CodeUsernameOccupied, domain.ErrUsernameOccupied)
		case !errors.Is(err, domain.ErrCollectibleUsernameNotFound):
			return CommandResult{Details: details}, collectibleUsernameError(err)
		}
		if req.DryRun {
			return CommandResult{Message: "collectible username mint validated", Details: details}, nil
		}
		asset, created, err := s.usernames.Mint(ctx, domain.MintCollectibleUsernameRequest{
			Username:       req.Username,
			Owner:          owner,
			PurchaseDate:   purchase,
			Currency:       req.Currency,
			Amount:         req.Amount,
			CryptoCurrency: req.CryptoCurrency,
			CryptoAmount:   req.CryptoAmount,
			URL:            req.URL,
			Actor:          req.Actor,
			Reason:         req.Reason,
			CommandKey:     "admin-collectible-mint:" + req.CommandID,
		})
		if err != nil {
			return CommandResult{Details: details}, collectibleUsernameError(err)
		}
		details["collectible_id"] = strconv.FormatInt(asset.ID, 10)
		details["status"] = string(asset.Status)
		details["url"] = asset.URL
		details["created"] = created
		message := "collectible username minted"
		if !created {
			message = "collectible username mint replayed"
		}
		return CommandResult{Message: message, Details: details}, nil
	})
}

// TransferCollectibleUsername moves an asset to a new holder. The asset must
// exist and must not be burned; the store keeps the move atomic with the
// receiving peer's username registry row.
func (s *Service) TransferCollectibleUsername(ctx context.Context, req TransferCollectibleUsernameRequest) (CommandResult, error) {
	if s == nil || s.usernames == nil {
		return CommandResult{}, fmt.Errorf("admin collectible username dependency is not configured")
	}
	req.Username = domain.NormalizeUsername(req.Username)
	if !domain.ValidCollectibleUsername(req.Username) {
		return CommandResult{}, codedError(CodeUsernameInvalid, domain.ErrUsernameInvalid)
	}
	to, err := collectibleOwnerPeer(req.ToUserID, req.ToChannelID)
	if err != nil {
		return CommandResult{}, err
	}
	if to.Type == "" {
		return CommandResult{}, fmt.Errorf("exactly one of to_user_id or to_channel_id is required")
	}
	return s.runCommand(ctx, req.CommandMeta, ActionTransferCollectibleUsername, req.ToUserID, to, req, func() (CommandResult, error) {
		details := map[string]any{
			"username": req.Username,
			"to_type":  string(to.Type),
			"to_id":    strconv.FormatInt(to.ID, 10),
		}
		asset, err := s.usernames.Collectible(ctx, req.Username)
		if err != nil {
			return CommandResult{Details: details}, collectibleUsernameError(err)
		}
		details["collectible_id"] = strconv.FormatInt(asset.ID, 10)
		details["previous_status"] = string(asset.Status)
		details["previous_owner_type"] = string(asset.Owner.Type)
		details["previous_owner_id"] = strconv.FormatInt(asset.Owner.ID, 10)
		details["transfer_count"] = asset.TransferCount
		if asset.Status == domain.CollectibleUsernameStatusBurned {
			return CommandResult{Details: details}, codedError(CodeCollectibleBurned, domain.ErrCollectibleUsernameBurned)
		}
		details["would_change"] = !asset.Owned() || asset.Owner != to
		if req.DryRun {
			return CommandResult{Message: "collectible username transfer validated", Details: details}, nil
		}
		updated, changed, err := s.usernames.Transfer(ctx, domain.TransferCollectibleUsernameRequest{
			Username:   req.Username,
			To:         to,
			Actor:      req.Actor,
			Reason:     req.Reason,
			CommandKey: "admin-collectible-transfer:" + req.CommandID,
		})
		if err != nil {
			return CommandResult{Details: details}, collectibleUsernameError(err)
		}
		details["status"] = string(updated.Status)
		details["owner_type"] = string(updated.Owner.Type)
		details["owner_id"] = strconv.FormatInt(updated.Owner.ID, 10)
		details["changed"] = changed
		message := "collectible username transferred"
		if !changed {
			message = "collectible username transfer was a no-op"
		}
		return CommandResult{Message: message, Details: details}, nil
	})
}

// RevokeCollectibleUsername returns an asset to the operator vault, or burns it
// permanently when Burn is set. Revoking an asset nobody holds is rejected:
// there is nothing to take back, and a silent no-op would read as success.
func (s *Service) RevokeCollectibleUsername(ctx context.Context, req RevokeCollectibleUsernameRequest) (CommandResult, error) {
	if s == nil || s.usernames == nil {
		return CommandResult{}, fmt.Errorf("admin collectible username dependency is not configured")
	}
	req.Username = domain.NormalizeUsername(req.Username)
	if !domain.ValidCollectibleUsername(req.Username) {
		return CommandResult{}, codedError(CodeUsernameInvalid, domain.ErrUsernameInvalid)
	}
	if req.ExpectedOwnerUserID < 0 {
		return CommandResult{}, fmt.Errorf("expected_owner_user_id must not be negative")
	}
	return s.runCommand(ctx, req.CommandMeta, ActionRevokeCollectibleUsername, 0, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{"username": req.Username, "burn": req.Burn}
		asset, err := s.usernames.Collectible(ctx, req.Username)
		if err != nil {
			return CommandResult{Details: details}, collectibleUsernameError(err)
		}
		details["collectible_id"] = strconv.FormatInt(asset.ID, 10)
		details["previous_status"] = string(asset.Status)
		details["previous_owner_type"] = string(asset.Owner.Type)
		details["previous_owner_id"] = strconv.FormatInt(asset.Owner.ID, 10)
		if asset.Status == domain.CollectibleUsernameStatusBurned {
			return CommandResult{Details: details}, codedError(CodeCollectibleBurned, domain.ErrCollectibleUsernameBurned)
		}
		if !req.Burn && !asset.Owned() {
			return CommandResult{Details: details}, codedError(CodeCollectibleNotOwned, domain.ErrCollectibleUsernameNotOwned)
		}
		if req.ExpectedOwnerUserID > 0 && asset.Owner != (domain.Peer{Type: domain.PeerTypeUser, ID: req.ExpectedOwnerUserID}) {
			return CommandResult{Details: details}, codedError(CodeCollectibleNotOwned, domain.ErrCollectibleUsernameNotOwned)
		}
		if req.DryRun {
			return CommandResult{Message: "collectible username revoke validated", Details: details}, nil
		}
		updated, changed, err := s.usernames.Revoke(ctx, domain.RevokeCollectibleUsernameRequest{
			Username:   req.Username,
			Burn:       req.Burn,
			Actor:      req.Actor,
			Reason:     req.Reason,
			CommandKey: "admin-collectible-revoke:" + req.CommandID,
		})
		if err != nil {
			return CommandResult{Details: details}, collectibleUsernameError(err)
		}
		details["status"] = string(updated.Status)
		details["changed"] = changed
		message := "collectible username returned to vault"
		if req.Burn {
			message = "collectible username burned"
		}
		return CommandResult{Message: message, Details: details}, nil
	})
}

// DeleteCollectibleUsername erases the asset and its provenance, releasing the
// name. Because the history disappears with the record, the command journal is
// the only remaining trace: the details below are captured before the delete so
// the entry still says what was removed and from whom.
func (s *Service) DeleteCollectibleUsername(ctx context.Context, req DeleteCollectibleUsernameRequest) (CommandResult, error) {
	if s == nil || s.usernames == nil {
		return CommandResult{}, fmt.Errorf("admin collectible username dependency is not configured")
	}
	req.Username = domain.NormalizeUsername(req.Username)
	if !domain.ValidCollectibleUsername(req.Username) {
		return CommandResult{}, codedError(CodeUsernameInvalid, domain.ErrUsernameInvalid)
	}
	return s.runCommand(ctx, req.CommandMeta, ActionDeleteCollectibleUsername, 0, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{"username": req.Username}
		asset, err := s.usernames.Collectible(ctx, req.Username)
		if err != nil {
			return CommandResult{Details: details}, collectibleUsernameError(err)
		}
		details["collectible_id"] = strconv.FormatInt(asset.ID, 10)
		details["previous_status"] = string(asset.Status)
		details["previous_owner_type"] = string(asset.Owner.Type)
		details["previous_owner_id"] = strconv.FormatInt(asset.Owner.ID, 10)
		details["transfer_count"] = asset.TransferCount
		details["currency"] = asset.Currency
		details["amount"] = strconv.FormatInt(asset.Amount, 10)
		if asset.Status == domain.CollectibleUsernameStatusBurned {
			// Only live assets can be deleted; burned rows are history and are
			// released by re-issuing the name instead.
			return CommandResult{Details: details}, codedError(CodeCollectibleBurned, domain.ErrCollectibleUsernameBurned)
		}
		if req.DryRun {
			return CommandResult{Message: "collectible username delete validated", Details: details}, nil
		}
		deleted, err := s.usernames.Delete(ctx, domain.DeleteCollectibleUsernameRequest{
			Username:   req.Username,
			Actor:      req.Actor,
			Reason:     req.Reason,
			CommandKey: "admin-collectible-delete:" + req.CommandID,
		})
		if err != nil {
			return CommandResult{Details: details}, collectibleUsernameError(err)
		}
		details["deleted"] = deleted
		if !deleted {
			return CommandResult{Message: "collectible username already absent", Details: details}, nil
		}
		return CommandResult{Message: "collectible username deleted", Details: details}, nil
	})
}

func collectibleOwnerPeer(userID, channelID int64) (domain.Peer, error) {
	if userID < 0 || channelID < 0 {
		return domain.Peer{}, fmt.Errorf("owner id must be positive")
	}
	if userID > 0 && channelID > 0 {
		return domain.Peer{}, fmt.Errorf("at most one of user id or channel id is allowed")
	}
	switch {
	case userID > 0:
		return domain.Peer{Type: domain.PeerTypeUser, ID: userID}, nil
	case channelID > 0:
		return domain.Peer{Type: domain.PeerTypeChannel, ID: channelID}, nil
	default:
		return domain.Peer{}, nil
	}
}

// collectiblePurchaseDate resolves the optional Unix purchase timestamp. Zero
// means "now", so a mint always records a complete, reproducible provenance
// entry; the int32 bound matches the TL date field clients render.
func collectiblePurchaseDate(unix int64, now func() time.Time) (time.Time, error) {
	if unix == 0 {
		return now().UTC(), nil
	}
	if unix < 0 || unix > math.MaxInt32 {
		return time.Time{}, fmt.Errorf("purchase_date must be a non-negative int32 Unix timestamp")
	}
	return time.Unix(unix, 0).UTC(), nil
}

func CollectibleUsernameErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, domain.ErrUsernameOccupied):
		return CodeUsernameOccupied
	case errors.Is(err, domain.ErrCollectibleUsernameNotFound), errors.Is(err, domain.ErrUsernameNotOccupied):
		return CodeCollectibleNotFound
	case errors.Is(err, domain.ErrCollectibleUsernameBurned):
		return CodeCollectibleBurned
	case errors.Is(err, domain.ErrCollectibleUsernameLimit):
		return CodeCollectiblePeerLimit
	case errors.Is(err, domain.ErrCollectibleUsernameNotOwned):
		return CodeCollectibleNotOwned
	case errors.Is(err, domain.ErrCollectibleCurrencyInvalid):
		return CodeCollectibleCurrencyInvalid
	case errors.Is(err, domain.ErrUsernameNotCollectible), errors.Is(err, domain.ErrUsernameNotEditable):
		return CodeUsernameNotCollectible
	case errors.Is(err, domain.ErrUsernameInvalid):
		return CodeUsernameInvalid
	case errors.Is(err, domain.ErrCollectibleUsernameStateInvalid), errors.Is(err, domain.ErrUsernameOrderInvalid):
		return CodeCollectibleStateInvalid
	default:
		return ""
	}
}

func collectibleUsernameError(err error) error {
	if code := CollectibleUsernameErrorCode(err); code != "" {
		return codedError(code, err)
	}
	return err
}

func codedError(code string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", code, err)
}

func (s *Service) SetChannelVerified(ctx context.Context, req SetChannelVerifiedRequest) (CommandResult, error) {
	if req.ChannelID <= 0 {
		return CommandResult{}, fmt.Errorf("channel_id is required")
	}
	if s == nil || s.channels == nil {
		return CommandResult{}, fmt.Errorf("admin channel dependency is not configured")
	}
	target := domain.Peer{Type: domain.PeerTypeChannel, ID: req.ChannelID}
	return s.runCommand(ctx, req.CommandMeta, ActionSetChannelVerified, 0, target, req, func() (CommandResult, error) {
		ch, err := s.channels.GetChannelByID(ctx, req.ChannelID)
		if err != nil {
			return CommandResult{}, err
		}
		if ch.Monoforum || (!ch.Broadcast && !ch.Megagroup) {
			return CommandResult{}, domain.ErrChannelInvalid
		}
		details := map[string]any{
			"title":             ch.Title,
			"username":          ch.Username,
			"broadcast":         ch.Broadcast,
			"megagroup":         ch.Megagroup,
			"previous_verified": ch.Verified,
			"new_verified":      req.Verified,
			"would_change":      ch.Verified != req.Verified,
		}
		if req.DryRun {
			return CommandResult{Message: "dry-run completed", Details: details}, nil
		}
		updated, err := s.channels.SetVerified(ctx, req.ChannelID, req.Verified)
		if err != nil {
			return CommandResult{}, err
		}
		details["updated_verified"] = updated.Verified
		if err := s.notifyChannelChanged(ctx, updated); err != nil {
			details["notify_error"] = err.Error()
		}
		return CommandResult{Message: "channel verified updated", Details: details}, nil
	})
}

// SetChannelFlags sets or clears the scam/fake moderation flags on a channel or
// supergroup. Both flags are applied together from the desired state.
func (s *Service) SetChannelFlags(ctx context.Context, req SetChannelFlagsRequest) (CommandResult, error) {
	if req.ChannelID <= 0 {
		return CommandResult{}, fmt.Errorf("channel_id is required")
	}
	if s == nil || s.channels == nil {
		return CommandResult{}, fmt.Errorf("admin channel dependency is not configured")
	}
	if req.Scam && req.Fake {
		return CommandResult{}, domain.ErrPeerModerationFlagsInvalid
	}
	target := domain.Peer{Type: domain.PeerTypeChannel, ID: req.ChannelID}
	return s.runCommand(ctx, req.CommandMeta, ActionSetChannelFlags, 0, target, req, func() (CommandResult, error) {
		ch, err := s.channels.GetChannelByID(ctx, req.ChannelID)
		if err != nil {
			return CommandResult{}, err
		}
		if ch.Monoforum || (!ch.Broadcast && !ch.Megagroup) {
			return CommandResult{}, domain.ErrChannelInvalid
		}
		details := map[string]any{
			"title": ch.Title, "username": ch.Username,
			"previous_scam": ch.Scam, "previous_fake": ch.Fake,
			"new_scam": req.Scam, "new_fake": req.Fake,
			"would_change": ch.Scam != req.Scam || ch.Fake != req.Fake,
		}
		if req.DryRun {
			return CommandResult{Message: "dry-run completed", Details: details}, nil
		}
		updated, err := s.channels.SetScamFake(ctx, req.ChannelID, req.Scam, req.Fake)
		if err != nil {
			return CommandResult{}, err
		}
		details["updated_scam"] = updated.Scam
		details["updated_fake"] = updated.Fake
		if err := s.notifyChannelChanged(ctx, updated); err != nil {
			details["notify_error"] = err.Error()
		}
		return CommandResult{Message: "channel flags updated", Details: details}, nil
	})
}

// SetChannelSettings applies an admin moderation-settings patch to a channel/supergroup.
func (s *Service) SetChannelSettings(ctx context.Context, req SetChannelSettingsRequest) (CommandResult, error) {
	if req.ChannelID <= 0 {
		return CommandResult{}, fmt.Errorf("channel_id is required")
	}
	if s == nil || s.channels == nil {
		return CommandResult{}, fmt.Errorf("admin channel dependency is not configured")
	}
	if req.SlowmodeSeconds != nil && (*req.SlowmodeSeconds < 0 || *req.SlowmodeSeconds > 86400) {
		return CommandResult{}, fmt.Errorf("slowmode_seconds must be between 0 and 86400")
	}
	patch := domain.ChannelAdminSettings{
		Gigagroup: req.Gigagroup, AntiSpam: req.AntiSpam, ParticipantsHidden: req.ParticipantsHidden,
		NoForwards: req.NoForwards, JoinToSend: req.JoinToSend, JoinRequest: req.JoinRequest, SlowmodeSeconds: req.SlowmodeSeconds,
	}
	target := domain.Peer{Type: domain.PeerTypeChannel, ID: req.ChannelID}
	return s.runCommand(ctx, req.CommandMeta, ActionSetChannelSettings, 0, target, req, func() (CommandResult, error) {
		if patch.Empty() {
			return CommandResult{}, fmt.Errorf("no settings provided")
		}
		details := boolIntPatchDetails(patch)
		if req.DryRun {
			return CommandResult{Message: "dry-run completed", Details: details}, nil
		}
		updated, err := s.channels.AdminSetSettings(ctx, req.ChannelID, patch)
		if err != nil {
			return CommandResult{}, err
		}
		details["updated"] = true
		if err := s.notifyChannelChanged(ctx, updated); err != nil {
			details["notify_error"] = err.Error()
		}
		return CommandResult{Message: "channel settings updated", Details: details}, nil
	})
}

// SetChannelUsername force-sets or clears a channel username.
func (s *Service) SetChannelUsername(ctx context.Context, req SetChannelUsernameRequest) (CommandResult, error) {
	if req.ChannelID <= 0 {
		return CommandResult{}, fmt.Errorf("channel_id is required")
	}
	if s == nil || s.channels == nil {
		return CommandResult{}, fmt.Errorf("admin channel dependency is not configured")
	}
	username := strings.TrimSpace(strings.TrimPrefix(req.Username, "@"))
	req.Username = username
	target := domain.Peer{Type: domain.PeerTypeChannel, ID: req.ChannelID}
	return s.runCommand(ctx, req.CommandMeta, ActionSetChannelUsername, 0, target, req, func() (CommandResult, error) {
		details := map[string]any{"new_username": username}
		if req.DryRun {
			return CommandResult{Message: "dry-run completed", Details: details}, nil
		}
		updated, err := s.channels.AdminSetUsername(ctx, req.ChannelID, username)
		if err != nil {
			return CommandResult{}, err
		}
		details["updated_username"] = updated.Username
		if err := s.notifyChannelChanged(ctx, updated); err != nil {
			details["notify_error"] = err.Error()
		}
		return CommandResult{Message: "channel username updated", Details: details}, nil
	})
}

// SetChannelColor force-sets or clears a channel name/profile color.
func (s *Service) SetChannelColor(ctx context.Context, req SetChannelColorRequest) (CommandResult, error) {
	if req.ChannelID <= 0 {
		return CommandResult{}, fmt.Errorf("channel_id is required")
	}
	if s == nil || s.channels == nil {
		return CommandResult{}, fmt.Errorf("admin channel dependency is not configured")
	}
	color := domain.ChannelPeerColor{HasColor: req.HasColor, Color: req.Color, BackgroundEmojiID: req.BackgroundEmojiID}
	target := domain.Peer{Type: domain.PeerTypeChannel, ID: req.ChannelID}
	return s.runCommand(ctx, req.CommandMeta, ActionSetChannelColor, 0, target, req, func() (CommandResult, error) {
		details := map[string]any{"for_profile": req.ForProfile, "has_color": req.HasColor, "color": req.Color}
		if req.DryRun {
			return CommandResult{Message: "dry-run completed", Details: details}, nil
		}
		updated, err := s.channels.AdminSetColor(ctx, req.ChannelID, req.ForProfile, color)
		if err != nil {
			return CommandResult{}, err
		}
		if err := s.notifyChannelChanged(ctx, updated); err != nil {
			details["notify_error"] = err.Error()
		}
		return CommandResult{Message: "channel color updated", Details: details}, nil
	})
}

// SetChannelEmojiStatus force-sets or clears (document_id=0) a channel emoji status.
func (s *Service) SetChannelEmojiStatus(ctx context.Context, req SetChannelEmojiStatusRequest) (CommandResult, error) {
	if req.ChannelID <= 0 {
		return CommandResult{}, fmt.Errorf("channel_id is required")
	}
	if s == nil || s.channels == nil {
		return CommandResult{}, fmt.Errorf("admin channel dependency is not configured")
	}
	status := domain.ChannelEmojiStatus{DocumentID: req.DocumentID, Until: req.Until}
	target := domain.Peer{Type: domain.PeerTypeChannel, ID: req.ChannelID}
	return s.runCommand(ctx, req.CommandMeta, ActionSetChannelEmojiStatus, 0, target, req, func() (CommandResult, error) {
		details := map[string]any{"document_id": strconv.FormatInt(req.DocumentID, 10), "until": req.Until}
		if req.DryRun {
			return CommandResult{Message: "dry-run completed", Details: details}, nil
		}
		updated, err := s.channels.AdminSetEmojiStatus(ctx, req.ChannelID, status)
		if err != nil {
			return CommandResult{}, err
		}
		if err := s.notifyChannelChanged(ctx, updated); err != nil {
			details["notify_error"] = err.Error()
		}
		return CommandResult{Message: "channel emoji status updated", Details: details}, nil
	})
}

func boolIntPatchDetails(p domain.ChannelAdminSettings) map[string]any {
	details := map[string]any{}
	if p.Gigagroup != nil {
		details["gigagroup"] = *p.Gigagroup
	}
	if p.AntiSpam != nil {
		details["antispam"] = *p.AntiSpam
	}
	if p.ParticipantsHidden != nil {
		details["participants_hidden"] = *p.ParticipantsHidden
	}
	if p.NoForwards != nil {
		details["noforwards"] = *p.NoForwards
	}
	if p.JoinToSend != nil {
		details["join_to_send"] = *p.JoinToSend
	}
	if p.JoinRequest != nil {
		details["join_request"] = *p.JoinRequest
	}
	if p.SlowmodeSeconds != nil {
		details["slowmode_seconds"] = *p.SlowmodeSeconds
	}
	return details
}

func (s *Service) RevokeSessions(ctx context.Context, req RevokeSessionsRequest) (CommandResult, error) {
	if req.UserID <= 0 {
		return CommandResult{}, fmt.Errorf("user_id is required")
	}
	if s == nil || s.auth == nil || s.revoker == nil {
		return CommandResult{}, fmt.Errorf("admin auth dependencies are not configured")
	}
	modeCount := 0
	if req.Hash != 0 {
		modeCount++
	}
	if req.KeepHash != 0 {
		modeCount++
	}
	if req.RevokeAll {
		modeCount++
	}
	if modeCount != 1 {
		return CommandResult{}, fmt.Errorf("choose one revoke mode")
	}
	return s.runCommand(ctx, req.CommandMeta, ActionRevokeSessions, req.UserID, domain.Peer{}, req, func() (CommandResult, error) {
		items, err := s.auth.ListAuthorizations(ctx, req.UserID)
		if err != nil {
			return CommandResult{}, err
		}
		targets, keep, err := revokeTargets(items, req)
		if err != nil {
			return CommandResult{}, err
		}
		details := map[string]any{
			"target_hashes": authorizationHashes(targets),
			"target_count":  len(targets),
			"keep_hash":     authorizationHashString(keep.Hash),
		}
		if req.DryRun {
			return CommandResult{Message: "dry-run completed", Details: details}, nil
		}
		var revoked []domain.Authorization
		if req.Hash != 0 {
			deleted, found, err := s.auth.ResetAuthorization(ctx, req.UserID, req.Hash)
			if err != nil {
				return CommandResult{}, err
			}
			if !found {
				return CommandResult{}, fmt.Errorf("authorization hash not found")
			}
			revoked = append(revoked, deleted)
		} else {
			deleted, err := s.auth.ResetAuthorizations(ctx, req.UserID, keep.AuthKeyID)
			if err != nil {
				return CommandResult{}, err
			}
			revoked = append(revoked, deleted...)
		}
		for _, a := range revoked {
			if err := s.revoker.RevokeAuthorizationAuthKey(ctx, a.AuthKeyID, req.UserID); err != nil {
				return CommandResult{}, err
			}
		}
		details["revoked_hashes"] = authorizationHashes(revoked)
		details["revoked_count"] = len(revoked)
		return CommandResult{Message: "sessions revoked", Details: details}, nil
	})
}

func (s *Service) DeletePrivateMessages(ctx context.Context, req DeletePrivateMessagesRequest) (CommandResult, error) {
	ids, err := normalizeIDs(req.IDs)
	if err != nil {
		return CommandResult{}, err
	}
	req.IDs = ids
	if req.OwnerUserID <= 0 || req.Peer.Type != domain.PeerTypeUser || req.Peer.ID <= 0 {
		return CommandResult{}, fmt.Errorf("owner_user_id and user peer are required")
	}
	if s == nil || s.messages == nil {
		return CommandResult{}, fmt.Errorf("admin message dependency is not configured")
	}
	return s.runCommand(ctx, req.CommandMeta, ActionDeletePrivateMessages, req.OwnerUserID, req.Peer, req, func() (CommandResult, error) {
		list, err := s.messages.GetMessages(ctx, req.OwnerUserID, req.IDs)
		if err != nil {
			return CommandResult{}, err
		}
		found, missing, err := validatePrivateMessageSelection(req.OwnerUserID, req.Peer, req.IDs, list.Messages)
		if err != nil {
			return CommandResult{}, err
		}
		details := map[string]any{
			"requested_ids": req.IDs,
			"found_ids":     found,
			"missing_ids":   missing,
			"revoke":        req.Revoke,
		}
		if req.DryRun {
			return CommandResult{Message: "dry-run completed", Details: details}, nil
		}
		if len(missing) > 0 {
			return CommandResult{}, fmt.Errorf("messages not found for owner/peer: %v", missing)
		}
		res, err := s.messages.DeleteMessages(ctx, req.OwnerUserID, domain.DeleteMessagesRequest{
			OwnerUserID: req.OwnerUserID,
			IDs:         req.IDs,
			Revoke:      req.Revoke,
			Date:        int(s.now().Unix()),
		})
		if err != nil {
			return CommandResult{}, err
		}
		details["deleted"] = summarizeDeleteResult(res)
		details["changed"] = res.Changed()
		return CommandResult{Message: "messages deleted", Details: details}, nil
	})
}

func (s *Service) DeletePrivateHistory(ctx context.Context, req DeletePrivateHistoryRequest) (CommandResult, error) {
	if req.OwnerUserID <= 0 || req.Peer.Type != domain.PeerTypeUser || req.Peer.ID <= 0 {
		return CommandResult{}, fmt.Errorf("owner_user_id and user peer are required")
	}
	if req.MaxID < 0 || req.MaxID > domain.MaxMessageBoxID || req.MinDate < 0 || req.MaxDate < 0 {
		return CommandResult{}, domain.ErrMessageIDInvalid
	}
	if req.MaxBatches <= 0 {
		req.MaxBatches = 10
	}
	if req.MaxBatches > maxHistoryBatches {
		return CommandResult{}, fmt.Errorf("max_batches exceeds %d", maxHistoryBatches)
	}
	if s == nil || s.messages == nil {
		return CommandResult{}, fmt.Errorf("admin message dependency is not configured")
	}
	return s.runCommand(ctx, req.CommandMeta, ActionDeletePrivateHistory, req.OwnerUserID, req.Peer, req, func() (CommandResult, error) {
		preview, err := s.messages.GetHistory(ctx, req.OwnerUserID, domain.MessageFilter{
			HasPeer: true,
			Peer:    req.Peer,
			MaxID:   req.MaxID,
			Limit:   50,
		})
		if err != nil {
			return CommandResult{}, err
		}
		details := map[string]any{
			"preview_ids":       messageIDs(preview.Messages),
			"preview_count":     len(preview.Messages),
			"batch_limit":       domain.MaxDeleteHistoryBatch,
			"max_batches":       req.MaxBatches,
			"revoke":            req.Revoke,
			"just_clear":        req.JustClear,
			"date_range_filter": req.MinDate != 0 || req.MaxDate != 0,
		}
		if req.DryRun {
			return CommandResult{Message: "dry-run completed", Details: details}, nil
		}
		totalDeleted := 0
		ownerBatches := make([]any, 0, req.MaxBatches)
		offset := 0
		for batch := 0; batch < req.MaxBatches; batch++ {
			res, err := s.messages.DeleteHistory(ctx, req.OwnerUserID, domain.DeleteHistoryRequest{
				OwnerUserID: req.OwnerUserID,
				Peer:        req.Peer,
				MaxID:       req.MaxID,
				MinDate:     req.MinDate,
				MaxDate:     req.MaxDate,
				JustClear:   req.JustClear,
				Revoke:      req.Revoke,
				Date:        int(s.now().Unix()),
			})
			if err != nil {
				return CommandResult{}, err
			}
			self := res.Self()
			totalDeleted += len(self.MessageIDs)
			ownerBatches = append(ownerBatches, summarizeDeleteResult(res)...)
			offset = res.Offset
			if res.Offset == 0 {
				break
			}
		}
		details["deleted_count"] = totalDeleted
		details["deleted"] = ownerBatches
		details["has_more"] = offset != 0
		msg := "history deleted"
		if offset != 0 {
			msg = "history partially deleted; run another command to continue"
		}
		return CommandResult{Message: msg, Details: details}, nil
	})
}

// MaxAccountAvatarBytes bounds both reading (AccountAvatar) and writing
// (SetAccountAvatar) a user's profile photo through the admin console.
const MaxAccountAvatarBytes = 4 << 20

// AccountAvatar returns an account's current profile photo bytes and detected
// MIME type, mirroring internal/web's public avatar serving (same size
// selection and safe-image-type checks) so the admin console shows exactly
// what a public preview card would show for the same account.
func (s *Service) AccountAvatar(ctx context.Context, userID int64) ([]byte, string, bool, error) {
	if s == nil || s.photos == nil || userID <= 0 {
		return nil, "", false, nil
	}
	photo, found, err := s.photos.CurrentProfilePhotoKind(ctx, domain.PeerTypeUser, userID, domain.ProfilePhotoKindProfile)
	if err != nil || !found {
		return nil, "", found, err
	}
	return s.avatarBytes(ctx, photo)
}

func (s *Service) avatarBytes(ctx context.Context, photo domain.Photo) ([]byte, string, bool, error) {
	size, inline, ok := bestAccountPhotoSize(photo.Sizes)
	if !ok {
		return nil, "", false, nil
	}
	data := inline
	if len(data) == 0 {
		chunk, found, err := s.photos.GetFile(ctx, domain.FileDownloadRequest{
			LocationKey: fmt.Sprintf("photo:%d:%s", photo.ID, size.Type),
			Limit:       MaxAccountAvatarBytes + 1,
		})
		if err != nil || !found {
			return nil, "", found, err
		}
		if chunk.Total <= 0 || chunk.Total > MaxAccountAvatarBytes || int64(len(chunk.Bytes)) != chunk.Total {
			return nil, "", false, nil
		}
		data = chunk.Bytes
	}
	if len(data) == 0 || len(data) > MaxAccountAvatarBytes {
		return nil, "", false, nil
	}
	mimeType := http.DetectContentType(data)
	if !safeAccountImageType(mimeType) {
		return nil, "", false, nil
	}
	return data, mimeType, true, nil
}

func bestAccountPhotoSize(sizes []domain.PhotoSize) (domain.PhotoSize, []byte, bool) {
	var (
		best      domain.PhotoSize
		bestBytes []byte
		bestScore int64 = -1
	)
	for _, size := range sizes {
		if !validAccountPhotoSizeType(size.Type) {
			continue
		}
		var inline []byte
		switch size.Kind {
		case domain.PhotoSizeKindCached:
			if len(size.Bytes) == 0 || len(size.Bytes) > MaxAccountAvatarBytes {
				continue
			}
			inline = size.Bytes
		case domain.PhotoSizeKindDefault, domain.PhotoSizeKindProgressive:
			// Downloadable static raster size.
		default:
			continue
		}
		score := int64(size.W) * int64(size.H)
		if score <= 0 {
			score = int64(size.Size)
		}
		if score > bestScore {
			best, bestBytes, bestScore = size, inline, score
		}
	}
	return best, bestBytes, bestScore >= 0
}

func validAccountPhotoSizeType(value string) bool {
	if value == "" || len(value) > 8 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func safeAccountImageType(value string) bool {
	switch value {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func dedupeCollectibleAttributeName(seen map[string]int, name string) string {
	key := strings.ToLower(name)
	seen[key]++
	if seen[key] == 1 {
		return name
	}
	return fmt.Sprintf("%s (%d)", name, seen[key])
}

func (s *Service) SetStickerSetArchived(ctx context.Context, req SetStickerSetArchivedRequest) (CommandResult, error) {
	if s == nil || s.stickerSets == nil || req.SetID <= 0 {
		return CommandResult{}, fmt.Errorf("valid sticker set and service are required")
	}
	return s.runCommand(ctx, req.CommandMeta, ActionSetStickerSetArchived, 0, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{"set_id": strconv.FormatInt(req.SetID, 10), "archived": req.Archived}
		if req.DryRun {
			return CommandResult{Message: "sticker set state change validated", Details: details}, nil
		}
		changed, err := s.stickerSets.AdminSetStickerSetArchived(ctx, req.SetID, req.Archived)
		details["changed"] = changed
		return CommandResult{Message: "sticker set state updated", Details: details}, err
	})
}

func (s *Service) SetStickerSetSortOrder(ctx context.Context, req SetStickerSetSortOrderRequest) (CommandResult, error) {
	if s == nil || s.stickerSets == nil || req.SetID <= 0 || req.SortOrder < math.MinInt32 || req.SortOrder > math.MaxInt32 {
		return CommandResult{}, domain.ErrStickerSetInvalid
	}
	return s.runCommand(ctx, req.CommandMeta, ActionSetStickerSetSortOrder, 0, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{"set_id": strconv.FormatInt(req.SetID, 10), "sort_order": req.SortOrder}
		if req.DryRun {
			return CommandResult{Message: "sticker set order change validated", Details: details}, nil
		}
		changed, err := s.stickerSets.AdminSetStickerSetSortOrder(ctx, req.SetID, req.SortOrder)
		details["changed"] = changed
		return CommandResult{Message: "sticker set order updated", Details: details}, err
	})
}

func (s *Service) RenameStickerSet(ctx context.Context, req RenameStickerSetRequest) (CommandResult, error) {
	if s == nil || s.stickerSets == nil || req.SetID <= 0 || strings.TrimSpace(req.Title) == "" {
		return CommandResult{}, domain.ErrStickerSetInvalid
	}
	return s.runCommand(ctx, req.CommandMeta, ActionRenameStickerSet, 0, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{"set_id": strconv.FormatInt(req.SetID, 10), "title": req.Title}
		if req.DryRun {
			return CommandResult{Message: "sticker set rename validated", Details: details}, nil
		}
		set, err := s.stickerSets.AdminRenameStickerSet(ctx, req.SetID, req.Title)
		if err != nil {
			return CommandResult{Details: details}, err
		}
		details["title"] = set.Title
		return CommandResult{Message: "sticker set renamed", Details: details}, nil
	})
}

func (s *Service) DeleteStickerSet(ctx context.Context, req DeleteStickerSetRequest) (CommandResult, error) {
	if s == nil || s.stickerSets == nil || req.SetID <= 0 {
		return CommandResult{}, domain.ErrStickerSetInvalid
	}
	return s.runCommand(ctx, req.CommandMeta, ActionDeleteStickerSet, 0, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{"set_id": strconv.FormatInt(req.SetID, 10)}
		if req.DryRun {
			return CommandResult{Message: "sticker set deletion validated", Details: details}, nil
		}
		kind, err := s.stickerSets.AdminDeleteStickerSet(ctx, req.SetID)
		if err != nil {
			return CommandResult{Details: details}, err
		}
		details["kind"] = string(kind)
		return CommandResult{Message: "sticker set deleted", Details: details}, nil
	})
}

func (s *Service) CreateStickerSet(ctx context.Context, req CreateStickerSetRequest) (CommandResult, error) {
	if s == nil || s.stickerSets == nil {
		return CommandResult{}, domain.ErrStickerSetInvalid
	}
	if strings.TrimSpace(req.Title) == "" || strings.TrimSpace(req.ShortName) == "" || strings.TrimSpace(req.Emoji) == "" {
		return CommandResult{}, domain.ErrStickerSetFileInvalid
	}
	mimeType, ok := s.stickerSets.ValidateStickerMaterialUpload(req.FileName, req.Data)
	if !ok {
		return CommandResult{}, domain.ErrStickerSetFileInvalid
	}
	kind := domain.StickerSetKindStickers
	if req.Kind == string(domain.StickerSetKindEmoji) {
		kind = domain.StickerSetKindEmoji
	}
	return s.runCommand(ctx, req.CommandMeta, ActionCreateStickerSet, 0, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{
			"title": req.Title, "short_name": req.ShortName, "kind": string(kind),
			"file_name": req.FileName, "mime_type": mimeType, "bytes": len(req.Data),
		}
		if err := s.stickerSets.ValidateAdminCreateStickerSet(ctx, req.Title, req.ShortName, req.Emoji, kind); err != nil {
			return CommandResult{Details: details}, err
		}
		if req.DryRun {
			return CommandResult{Message: "sticker pack validated", Details: details}, nil
		}
		doc, err := s.stickerSets.AdminUploadStickerMaterial(ctx, req.FileName, req.Data)
		if err != nil {
			return CommandResult{Details: details}, err
		}
		set, _, err := s.stickerSets.AdminCreateStickerSet(ctx, domain.CreateStickerSetRequest{
			Title:     req.Title,
			ShortName: req.ShortName,
			Kind:      kind,
			Items: []domain.StickerSetItemInput{{
				DocumentID:         doc.ID,
				DocumentAccessHash: doc.AccessHash,
				Emoji:              req.Emoji,
				Keywords:           req.Keywords,
			}},
		})
		if err != nil {
			return CommandResult{Details: details}, err
		}
		details["set_id"] = strconv.FormatInt(set.ID, 10)
		details["short_name"] = set.ShortName
		return CommandResult{Message: "sticker pack created", Details: details}, nil
	})
}

func (s *Service) AddStickerToSet(ctx context.Context, req AddStickerToSetRequest) (CommandResult, error) {
	if s == nil || s.stickerSets == nil || req.SetID <= 0 {
		return CommandResult{}, domain.ErrStickerSetInvalid
	}
	if strings.TrimSpace(req.Emoji) == "" {
		return CommandResult{}, domain.ErrStickerSetEmojiInvalid
	}
	mimeType, ok := s.stickerSets.ValidateStickerMaterialUpload(req.FileName, req.Data)
	if !ok {
		return CommandResult{}, domain.ErrStickerSetFileInvalid
	}
	return s.runCommand(ctx, req.CommandMeta, ActionAddStickerToSet, 0, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{
			"set_id": strconv.FormatInt(req.SetID, 10), "emoji": req.Emoji,
			"file_name": req.FileName, "mime_type": mimeType, "bytes": len(req.Data),
		}
		// Validate the target and item before materializing a loose
		// document/blob. Keeping this inside runCommand preserves replay of a
		// previously completed command even if the pack has since changed.
		if err := s.stickerSets.ValidateAdminAddStickerToSet(ctx, req.SetID, req.Emoji); err != nil {
			return CommandResult{Details: details}, err
		}
		if req.DryRun {
			return CommandResult{Message: "sticker upload validated", Details: details}, nil
		}
		doc, err := s.stickerSets.AdminUploadStickerMaterial(ctx, req.FileName, req.Data)
		if err != nil {
			return CommandResult{Details: details}, err
		}
		set, _, err := s.stickerSets.AdminAddStickerToSet(ctx, req.SetID, domain.StickerSetItemInput{
			DocumentID:         doc.ID,
			DocumentAccessHash: doc.AccessHash,
			Emoji:              req.Emoji,
			Keywords:           req.Keywords,
		})
		if err != nil {
			return CommandResult{Details: details}, err
		}
		details["document_id"] = strconv.FormatInt(doc.ID, 10)
		details["count"] = set.Count
		return CommandResult{Message: "sticker added", Details: details}, nil
	})
}

func (s *Service) RemoveStickerFromSet(ctx context.Context, req RemoveStickerFromSetRequest) (CommandResult, error) {
	if s == nil || s.stickerSets == nil || req.SetID <= 0 || req.DocumentID <= 0 {
		return CommandResult{}, domain.ErrStickerSetInvalid
	}
	return s.runCommand(ctx, req.CommandMeta, ActionRemoveStickerFromSet, 0, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{"set_id": strconv.FormatInt(req.SetID, 10), "document_id": strconv.FormatInt(req.DocumentID, 10)}
		if req.DryRun {
			return CommandResult{Message: "sticker removal validated", Details: details}, nil
		}
		set, _, err := s.stickerSets.AdminRemoveStickerFromSet(ctx, req.SetID, req.DocumentID)
		if err != nil {
			return CommandResult{Details: details}, err
		}
		details["count"] = set.Count
		return CommandResult{Message: "sticker removed", Details: details}, nil
	})
}

// GifCatalog returns every GIF catalog entry for the admin console's
// management view.
func (s *Service) GifCatalog(ctx context.Context) ([]domain.GifCatalogEntry, error) {
	if s == nil || s.gifCatalog == nil {
		return nil, domain.ErrGifCatalogUnavailable
	}
	return s.gifCatalog.AdminListGifCatalog(ctx)
}

func (s *Service) CreateGifCatalogEntry(ctx context.Context, req CreateGifCatalogEntryRequest) (CommandResult, error) {
	if s == nil || s.gifCatalog == nil {
		return CommandResult{}, domain.ErrGifCatalogUnavailable
	}
	if strings.TrimSpace(req.Title) == "" {
		return CommandResult{}, domain.ErrGifCatalogEntryInvalid
	}
	mimeType, ok := s.gifCatalog.ValidateGifUpload(req.FileName, req.Data)
	if !ok {
		return CommandResult{}, domain.ErrGifCatalogFileInvalid
	}
	digest := sha256.Sum256(req.Data)
	req.ContentSHA256 = hex.EncodeToString(digest[:])
	return s.runCommand(ctx, req.CommandMeta, ActionCreateGifCatalogEntry, 0, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{
			"title": req.Title, "file_name": req.FileName, "mime_type": mimeType, "bytes": len(req.Data),
		}
		entries, err := s.gifCatalog.AdminListGifCatalog(ctx)
		if err != nil {
			return CommandResult{Details: details}, err
		}
		if len(entries) >= domain.MaxGifCatalogEntries {
			return CommandResult{Details: details}, domain.ErrGifCatalogFull
		}
		if req.DryRun {
			return CommandResult{Message: "gif catalog entry validated", Details: details}, nil
		}
		doc, err := s.gifCatalog.AdminUploadGifMaterial(ctx, req.FileName, req.Data)
		if err != nil {
			return CommandResult{Details: details}, err
		}
		entry, err := s.gifCatalog.AdminCreateGifCatalogEntry(ctx, req.Title, doc.ID)
		if err != nil {
			return CommandResult{Details: details}, err
		}
		details["id"] = strconv.FormatInt(entry.ID, 10)
		details["document_id"] = strconv.FormatInt(doc.ID, 10)
		return CommandResult{Message: "gif catalog entry created", Details: details}, nil
	})
}

func (s *Service) SetGifCatalogEnabled(ctx context.Context, req SetGifCatalogEnabledRequest) (CommandResult, error) {
	if s == nil || s.gifCatalog == nil || req.ID <= 0 {
		return CommandResult{}, domain.ErrGifCatalogEntryInvalid
	}
	return s.runCommand(ctx, req.CommandMeta, ActionSetGifCatalogEnabled, 0, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{"id": strconv.FormatInt(req.ID, 10), "enabled": req.Enabled}
		if req.DryRun {
			return CommandResult{Message: "gif catalog entry state change validated", Details: details}, nil
		}
		changed, err := s.gifCatalog.AdminSetGifCatalogEnabled(ctx, req.ID, req.Enabled)
		details["changed"] = changed
		return CommandResult{Message: "gif catalog entry state updated", Details: details}, err
	})
}

func (s *Service) SetGifCatalogSortOrder(ctx context.Context, req SetGifCatalogSortOrderRequest) (CommandResult, error) {
	if s == nil || s.gifCatalog == nil || req.ID <= 0 || req.SortOrder < math.MinInt32 || req.SortOrder > math.MaxInt32 {
		return CommandResult{}, domain.ErrGifCatalogEntryInvalid
	}
	return s.runCommand(ctx, req.CommandMeta, ActionSetGifCatalogSortOrder, 0, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{"id": strconv.FormatInt(req.ID, 10), "sort_order": req.SortOrder}
		if req.DryRun {
			return CommandResult{Message: "gif catalog entry order change validated", Details: details}, nil
		}
		changed, err := s.gifCatalog.AdminSetGifCatalogSortOrder(ctx, req.ID, req.SortOrder)
		details["changed"] = changed
		return CommandResult{Message: "gif catalog entry order updated", Details: details}, err
	})
}

func (s *Service) SetGifCatalogCategory(ctx context.Context, req SetGifCatalogCategoryRequest) (CommandResult, error) {
	if s == nil || s.gifCatalog == nil || req.ID <= 0 {
		return CommandResult{}, fmt.Errorf("valid gif catalog entry and service are required")
	}
	if !domain.ValidGifCatalogCategory(req.Category) {
		return CommandResult{}, domain.ErrGifCatalogEntryInvalid
	}
	return s.runCommand(ctx, req.CommandMeta, ActionSetGifCatalogCategory, 0, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{"id": strconv.FormatInt(req.ID, 10), "category": req.Category}
		if req.DryRun {
			return CommandResult{Message: "gif catalog entry category change validated", Details: details}, nil
		}
		changed, err := s.gifCatalog.AdminSetGifCatalogCategory(ctx, req.ID, req.Category)
		details["changed"] = changed
		return CommandResult{Message: "gif catalog entry category updated", Details: details}, err
	})
}

func (s *Service) AutoCategorizeGifCatalog(ctx context.Context, req AutoCategorizeGifCatalogRequest) (CommandResult, error) {
	if s == nil || s.gifCatalog == nil {
		return CommandResult{}, fmt.Errorf("gif catalog service is not configured")
	}
	return s.runCommand(ctx, req.CommandMeta, ActionAutoCategorizeGifCatalog, 0, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{}
		if req.DryRun {
			return CommandResult{Message: "gif catalog auto-categorize validated", Details: details}, nil
		}
		count, err := s.gifCatalog.AdminAutoCategorizeGifCatalog(ctx)
		details["categorized"] = count
		return CommandResult{Message: "gif catalog auto-categorized", Details: details}, err
	})
}

func (s *Service) DeleteUncategorizedGifs(ctx context.Context, req DeleteUncategorizedGifsRequest) (CommandResult, error) {
	if s == nil || s.gifCatalog == nil {
		return CommandResult{}, fmt.Errorf("gif catalog service is not configured")
	}
	return s.runCommand(ctx, req.CommandMeta, ActionDeleteUncategorizedGifs, 0, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{}
		if req.DryRun {
			// A real count, not just "validated" -- this is a bulk delete, and
			// an operator confirming it deserves to know how many entries
			// they're about to lose before they do.
			entries, err := s.gifCatalog.AdminListGifCatalog(ctx)
			if err != nil {
				return CommandResult{}, err
			}
			uncategorized := 0
			for _, e := range entries {
				if e.Category == "" {
					uncategorized++
				}
			}
			details["would_delete"] = uncategorized
			return CommandResult{Message: fmt.Sprintf("would delete %d uncategorized gif(s)", uncategorized), Details: details}, nil
		}
		deletedEntries, deletedDocuments, err := s.gifCatalog.AdminDeleteUncategorizedGifs(ctx)
		details["deleted_entries"] = deletedEntries
		details["deleted_documents"] = deletedDocuments
		return CommandResult{Message: "uncategorized gifs deleted", Details: details}, err
	})
}

func (s *Service) DeleteGifCatalogEntry(ctx context.Context, req DeleteGifCatalogEntryRequest) (CommandResult, error) {
	if s == nil || s.gifCatalog == nil || req.ID <= 0 {
		return CommandResult{}, domain.ErrGifCatalogEntryInvalid
	}
	return s.runCommand(ctx, req.CommandMeta, ActionDeleteGifCatalogEntry, 0, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{"id": strconv.FormatInt(req.ID, 10)}
		if req.DryRun {
			return CommandResult{Message: "gif catalog entry deletion validated", Details: details}, nil
		}
		changed, err := s.gifCatalog.AdminDeleteGifCatalogEntry(ctx, req.ID)
		details["changed"] = changed
		return CommandResult{Message: "gif catalog entry deleted", Details: details}, err
	})
}

const maxStickerDocumentBytes = 8 << 20

// StickerDocumentAnimation returns a sticker/custom-emoji document's preview
// for the admin console's set-preview grid, plus the content type the caller
// should serve it as. Most packs are gzip-compressed TGS (decompressed here to
// Lottie JSON), but some (e.g. hand-uploaded packs) contain plain static
// raster stickers instead — those are returned as-is with their real image
// content type so the frontend can render an <img> instead of a Lottie player.
func (s *Service) StickerDocumentAnimation(ctx context.Context, documentID int64) ([]byte, string, bool, error) {
	if s == nil || s.photos == nil || documentID <= 0 {
		return nil, "", false, nil
	}
	chunk, found, err := s.photos.GetFile(ctx, domain.FileDownloadRequest{
		LocationKey: fmt.Sprintf("doc:%d", documentID),
		Limit:       maxStickerDocumentBytes + 1,
	})
	if err != nil {
		return nil, "", false, err
	}
	if !found || chunk.Total <= 0 || chunk.Total > maxStickerDocumentBytes || int64(len(chunk.Bytes)) != chunk.Total {
		return nil, "", false, nil
	}
	data := chunk.Bytes
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, "", false, nil
		}
		defer reader.Close()
		decompressed, err := io.ReadAll(io.LimitReader(reader, maxStickerDocumentBytes))
		if err != nil {
			return nil, "", false, nil
		}
		return decompressed, "application/json; charset=utf-8", true, nil
	}
	detected := http.DetectContentType(data)
	if !isSafeStickerPreviewImageType(detected) {
		return nil, "", false, nil
	}
	return data, detected, true, nil
}

// GifCatalogDocumentPreview returns a gif_catalog entry's document bytes for
// the admin panel's list-page preview, plus the content type to serve it as.
// Unlike StickerDocumentAnimation there is no gzip/Lottie branch to consider:
// every document AdminUploadGifMaterial creates is already a plain H.264 MP4
// (see gif_admin.go), so this only needs to fetch and sanity-check the blob.
func (s *Service) GifCatalogDocumentPreview(ctx context.Context, documentID int64) ([]byte, string, bool, error) {
	if s == nil || s.photos == nil || documentID <= 0 {
		return nil, "", false, nil
	}
	chunk, found, err := s.photos.GetFile(ctx, domain.FileDownloadRequest{
		LocationKey: fmt.Sprintf("doc:%d", documentID),
		Limit:       domain.MaxGifCatalogUploadSize + 1,
	})
	if err != nil {
		return nil, "", false, err
	}
	if !found || chunk.Total <= 0 || chunk.Total > domain.MaxGifCatalogUploadSize || int64(len(chunk.Bytes)) != chunk.Total {
		return nil, "", false, nil
	}
	detected := http.DetectContentType(chunk.Bytes)
	if detected != "video/mp4" && !strings.HasPrefix(detected, "video/") {
		return nil, "", false, nil
	}
	return chunk.Bytes, detected, true, nil
}

func isSafeStickerPreviewImageType(value string) bool {
	switch value {
	case "image/webp", "image/png", "image/jpeg", "image/gif":
		return true
	default:
		return false
	}
}

func (s *Service) EmojiAnimation(ctx context.Context, documentID int64) ([]byte, bool, error) {
	if s == nil || s.emoji == nil || documentID <= 0 {
		return nil, false, nil
	}
	return s.emoji.DocumentAnimationJSON(ctx, documentID)
}

func (s *Service) runCommand(ctx context.Context, meta CommandMeta, action string, targetUserID int64, targetPeer domain.Peer, request any, fn func() (CommandResult, error)) (CommandResult, error) {
	if s == nil || s.commands == nil {
		return CommandResult{}, fmt.Errorf("admin command store is not configured")
	}
	meta.CommandID = strings.TrimSpace(meta.CommandID)
	meta.Actor = strings.TrimSpace(meta.Actor)
	meta.Reason = strings.TrimSpace(meta.Reason)
	if meta.CommandID == "" || len(meta.CommandID) > maxCommandIDLength {
		return CommandResult{}, fmt.Errorf("command_id is required and must be <= %d bytes", maxCommandIDLength)
	}
	if meta.Actor == "" || len(meta.Actor) > maxActorLength {
		return CommandResult{}, fmt.Errorf("actor is required and must be <= %d bytes", maxActorLength)
	}
	if meta.Reason == "" || len(meta.Reason) > maxReasonLength {
		return CommandResult{}, fmt.Errorf("reason is required and must be <= %d bytes", maxReasonLength)
	}
	requestJSON, err := json.Marshal(request)
	if err != nil {
		return CommandResult{}, fmt.Errorf("marshal admin request: %w", err)
	}
	cmd, created, err := s.commands.BeginCommand(ctx, domain.AdminCommand{
		CommandID:    meta.CommandID,
		Actor:        meta.Actor,
		Action:       action,
		TargetUserID: targetUserID,
		TargetPeer:   targetPeer,
		DryRun:       meta.DryRun,
		Reason:       meta.Reason,
		RequestJSON:  requestJSON,
		Status:       domain.AdminCommandRunning,
		CreatedAt:    s.now(),
	})
	if err != nil {
		return CommandResult{}, err
	}
	if !created {
		if cmd.Action != action || cmd.DryRun != meta.DryRun || !sameJSON(cmd.RequestJSON, requestJSON) {
			return CommandResult{CommandID: meta.CommandID, Action: action, Status: string(domain.AdminCommandFailed), Error: "COMMAND_ID_CONFLICT", Message: "command_id is already bound to a different request"}, fmt.Errorf("COMMAND_ID_CONFLICT")
		}
		return resultFromCommand(cmd), nil
	}
	result, opErr := fn()
	result.CommandID = meta.CommandID
	result.Action = action
	result.DryRun = meta.DryRun
	result.TargetUserID = targetUserID
	result.TargetPeer = targetPeer
	status := domain.AdminCommandCompleted
	if opErr != nil {
		status = domain.AdminCommandFailed
		result.Status = string(status)
		result.Error = opErr.Error()
		if result.Message == "" {
			result.Message = "command failed"
		}
	} else {
		result.Status = string(status)
	}
	resultJSON, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return result, fmt.Errorf("marshal admin result: %w", marshalErr)
	}
	response := result
	if len(result.transientDetails) > 0 {
		response.Details = make(map[string]any, len(result.Details)+len(result.transientDetails))
		for key, value := range result.Details {
			response.Details[key] = value
		}
		for key, value := range result.transientDetails {
			response.Details[key] = value
		}
	}
	errorText := ""
	if opErr != nil {
		errorText = opErr.Error()
	}
	if _, err := s.commands.FinishCommand(ctx, meta.CommandID, status, resultJSON, errorText); err != nil {
		return response, err
	}
	return response, opErr
}

func sameJSON(a, b []byte) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return string(a) == string(b)
	}
	return reflect.DeepEqual(left, right)
}

func resultFromCommand(cmd domain.AdminCommand) CommandResult {
	var result CommandResult
	if len(cmd.ResultJSON) > 0 {
		if err := json.Unmarshal(cmd.ResultJSON, &result); err == nil {
			result.AlreadyExecuted = true
			return result
		}
	}
	result = CommandResult{
		CommandID:       cmd.CommandID,
		Action:          cmd.Action,
		Status:          string(cmd.Status),
		AlreadyExecuted: true,
		DryRun:          cmd.DryRun,
		TargetUserID:    cmd.TargetUserID,
		TargetPeer:      cmd.TargetPeer,
		Message:         "command already exists",
		Error:           cmd.Error,
	}
	return result
}

func (s *Service) notifyUserChanged(ctx context.Context, u domain.User) error {
	if s == nil || s.userNotifier == nil {
		return nil
	}
	return s.userNotifier.NotifyUserChanged(ctx, u)
}

func (s *Service) notifyUserModerationFlagsChanged(ctx context.Context, u domain.User) error {
	if s == nil || s.userModerationNotifier == nil {
		return s.notifyUserChanged(ctx, u)
	}
	return s.userModerationNotifier.NotifyUserModerationFlagsChanged(ctx, u)
}

func (s *Service) notifyAccountFreezeChanged(ctx context.Context, freeze domain.AccountFreeze) error {
	if s == nil || s.freezeNotifier == nil {
		return nil
	}
	return s.freezeNotifier.NotifyAccountFreezeChanged(ctx, freeze)
}

func (s *Service) notifyChannelChanged(ctx context.Context, ch domain.Channel) error {
	if s == nil || s.channelNotifier == nil {
		return nil
	}
	return s.channelNotifier.NotifyChannelChanged(ctx, ch)
}

func premiumCommandDetails(u domain.User, months int, now time.Time) map[string]any {
	active := u.PremiumActiveAt(now.Unix())
	base := now
	if active {
		base = time.Unix(int64(u.PremiumUntil), 0)
	}
	projected := 0
	if months > 0 {
		projected = int(base.AddDate(0, months, 0).Unix())
	}
	return map[string]any{
		"previous_premium_until":  u.PremiumUntil,
		"previous_premium_active": active,
		"months":                  months,
		"new_premium_until":       projected,
		"would_change":            months > 0 || u.PremiumUntil != 0,
	}
}

func revokeTargets(items []domain.Authorization, req RevokeSessionsRequest) ([]domain.Authorization, domain.Authorization, error) {
	if req.Hash != 0 {
		for _, a := range items {
			if a.Hash == req.Hash {
				return []domain.Authorization{a}, domain.Authorization{}, nil
			}
		}
		return nil, domain.Authorization{}, fmt.Errorf("authorization hash not found")
	}
	var keep domain.Authorization
	if req.KeepHash != 0 {
		found := false
		for _, a := range items {
			if a.Hash == req.KeepHash {
				keep = a
				found = true
				break
			}
		}
		if !found {
			return nil, domain.Authorization{}, fmt.Errorf("keep_hash authorization not found")
		}
	}
	targets := make([]domain.Authorization, 0, len(items))
	for _, a := range items {
		if req.KeepHash != 0 && a.Hash == req.KeepHash {
			continue
		}
		targets = append(targets, a)
	}
	return targets, keep, nil
}

func authorizationHashes(items []domain.Authorization) []string {
	hashes := make([]int64, 0, len(items))
	for _, a := range items {
		hashes = append(hashes, a.Hash)
	}
	sort.Slice(hashes, func(i, j int) bool { return hashes[i] < hashes[j] })
	out := make([]string, 0, len(hashes))
	for _, hash := range hashes {
		out = append(out, authorizationHashString(hash))
	}
	return out
}

func authorizationHashString(hash int64) string {
	if hash == 0 {
		return ""
	}
	return strconv.FormatInt(hash, 10)
}

func normalizeIDs(ids []int) ([]int, error) {
	if len(ids) == 0 {
		return nil, domain.ErrMessageIDInvalid
	}
	if len(ids) > domain.MaxDeleteMessageIDs {
		return nil, fmt.Errorf("too many ids: %d > %d", len(ids), domain.MaxDeleteMessageIDs)
	}
	seen := make(map[int]struct{}, len(ids))
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if id <= 0 || id > domain.MaxMessageBoxID {
			return nil, domain.ErrMessageIDInvalid
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Ints(out)
	return out, nil
}

func validatePrivateMessageSelection(ownerUserID int64, peer domain.Peer, ids []int, messages []domain.Message) ([]int, []int, error) {
	foundSet := make(map[int]domain.Message, len(messages))
	for _, msg := range messages {
		foundSet[msg.ID] = msg
		if msg.OwnerUserID != ownerUserID || msg.Peer.Type != domain.PeerTypeUser || msg.Peer.ID != peer.ID {
			return nil, nil, domain.ErrMessageIDInvalid
		}
	}
	found := make([]int, 0, len(messages))
	missing := make([]int, 0)
	for _, id := range ids {
		if _, ok := foundSet[id]; ok {
			found = append(found, id)
			continue
		}
		missing = append(missing, id)
	}
	return found, missing, nil
}

func summarizeDeleteResult(res domain.DeleteMessagesResult) []any {
	out := make([]any, 0, len(res.Deleted))
	for _, item := range res.Deleted {
		ids := append([]int(nil), item.MessageIDs...)
		sort.Ints(ids)
		pts, ptsCount := item.AffectedPts()
		out = append(out, map[string]any{
			"user_id":     item.UserID,
			"message_ids": ids,
			"pts":         pts,
			"pts_count":   ptsCount,
		})
	}
	return out
}

func messageIDs(messages []domain.Message) []int {
	out := make([]int, 0, len(messages))
	for _, msg := range messages {
		out = append(out, msg.ID)
	}
	return out
}
