package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"telesrv/internal/admin"
	"telesrv/internal/domain"
)

type Config struct {
	Addr  string
	Token string
	// ScopedTokens are additional bearer tokens with a bounded permission set
	// each. Token stays the unrestricted master token, so a deployment that
	// configures no scoped token behaves exactly as it did before.
	//
	// The shape mirrors config.AdminScopedToken without importing the loader --
	// only the main packages depend on internal/config -- so the caller converts:
	//
	//	scoped := make([]adminapi.ScopedToken, 0, len(cfg.AdminScopedTokens))
	//	for _, item := range cfg.AdminScopedTokens {
	//		scoped = append(scoped, adminapi.ScopedToken{
	//			Name: item.Name, Token: item.Token, Permissions: item.Permissions,
	//		})
	//	}
	ScopedTokens []ScopedToken
}

type Service interface {
	AccountAvatar(ctx context.Context, userID int64) ([]byte, string, bool, error)
	SetAccountFrozen(ctx context.Context, req admin.SetAccountFrozenRequest) (admin.CommandResult, error)
	GrantPremium(ctx context.Context, req admin.GrantPremiumRequest) (admin.CommandResult, error)
	SetVerified(ctx context.Context, req admin.SetVerifiedRequest) (admin.CommandResult, error)
	SetUserFlags(ctx context.Context, req admin.SetUserFlagsRequest) (admin.CommandResult, error)
	SetChannelVerified(ctx context.Context, req admin.SetChannelVerifiedRequest) (admin.CommandResult, error)
	SetChannelFlags(ctx context.Context, req admin.SetChannelFlagsRequest) (admin.CommandResult, error)
	CreateBot(ctx context.Context, req admin.CreateBotRequest) (admin.CommandResult, error)
	CreateBroadcast(ctx context.Context, req admin.CreateBroadcastRequest) (admin.CommandResult, error)
	DeleteBot(ctx context.Context, req admin.DeleteBotRequest) (admin.CommandResult, error)
	ExportBotToken(ctx context.Context, req admin.ExportBotTokenRequest) (admin.CommandResult, error)
	SetSupport(ctx context.Context, req admin.SetSupportRequest) (admin.CommandResult, error)
	SetUsername(ctx context.Context, req admin.SetUsernameRequest) (admin.CommandResult, error)
	SetProfile(ctx context.Context, req admin.SetProfileRequest) (admin.CommandResult, error)
	SetPhone(ctx context.Context, req admin.SetPhoneRequest) (admin.CommandResult, error)
	SetLoginEmail(ctx context.Context, req admin.SetLoginEmailRequest) (admin.CommandResult, error)
	SetAccountAvatar(ctx context.Context, req admin.SetAccountAvatarRequest) (admin.CommandResult, error)
	ChannelAvatar(ctx context.Context, channelID int64) ([]byte, string, bool, error)
	SetChannelAvatar(ctx context.Context, req admin.SetChannelAvatarRequest) (admin.CommandResult, error)
	SetUserColor(ctx context.Context, req admin.SetUserColorRequest) (admin.CommandResult, error)
	SetUserEmojiStatus(ctx context.Context, req admin.SetUserEmojiStatusRequest) (admin.CommandResult, error)
	SetChannelSettings(ctx context.Context, req admin.SetChannelSettingsRequest) (admin.CommandResult, error)
	SetChannelUsername(ctx context.Context, req admin.SetChannelUsernameRequest) (admin.CommandResult, error)
	SetChannelColor(ctx context.Context, req admin.SetChannelColorRequest) (admin.CommandResult, error)
	SetChannelEmojiStatus(ctx context.Context, req admin.SetChannelEmojiStatusRequest) (admin.CommandResult, error)
	RevokeSessions(ctx context.Context, req admin.RevokeSessionsRequest) (admin.CommandResult, error)
	DeletePrivateMessages(ctx context.Context, req admin.DeletePrivateMessagesRequest) (admin.CommandResult, error)
	DeletePrivateHistory(ctx context.Context, req admin.DeletePrivateHistoryRequest) (admin.CommandResult, error)
	SetStickerSetArchived(ctx context.Context, req admin.SetStickerSetArchivedRequest) (admin.CommandResult, error)
	SetStickerSetSortOrder(ctx context.Context, req admin.SetStickerSetSortOrderRequest) (admin.CommandResult, error)
	RenameStickerSet(ctx context.Context, req admin.RenameStickerSetRequest) (admin.CommandResult, error)
	DeleteStickerSet(ctx context.Context, req admin.DeleteStickerSetRequest) (admin.CommandResult, error)
	CreateStickerSet(ctx context.Context, req admin.CreateStickerSetRequest) (admin.CommandResult, error)
	AddStickerToSet(ctx context.Context, req admin.AddStickerToSetRequest) (admin.CommandResult, error)
	RemoveStickerFromSet(ctx context.Context, req admin.RemoveStickerFromSetRequest) (admin.CommandResult, error)
	StickerDocumentAnimation(ctx context.Context, documentID int64) ([]byte, string, bool, error)
	GifCatalogDocumentPreview(ctx context.Context, documentID int64) ([]byte, string, bool, error)
	CreateGifCatalogEntry(ctx context.Context, req admin.CreateGifCatalogEntryRequest) (admin.CommandResult, error)
	SetGifCatalogEnabled(ctx context.Context, req admin.SetGifCatalogEnabledRequest) (admin.CommandResult, error)
	SetGifCatalogSortOrder(ctx context.Context, req admin.SetGifCatalogSortOrderRequest) (admin.CommandResult, error)
	SetGifCatalogCategory(ctx context.Context, req admin.SetGifCatalogCategoryRequest) (admin.CommandResult, error)
	AutoCategorizeGifCatalog(ctx context.Context, req admin.AutoCategorizeGifCatalogRequest) (admin.CommandResult, error)
	DeleteUncategorizedGifs(ctx context.Context, req admin.DeleteUncategorizedGifsRequest) (admin.CommandResult, error)
	DeleteGifCatalogEntry(ctx context.Context, req admin.DeleteGifCatalogEntryRequest) (admin.CommandResult, error)
	EmojiAnimation(ctx context.Context, documentID int64) ([]byte, bool, error)
	ModerationCases(ctx context.Context, filter domain.ModerationCaseFilter) ([]domain.ModerationCase, error)
	ModerationCase(ctx context.Context, caseID int64) (domain.ModerationCaseDetail, bool, error)
	ModerationReport(ctx context.Context, reportID int64) (domain.ModerationReport, bool, error)
	ClaimModerationCase(ctx context.Context, caseID, expectedVersion int64, actor string) (domain.ModerationCase, error)
	DecideModerationCase(ctx context.Context, request domain.ModerationDecisionRequest) (domain.ModerationCaseDetail, bool, error)
	SubmitModerationAppeal(ctx context.Context, caseID, appellantUserID int64, text string) (domain.ModerationAppeal, bool, error)
	ReviewModerationAppeal(ctx context.Context, request domain.ModerationDecisionRequest) (domain.ModerationCaseDetail, bool, error)
	MintCollectibleUsername(ctx context.Context, req admin.MintCollectibleUsernameRequest) (admin.CommandResult, error)
	TransferCollectibleUsername(ctx context.Context, req admin.TransferCollectibleUsernameRequest) (admin.CommandResult, error)
	RevokeCollectibleUsername(ctx context.Context, req admin.RevokeCollectibleUsernameRequest) (admin.CommandResult, error)
	DeleteCollectibleUsername(ctx context.Context, req admin.DeleteCollectibleUsernameRequest) (admin.CommandResult, error)
	CollectibleUsernames(ctx context.Context, filter domain.CollectibleUsernameFilter) ([]domain.CollectibleUsername, error)
	CollectibleUsernameByID(ctx context.Context, id int64) (domain.CollectibleUsername, error)
	CollectibleUsernameTransfers(ctx context.Context, collectibleID int64, limit int) ([]domain.CollectibleUsernameTransfer, error)
	ClaimVerification(ctx context.Context, req admin.ClaimVerificationRequest) (admin.CommandResult, error)
	ApproveVerification(ctx context.Context, req admin.ApproveVerificationRequest) (admin.CommandResult, error)
	RejectVerification(ctx context.Context, req admin.RejectVerificationRequest) (admin.CommandResult, error)
	RevokeVerification(ctx context.Context, req admin.RevokeVerificationRequest) (admin.CommandResult, error)
	VerificationApplications(ctx context.Context, filter domain.VerificationApplicationFilter) ([]domain.VerificationApplication, error)
	VerificationApplication(ctx context.Context, applicationID int64) (domain.VerificationApplication, error)
	VerificationApplicationEvents(ctx context.Context, applicationID int64, limit int) ([]domain.VerificationApplicationEvent, error)
	VerificationCounts(ctx context.Context) (domain.VerificationStatusCounts, error)
	VerificationTargetSnapshot(ctx context.Context, targetType domain.VerificationTargetType, targetID int64) (domain.VerificationTarget, error)
	// Third-party bot verification. A separate mechanism from the official
	// verification methods above, over separate tables and separate permissions;
	// see botverification.go.
	GrantBotVerifier(ctx context.Context, req admin.GrantBotVerifierRequest) (admin.CommandResult, error)
	SetBotVerifierEnabled(ctx context.Context, req admin.SetBotVerifierEnabledRequest) (admin.CommandResult, error)
	RevokeBotVerifier(ctx context.Context, req admin.RevokeBotVerifierRequest) (admin.CommandResult, error)
	UpsertVerificationIcon(ctx context.Context, req admin.UpsertVerificationIconRequest) (admin.CommandResult, error)
	SetVerificationIconActive(ctx context.Context, req admin.SetVerificationIconActiveRequest) (admin.CommandResult, error)
	RevokeCustomVerification(ctx context.Context, req admin.RevokeCustomVerificationRequest) (admin.CommandResult, error)
	ApproveBotVerification(ctx context.Context, req admin.ApproveBotVerificationRequest) (admin.CommandResult, error)
	RejectBotVerification(ctx context.Context, req admin.RejectBotVerificationRequest) (admin.CommandResult, error)
	RevokeBotVerification(ctx context.Context, req admin.RevokeBotVerificationRequest) (admin.CommandResult, error)
	BotVerifiers(ctx context.Context, enabledOnly bool, limit int) ([]domain.BotVerifierSettings, error)
	BotVerifier(ctx context.Context, botID int64) (domain.BotVerifierSettings, error)
	VerificationIcons(ctx context.Context, activeOnly bool, limit int) ([]domain.VerificationIcon, error)
	CustomVerifications(ctx context.Context, filter domain.CustomVerificationFilter) ([]domain.CustomVerification, error)
	CustomVerificationRequests(ctx context.Context, filter domain.CustomVerificationRequestFilter) ([]domain.CustomVerificationRequest, error)
	CustomVerificationRequest(ctx context.Context, requestID int64) (domain.CustomVerificationRequest, error)
	CustomVerificationRequestCounts(ctx context.Context) (map[domain.CustomVerificationRequestStatus]int64, error)
	CustomVerificationMarkActive(ctx context.Context, verifierBotID int64, peer domain.Peer) (bool, error)
}

func Start(ctx context.Context, cfg Config, svc Service, log *zap.Logger) (*http.Server, error) {
	cfg.Addr = strings.TrimSpace(cfg.Addr)
	if cfg.Addr == "" {
		return nil, nil
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("TELESRV_ADMIN_API_TOKEN is required when TELESRV_ADMIN_API_ADDR is set")
	}
	if svc == nil {
		return nil, fmt.Errorf("admin api service is nil")
	}
	if log == nil {
		log = zap.NewNop()
	}
	server := &Server{token: cfg.Token, scoped: cfg.ScopedTokens, svc: svc, log: log}
	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Info("Admin API enabled", zap.String("addr", cfg.Addr))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Warn("Admin API exited", zap.Error(err))
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	return httpServer, nil
}

type Server struct {
	token  string
	scoped []ScopedToken
	svc    Service
	log    *zap.Logger
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /v1/accounts/set-frozen", s.authenticated(s.handleSetAccountFrozen))
	mux.HandleFunc("GET /v1/accounts/{id}/avatar", s.authenticated(s.handleAccountAvatar))
	mux.HandleFunc("POST /v1/accounts/grant-premium", s.authenticated(s.handleGrantPremium))
	mux.HandleFunc("POST /v1/accounts/set-verified", s.authenticated(s.handleSetVerified))
	mux.HandleFunc("POST /v1/accounts/set-flags", s.authenticated(s.handleSetUserFlags))
	mux.HandleFunc("POST /v1/accounts/set-support", s.authenticated(s.handleSetSupport))
	mux.HandleFunc("POST /v1/accounts/set-username", s.authenticated(s.handleSetUsername))
	mux.HandleFunc("POST /v1/accounts/set-profile", s.authenticated(s.handleSetProfile))
	mux.HandleFunc("POST /v1/accounts/set-phone", s.authenticated(s.handleSetPhone))
	mux.HandleFunc("POST /v1/accounts/set-login-email", s.authenticated(s.handleSetLoginEmail))
	mux.HandleFunc("POST /v1/accounts/set-avatar", s.authenticated(s.handleSetAccountAvatar))
	mux.HandleFunc("POST /v1/accounts/set-color", s.authenticated(s.handleSetUserColor))
	mux.HandleFunc("POST /v1/accounts/set-emoji-status", s.authenticated(s.handleSetUserEmojiStatus))
	mux.HandleFunc("POST /v1/accounts/revoke-sessions", s.authenticated(s.handleRevokeSessions))
	mux.HandleFunc("GET /v1/channels/{id}/avatar", s.authenticated(s.handleChannelAvatar))
	mux.HandleFunc("POST /v1/channels/set-avatar", s.authenticated(s.handleSetChannelAvatar))
	mux.HandleFunc("POST /v1/channels/set-verified", s.authenticated(s.handleSetChannelVerified))
	mux.HandleFunc("POST /v1/channels/set-flags", s.authenticated(s.handleSetChannelFlags))
	mux.HandleFunc("POST /v1/channels/set-settings", s.authenticated(s.handleSetChannelSettings))
	mux.HandleFunc("POST /v1/channels/set-username", s.authenticated(s.handleSetChannelUsername))
	mux.HandleFunc("POST /v1/channels/set-color", s.authenticated(s.handleSetChannelColor))
	mux.HandleFunc("POST /v1/channels/set-emoji-status", s.authenticated(s.handleSetChannelEmojiStatus))
	mux.HandleFunc("POST /v1/bots/create", s.authenticated(s.handleCreateBot))
	mux.HandleFunc("POST /v1/broadcasts/create", s.authenticated(s.handleCreateBroadcast))
	mux.HandleFunc("POST /v1/bots/delete", s.authenticated(s.handleDeleteBot))
	mux.HandleFunc("POST /v1/bots/export-token", s.authorized(PermissionBotTokenRead, s.handleExportBotToken))
	mux.HandleFunc("POST /v1/messages/delete", s.authenticated(s.handleDeleteMessages))
	mux.HandleFunc("POST /v1/messages/delete-history", s.authenticated(s.handleDeleteHistory))
	mux.HandleFunc("POST /v1/stickers/set-archived", s.authenticated(s.handleSetStickerSetArchived))
	mux.HandleFunc("POST /v1/stickers/set-sort-order", s.authenticated(s.handleSetStickerSetSortOrder))
	mux.HandleFunc("POST /v1/stickers/rename", s.authenticated(s.handleRenameStickerSet))
	mux.HandleFunc("POST /v1/stickers/delete", s.authenticated(s.handleDeleteStickerSet))
	mux.HandleFunc("POST /v1/stickers/create", s.authenticated(s.handleCreateStickerSet))
	mux.HandleFunc("POST /v1/stickers/add", s.authenticated(s.handleAddStickerToSet))
	mux.HandleFunc("POST /v1/stickers/remove", s.authenticated(s.handleRemoveStickerFromSet))
	mux.HandleFunc("POST /v1/gif-catalog/create", s.authenticated(s.handleCreateGifCatalogEntry))
	mux.HandleFunc("POST /v1/gif-catalog/set-enabled", s.authenticated(s.handleSetGifCatalogEnabled))
	mux.HandleFunc("POST /v1/gif-catalog/set-sort-order", s.authenticated(s.handleSetGifCatalogSortOrder))
	mux.HandleFunc("POST /v1/gif-catalog/set-category", s.authenticated(s.handleSetGifCatalogCategory))
	mux.HandleFunc("POST /v1/gif-catalog/auto-categorize", s.authenticated(s.handleAutoCategorizeGifCatalog))
	mux.HandleFunc("POST /v1/gif-catalog/delete-uncategorized", s.authenticated(s.handleDeleteUncategorizedGifs))
	mux.HandleFunc("POST /v1/gif-catalog/delete", s.authenticated(s.handleDeleteGifCatalogEntry))
	mux.HandleFunc("GET /v1/stickers/documents/{id}/animation", s.authenticated(s.handleStickerDocumentAnimation))
	mux.HandleFunc("GET /v1/gif-catalog/documents/{id}/preview", s.authenticated(s.handleGifCatalogDocumentPreview))
	mux.HandleFunc("GET /v1/emoji/{id}/animation", s.authenticated(s.handleEmojiAnimation))
	mux.HandleFunc("GET /v1/moderation/cases", s.authenticated(s.handleModerationCases))
	mux.HandleFunc("GET /v1/moderation/cases/{id}", s.authenticated(s.handleModerationCase))
	mux.HandleFunc("GET /v1/moderation/reports/{id}", s.authenticated(s.handleModerationReport))
	mux.HandleFunc("POST /v1/moderation/cases/{id}/claim", s.authenticated(s.handleClaimModerationCase))
	mux.HandleFunc("POST /v1/moderation/cases/{id}/decide", s.authenticated(s.handleDecideModerationCase))
	mux.HandleFunc("POST /v1/moderation/cases/{id}/appeals", s.authenticated(s.handleSubmitModerationAppeal))
	mux.HandleFunc("POST /v1/moderation/cases/{id}/appeals/{appeal_id}/review", s.authenticated(s.handleReviewModerationAppeal))
	mux.HandleFunc("POST /v1/collectible-usernames/mint", s.authenticated(s.handleMintCollectibleUsername))
	mux.HandleFunc("POST /v1/collectible-usernames/transfer", s.authenticated(s.handleTransferCollectibleUsername))
	mux.HandleFunc("POST /v1/collectible-usernames/revoke", s.authenticated(s.handleRevokeCollectibleUsername))
	mux.HandleFunc("POST /v1/collectible-usernames/delete", s.authenticated(s.handleDeleteCollectibleUsername))
	mux.HandleFunc("GET /v1/collectible-usernames", s.authenticated(s.handleCollectibleUsernames))
	mux.HandleFunc("GET /v1/collectible-usernames/{id}", s.authenticated(s.handleCollectibleUsername))
	// Official platform verification. Unlike every route above, these carry a
	// named permission, so a scoped token can be given the review surface and
	// nothing else. Revocation additionally requires verification.revoke.
	mux.HandleFunc("GET /v1/verification/applications", s.authorized(PermissionVerificationReview, s.handleVerificationApplications))
	mux.HandleFunc("GET /v1/verification/applications/{id}", s.authorized(PermissionVerificationReview, s.handleVerificationApplication))
	mux.HandleFunc("GET /v1/verification/counts", s.authorized(PermissionVerificationReview, s.handleVerificationCounts))
	mux.HandleFunc("POST /v1/verification/applications/{id}/claim", s.authorized(PermissionVerificationReview, s.handleClaimVerification))
	mux.HandleFunc("POST /v1/verification/applications/{id}/approve", s.authorized(PermissionVerificationReview, s.handleApproveVerification))
	mux.HandleFunc("POST /v1/verification/applications/{id}/reject", s.authorized(PermissionVerificationReview, s.handleRejectVerification))
	mux.HandleFunc("POST /v1/verification/revoke", s.authorizedAll(
		[]string{PermissionVerificationReview, PermissionVerificationRevoke}, s.handleRevokeVerification))
	// Third-party bot verification. Separate routes, separate permissions and
	// separate tables from the official verification block above -- the two
	// mechanisms never read each other's state. Reads and queue decisions need
	// botverification.review; appointing verifiers, curating icons and stripping a
	// granted mark need botverification.manage.
	mux.HandleFunc("GET /v1/botverification/verifiers", s.authorized(PermissionBotVerificationReview, s.handleBotVerifiers))
	mux.HandleFunc("GET /v1/botverification/icons", s.authorized(PermissionBotVerificationReview, s.handleVerificationIcons))
	mux.HandleFunc("GET /v1/botverification/marks", s.authorized(PermissionBotVerificationReview, s.handleCustomVerifications))
	mux.HandleFunc("GET /v1/botverification/requests", s.authorized(PermissionBotVerificationReview, s.handleCustomVerificationRequests))
	mux.HandleFunc("GET /v1/botverification/requests/{id}", s.authorized(PermissionBotVerificationReview, s.handleCustomVerificationRequest))
	mux.HandleFunc("GET /v1/botverification/counts", s.authorized(PermissionBotVerificationReview, s.handleCustomVerificationCounts))
	mux.HandleFunc("POST /v1/botverification/requests/{id}/approve", s.authorized(PermissionBotVerificationReview, s.handleApproveBotVerification))
	mux.HandleFunc("POST /v1/botverification/requests/{id}/reject", s.authorized(PermissionBotVerificationReview, s.handleRejectBotVerification))
	mux.HandleFunc("POST /v1/botverification/requests/{id}/revoke", s.authorized(PermissionBotVerificationReview, s.handleRevokeBotVerification))
	mux.HandleFunc("POST /v1/botverification/verifiers/grant", s.authorized(PermissionBotVerificationManage, s.handleGrantBotVerifier))
	mux.HandleFunc("POST /v1/botverification/verifiers/set-enabled", s.authorized(PermissionBotVerificationManage, s.handleSetBotVerifierEnabled))
	mux.HandleFunc("POST /v1/botverification/verifiers/revoke", s.authorized(PermissionBotVerificationManage, s.handleRevokeBotVerifier))
	mux.HandleFunc("POST /v1/botverification/icons/upsert", s.authorized(PermissionBotVerificationManage, s.handleUpsertVerificationIcon))
	mux.HandleFunc("POST /v1/botverification/icons/set-active", s.authorized(PermissionBotVerificationManage, s.handleSetVerificationIconActive))
	mux.HandleFunc("POST /v1/botverification/marks/revoke", s.authorized(PermissionBotVerificationManage, s.handleRevokeCustomVerification))
	return mux
}

func (s *Server) handleAccountAvatar(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || userID <= 0 {
		http.NotFound(w, r)
		return
	}
	data, mimeType, found, err := s.svc.AccountAvatar(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleSetAccountFrozen(w http.ResponseWriter, r *http.Request) {
	var req admin.SetAccountFrozenRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.SetAccountFrozen(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleGrantPremium(w http.ResponseWriter, r *http.Request) {
	var req admin.GrantPremiumRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.GrantPremium(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleSetVerified(w http.ResponseWriter, r *http.Request) {
	var req admin.SetVerifiedRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.SetVerified(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleSetChannelVerified(w http.ResponseWriter, r *http.Request) {
	var req admin.SetChannelVerifiedRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.SetChannelVerified(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleSetUserFlags(w http.ResponseWriter, r *http.Request) {
	var req admin.SetUserFlagsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.SetUserFlags(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleSetChannelFlags(w http.ResponseWriter, r *http.Request) {
	var req admin.SetChannelFlagsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.SetChannelFlags(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleSetSupport(w http.ResponseWriter, r *http.Request) {
	var req admin.SetSupportRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.SetSupport(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleSetUsername(w http.ResponseWriter, r *http.Request) {
	var req admin.SetUsernameRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.SetUsername(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleSetProfile(w http.ResponseWriter, r *http.Request) {
	var req admin.SetProfileRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.SetProfile(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleSetPhone(w http.ResponseWriter, r *http.Request) {
	var req admin.SetPhoneRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.SetPhone(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleSetLoginEmail(w http.ResponseWriter, r *http.Request) {
	var req admin.SetLoginEmailRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.SetLoginEmail(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleSetAccountAvatar(w http.ResponseWriter, r *http.Request) {
	var req admin.SetAccountAvatarRequest
	if !s.decodeAvatarUpload(w, r, &req.FileName, &req.Data) {
		return
	}
	if !decodeMultipartMetadata(w, r, &req) {
		return
	}
	result, err := s.svc.SetAccountAvatar(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleChannelAvatar(w http.ResponseWriter, r *http.Request) {
	channelID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || channelID <= 0 {
		http.NotFound(w, r)
		return
	}
	data, mimeType, found, err := s.svc.ChannelAvatar(r.Context(), channelID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleSetChannelAvatar(w http.ResponseWriter, r *http.Request) {
	var req admin.SetChannelAvatarRequest
	if !s.decodeAvatarUpload(w, r, &req.FileName, &req.Data) {
		return
	}
	if !decodeMultipartMetadata(w, r, &req) {
		return
	}
	result, err := s.svc.SetChannelAvatar(r.Context(), req)
	writeCommandResult(w, result, err)
}

// decodeAvatarUpload parses a multipart avatar-upload form shared by the
// account and channel avatar endpoints, reading the uploaded file into data.
func (s *Server) decodeAvatarUpload(w http.ResponseWriter, r *http.Request, fileName *string, data *[]byte) bool {
	r.Body = http.MaxBytesReader(w, r.Body, admin.MaxAccountAvatarBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		return false
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "avatar file is required")
		return false
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, admin.MaxAccountAvatarBytes+1))
	if err != nil || len(raw) == 0 || int64(len(raw)) > admin.MaxAccountAvatarBytes {
		writeError(w, http.StatusBadRequest, "avatar file is empty or too large")
		return false
	}
	*fileName = header.Filename
	*data = raw
	return true
}

// decodeMultipartMetadata decodes the JSON "metadata" form field of a
// multipart upload into dst, rejecting unknown fields.
func decodeMultipartMetadata(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(strings.NewReader(r.FormValue("metadata")))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid metadata: "+err.Error())
		return false
	}
	return true
}

func (s *Server) handleSetUserColor(w http.ResponseWriter, r *http.Request) {
	var req admin.SetUserColorRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.SetUserColor(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleSetUserEmojiStatus(w http.ResponseWriter, r *http.Request) {
	var req admin.SetUserEmojiStatusRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.SetUserEmojiStatus(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleSetChannelSettings(w http.ResponseWriter, r *http.Request) {
	var req admin.SetChannelSettingsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.SetChannelSettings(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleSetChannelUsername(w http.ResponseWriter, r *http.Request) {
	var req admin.SetChannelUsernameRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.SetChannelUsername(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleSetChannelColor(w http.ResponseWriter, r *http.Request) {
	var req admin.SetChannelColorRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.SetChannelColor(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleSetChannelEmojiStatus(w http.ResponseWriter, r *http.Request) {
	var req admin.SetChannelEmojiStatusRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.SetChannelEmojiStatus(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleCreateBot(w http.ResponseWriter, r *http.Request) {
	var req admin.CreateBotRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.CreateBot(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleCreateBroadcast(w http.ResponseWriter, r *http.Request) {
	var req admin.CreateBroadcastRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.CreateBroadcast(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleDeleteBot(w http.ResponseWriter, r *http.Request) {
	var req admin.DeleteBotRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.DeleteBot(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleExportBotToken(w http.ResponseWriter, r *http.Request) {
	var req admin.ExportBotTokenRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.ExportBotToken(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleRevokeSessions(w http.ResponseWriter, r *http.Request) {
	var req admin.RevokeSessionsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.RevokeSessions(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleDeleteMessages(w http.ResponseWriter, r *http.Request) {
	var req admin.DeletePrivateMessagesRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.DeletePrivateMessages(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleDeleteHistory(w http.ResponseWriter, r *http.Request) {
	var req admin.DeletePrivateHistoryRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.DeletePrivateHistory(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleSetStickerSetArchived(w http.ResponseWriter, r *http.Request) {
	var req admin.SetStickerSetArchivedRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.SetStickerSetArchived(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleSetStickerSetSortOrder(w http.ResponseWriter, r *http.Request) {
	var req admin.SetStickerSetSortOrderRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.SetStickerSetSortOrder(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleRenameStickerSet(w http.ResponseWriter, r *http.Request) {
	var req admin.RenameStickerSetRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.RenameStickerSet(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleDeleteStickerSet(w http.ResponseWriter, r *http.Request) {
	var req admin.DeleteStickerSetRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.DeleteStickerSet(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleCreateStickerSet(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, domain.MaxStickerMaterialDocumentSize+(2<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	var req admin.CreateStickerSetRequest
	dec := json.NewDecoder(strings.NewReader(r.FormValue("metadata")))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid metadata: "+err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "sticker file is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, domain.MaxStickerMaterialDocumentSize+1))
	if err != nil || len(data) == 0 || int64(len(data)) > domain.MaxStickerMaterialDocumentSize {
		writeError(w, http.StatusBadRequest, "sticker file is empty or too large")
		return
	}
	req.FileName = header.Filename
	req.Data = data
	result, err := s.svc.CreateStickerSet(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleAddStickerToSet(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, domain.MaxStickerMaterialDocumentSize+(2<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	var req admin.AddStickerToSetRequest
	dec := json.NewDecoder(strings.NewReader(r.FormValue("metadata")))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid metadata: "+err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "sticker file is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, domain.MaxStickerMaterialDocumentSize+1))
	if err != nil || len(data) == 0 || int64(len(data)) > domain.MaxStickerMaterialDocumentSize {
		writeError(w, http.StatusBadRequest, "sticker file is empty or too large")
		return
	}
	req.FileName = header.Filename
	req.Data = data
	result, err := s.svc.AddStickerToSet(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleRemoveStickerFromSet(w http.ResponseWriter, r *http.Request) {
	var req admin.RemoveStickerFromSetRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.RemoveStickerFromSet(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleCreateGifCatalogEntry(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, domain.MaxGifCatalogUploadSize+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	var req admin.CreateGifCatalogEntryRequest
	dec := json.NewDecoder(strings.NewReader(r.FormValue("metadata")))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid metadata: "+err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "gif file is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, domain.MaxGifCatalogUploadSize+1))
	if err != nil || len(data) == 0 || int64(len(data)) > domain.MaxGifCatalogUploadSize {
		writeError(w, http.StatusBadRequest, "gif file is empty or too large")
		return
	}
	req.FileName = header.Filename
	req.Data = data
	result, err := s.svc.CreateGifCatalogEntry(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleSetGifCatalogEnabled(w http.ResponseWriter, r *http.Request) {
	var req admin.SetGifCatalogEnabledRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.SetGifCatalogEnabled(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleSetGifCatalogSortOrder(w http.ResponseWriter, r *http.Request) {
	var req admin.SetGifCatalogSortOrderRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.SetGifCatalogSortOrder(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleSetGifCatalogCategory(w http.ResponseWriter, r *http.Request) {
	var req admin.SetGifCatalogCategoryRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.SetGifCatalogCategory(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleAutoCategorizeGifCatalog(w http.ResponseWriter, r *http.Request) {
	var req admin.AutoCategorizeGifCatalogRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.AutoCategorizeGifCatalog(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleDeleteUncategorizedGifs(w http.ResponseWriter, r *http.Request) {
	var req admin.DeleteUncategorizedGifsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.DeleteUncategorizedGifs(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleDeleteGifCatalogEntry(w http.ResponseWriter, r *http.Request) {
	var req admin.DeleteGifCatalogEntryRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.DeleteGifCatalogEntry(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleStickerDocumentAnimation(w http.ResponseWriter, r *http.Request) {
	documentID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || documentID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid document id")
		return
	}
	raw, contentType, found, err := s.svc.StickerDocumentAnimation(r.Context(), documentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "document animation not found")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (s *Server) handleGifCatalogDocumentPreview(w http.ResponseWriter, r *http.Request) {
	documentID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || documentID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid document id")
		return
	}
	raw, contentType, found, err := s.svc.GifCatalogDocumentPreview(r.Context(), documentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "gif document not found")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (s *Server) handleEmojiAnimation(w http.ResponseWriter, r *http.Request) {
	documentID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || documentID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid document id")
		return
	}
	raw, found, err := s.svc.EmojiAnimation(r.Context(), documentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "emoji animation not found")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

type moderationClaimRequest struct {
	ExpectedVersion int64  `json:"expected_version"`
	Actor           string `json:"actor"`
}

type moderationActionRequest struct {
	Kind    domain.ModerationActionKind `json:"kind"`
	Payload json.RawMessage             `json:"payload"`
}

type moderationDecisionRequest struct {
	ExpectedVersion int64                         `json:"expected_version"`
	Actor           string                        `json:"actor"`
	Reason          string                        `json:"reason"`
	CommandID       string                        `json:"command_id"`
	Kind            domain.ModerationDecisionKind `json:"kind"`
	Actions         []moderationActionRequest     `json:"actions"`
}

type moderationAppealRequest struct {
	AppellantUserID int64  `json:"appellant_user_id"`
	Text            string `json:"text"`
}

type moderationAppealReviewRequest struct {
	ExpectedVersion int64                     `json:"expected_version"`
	Actor           string                    `json:"actor"`
	Reason          string                    `json:"reason"`
	CommandID       string                    `json:"command_id"`
	Granted         bool                      `json:"granted"`
	Actions         []moderationActionRequest `json:"actions"`
}

func (s *Server) handleModerationCases(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit := 50
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = parsed
	}
	filter := domain.ModerationCaseFilter{
		AssignedTo: query.Get("assigned_to"),
		Limit:      limit,
	}
	if raw := query.Get("statuses"); raw != "" {
		for _, status := range strings.Split(raw, ",") {
			if status = strings.TrimSpace(status); status != "" {
				filter.Statuses = append(filter.Statuses, domain.ModerationCaseStatus(status))
			}
		}
	}
	if raw := query.Get("target_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid target id")
			return
		}
		filter.Target = domain.Peer{
			Type: domain.PeerType(query.Get("target_type")), ID: id,
		}
	}
	if raw := query.Get("before_updated_at"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid before_updated_at")
			return
		}
		filter.BeforeUpdate = parsed
		filter.BeforeID, _ = strconv.ParseInt(query.Get("before_id"), 10, 64)
	}
	items, err := s.svc.ModerationCases(r.Context(), filter)
	if err != nil {
		writeModerationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cases": moderationCasesResponse(items),
	})
}

func (s *Server) handleModerationCase(w http.ResponseWriter, r *http.Request) {
	caseID, ok := moderationPathID(w, r, "id")
	if !ok {
		return
	}
	detail, found, err := s.svc.ModerationCase(r.Context(), caseID)
	if err != nil {
		writeModerationError(w, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "moderation case not found")
		return
	}
	writeJSON(w, http.StatusOK, moderationCaseDetailResponse(detail))
}

func (s *Server) handleModerationReport(w http.ResponseWriter, r *http.Request) {
	reportID, ok := moderationPathID(w, r, "id")
	if !ok {
		return
	}
	report, found, err := s.svc.ModerationReport(r.Context(), reportID)
	if err != nil {
		writeModerationError(w, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "moderation report not found")
		return
	}
	writeJSON(w, http.StatusOK, moderationReportResponse(report))
}

func (s *Server) handleClaimModerationCase(w http.ResponseWriter, r *http.Request) {
	caseID, ok := moderationPathID(w, r, "id")
	if !ok {
		return
	}
	var request moderationClaimRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	item, err := s.svc.ClaimModerationCase(
		r.Context(), caseID, request.ExpectedVersion, request.Actor,
	)
	if err != nil {
		writeModerationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleDecideModerationCase(w http.ResponseWriter, r *http.Request) {
	caseID, ok := moderationPathID(w, r, "id")
	if !ok {
		return
	}
	var request moderationDecisionRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	detail, created, err := s.svc.DecideModerationCase(
		r.Context(), moderationDecisionDomain(caseID, 0, request),
	)
	if err != nil {
		writeModerationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"created": created, "case": moderationCaseDetailResponse(detail),
	})
}

func (s *Server) handleSubmitModerationAppeal(w http.ResponseWriter, r *http.Request) {
	caseID, ok := moderationPathID(w, r, "id")
	if !ok {
		return
	}
	var request moderationAppealRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	appeal, created, err := s.svc.SubmitModerationAppeal(
		r.Context(), caseID, request.AppellantUserID, request.Text,
	)
	if err != nil {
		writeModerationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"created": created, "appeal": appeal,
	})
}

func (s *Server) handleReviewModerationAppeal(w http.ResponseWriter, r *http.Request) {
	caseID, ok := moderationPathID(w, r, "id")
	if !ok {
		return
	}
	appealID, ok := moderationPathID(w, r, "appeal_id")
	if !ok {
		return
	}
	var request moderationAppealReviewRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	kind := domain.ModerationDecisionAppealDeny
	if request.Granted {
		kind = domain.ModerationDecisionAppealGrant
	}
	decision := moderationDecisionRequest{
		ExpectedVersion: request.ExpectedVersion, Actor: request.Actor,
		Reason: request.Reason, CommandID: request.CommandID,
		Kind: kind, Actions: request.Actions,
	}
	detail, created, err := s.svc.ReviewModerationAppeal(
		r.Context(), moderationDecisionDomain(caseID, appealID, decision),
	)
	if err != nil {
		writeModerationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"created": created, "case": moderationCaseDetailResponse(detail),
	})
}

func moderationCasesResponse(items []domain.ModerationCase) []domain.ModerationCase {
	if items == nil {
		return []domain.ModerationCase{}
	}
	return items
}

func moderationCaseDetailResponse(detail domain.ModerationCaseDetail) domain.ModerationCaseDetail {
	if detail.Decisions == nil {
		detail.Decisions = []domain.ModerationDecision{}
	}
	if detail.Actions == nil {
		detail.Actions = []domain.ModerationAction{}
	}
	if detail.Appeals == nil {
		detail.Appeals = []domain.ModerationAppeal{}
	}
	return detail
}

func moderationReportResponse(report domain.ModerationReport) domain.ModerationReport {
	if report.MediaHolds == nil {
		report.MediaHolds = []domain.ModerationMediaHold{}
	}
	return report
}

func moderationDecisionDomain(caseID, appealID int64, request moderationDecisionRequest) domain.ModerationDecisionRequest {
	actions := make([]domain.ModerationActionDraft, 0, len(request.Actions))
	for _, action := range request.Actions {
		payload := action.Payload
		if len(payload) == 0 {
			payload = json.RawMessage(`{}`)
		}
		actions = append(actions, domain.ModerationActionDraft{
			Kind: action.Kind, Payload: payload,
		})
	}
	return domain.ModerationDecisionRequest{
		CaseID: caseID, AppealID: appealID,
		ExpectedVersion: request.ExpectedVersion, Actor: request.Actor,
		Reason: request.Reason, CommandID: request.CommandID,
		Kind: request.Kind, Actions: actions,
	}
}

func moderationPathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid "+name)
		return 0, false
	}
	return id, true
}

func writeModerationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrModerationCaseNotFound),
		errors.Is(err, domain.ErrModerationReportNotFound),
		errors.Is(err, domain.ErrModerationEvidenceNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, domain.ErrModerationPermissionDenied):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, domain.ErrModerationCaseConflict),
		errors.Is(err, domain.ErrModerationActionConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, domain.ErrModerationRateLimited):
		writeError(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, domain.ErrModerationCaseInvalid),
		errors.Is(err, domain.ErrModerationActionInvalid),
		errors.Is(err, domain.ErrModerationReportInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (s *Server) handleMintCollectibleUsername(w http.ResponseWriter, r *http.Request) {
	var req admin.MintCollectibleUsernameRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.MintCollectibleUsername(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleTransferCollectibleUsername(w http.ResponseWriter, r *http.Request) {
	var req admin.TransferCollectibleUsernameRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.TransferCollectibleUsername(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleRevokeCollectibleUsername(w http.ResponseWriter, r *http.Request) {
	var req admin.RevokeCollectibleUsernameRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.RevokeCollectibleUsername(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleDeleteCollectibleUsername(w http.ResponseWriter, r *http.Request) {
	var req admin.DeleteCollectibleUsernameRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.DeleteCollectibleUsername(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleCollectibleUsernames(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := domain.CollectibleUsernameFilter{
		Status: domain.CollectibleUsernameStatus(strings.TrimSpace(query.Get("status"))),
		Query:  query.Get("q"),
	}
	if filter.Status != "" && !filter.Status.Valid() {
		writeCodedError(w, http.StatusBadRequest, admin.CodeCollectibleStateInvalid, "invalid status")
		return
	}
	owner, ok := collectibleOwnerFilter(w, query)
	if !ok {
		return
	}
	filter.Owner = owner
	limit, ok := optionalQueryInt(w, query, "limit")
	if !ok {
		return
	}
	filter.Limit = limit
	beforeID, ok := optionalQueryInt64(w, query, "before_id")
	if !ok {
		return
	}
	filter.BeforeID = beforeID
	items, err := s.svc.CollectibleUsernames(r.Context(), filter)
	if err != nil {
		writeCollectibleUsernameError(w, err)
		return
	}
	assets := make([]map[string]any, 0, len(items))
	for _, item := range items {
		assets = append(assets, collectibleUsernameResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"assets": assets})
}

func (s *Server) handleCollectibleUsername(w http.ResponseWriter, r *http.Request) {
	id, ok := moderationPathID(w, r, "id")
	if !ok {
		return
	}
	asset, err := s.svc.CollectibleUsernameByID(r.Context(), id)
	if err != nil {
		writeCollectibleUsernameError(w, err)
		return
	}
	limit, ok := optionalQueryInt(w, r.URL.Query(), "limit")
	if !ok {
		return
	}
	transfers, err := s.svc.CollectibleUsernameTransfers(r.Context(), asset.ID, limit)
	if err != nil {
		writeCollectibleUsernameError(w, err)
		return
	}
	log := make([]map[string]any, 0, len(transfers))
	for _, item := range transfers {
		log = append(log, collectibleUsernameTransferResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"asset": collectibleUsernameResponse(asset), "transfers": log,
	})
}

// collectibleOwnerFilter reads the optional owner filter. At most one of the two
// identifiers may be present, mirroring the mint/transfer request shape.
func collectibleOwnerFilter(w http.ResponseWriter, query url.Values) (domain.Peer, bool) {
	userID, ok := optionalQueryInt64(w, query, "owner_user_id")
	if !ok {
		return domain.Peer{}, false
	}
	channelID, ok := optionalQueryInt64(w, query, "owner_channel_id")
	if !ok {
		return domain.Peer{}, false
	}
	switch {
	case userID > 0 && channelID > 0:
		writeError(w, http.StatusBadRequest, "at most one owner filter is allowed")
		return domain.Peer{}, false
	case userID > 0:
		return domain.Peer{Type: domain.PeerTypeUser, ID: userID}, true
	case channelID > 0:
		return domain.Peer{Type: domain.PeerTypeChannel, ID: channelID}, true
	default:
		return domain.Peer{}, true
	}
}

func optionalQueryInt64(w http.ResponseWriter, query url.Values, name string) (int64, bool) {
	raw := strings.TrimSpace(query.Get(name))
	if raw == "" {
		return 0, true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		writeError(w, http.StatusBadRequest, "invalid "+name)
		return 0, false
	}
	return value, true
}

func optionalQueryInt(w http.ResponseWriter, query url.Values, name string) (int, bool) {
	value, ok := optionalQueryInt64(w, query, name)
	if !ok {
		return 0, false
	}
	if value > math.MaxInt32 {
		writeError(w, http.StatusBadRequest, "invalid "+name)
		return 0, false
	}
	return int(value), true
}

// collectibleUsernameResponse renders one asset. Every int64 crosses the JSON
// boundary as a decimal string: asset ids and nanoton amounts exceed the exact
// range of a JSON number, and a rounded id would address the wrong asset.
func collectibleUsernameResponse(asset domain.CollectibleUsername) map[string]any {
	out := map[string]any{
		"id":                  strconv.FormatInt(asset.ID, 10),
		"username":            asset.Username,
		"status":              string(asset.Status),
		"owner_type":          string(asset.Owner.Type),
		"owner_id":            strconv.FormatInt(asset.Owner.ID, 10),
		"purchase_date":       asset.Info().PurchaseDate,
		"currency":            asset.Currency,
		"amount":              strconv.FormatInt(asset.Amount, 10),
		"crypto_currency":     asset.CryptoCurrency,
		"crypto_amount":       strconv.FormatInt(asset.CryptoAmount, 10),
		"url":                 asset.URL,
		"original_owner_type": string(asset.OriginalOwner.Type),
		"original_owner_id":   strconv.FormatInt(asset.OriginalOwner.ID, 10),
		"transfer_count":      asset.TransferCount,
		"version":             strconv.FormatInt(asset.Version, 10),
	}
	if !asset.CreatedAt.IsZero() {
		out["created_at"] = asset.CreatedAt.UTC().Format(time.RFC3339)
	}
	if !asset.UpdatedAt.IsZero() {
		out["updated_at"] = asset.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func collectibleUsernameTransferResponse(item domain.CollectibleUsernameTransfer) map[string]any {
	out := map[string]any{
		"id":             strconv.FormatInt(item.ID, 10),
		"collectible_id": strconv.FormatInt(item.CollectibleID, 10),
		"kind":           string(item.Kind),
		"from_type":      string(item.From.Type),
		"from_id":        strconv.FormatInt(item.From.ID, 10),
		"to_type":        string(item.To.Type),
		"to_id":          strconv.FormatInt(item.To.ID, 10),
		"currency":       item.Currency,
		"amount":         strconv.FormatInt(item.Amount, 10),
		"actor":          item.Actor,
		"reason":         item.Reason,
		"command_key":    item.CommandKey,
	}
	if !item.CreatedAt.IsZero() {
		out["created_at"] = item.CreatedAt.UTC().Format(time.RFC3339)
	}
	return out
}

// accountRatingResponse renders one composite rating. The score and every
// component stay decimal strings for the same exactness reason as the asset ids.

// writeCollectibleUsernameError maps a collectible-username failure onto its
// stable admin code and the matching HTTP status, the way writeModerationError
// does for moderation. An unmapped failure stays a 500 with its own text rather
// than being dressed up as a client error.
func writeCollectibleUsernameError(w http.ResponseWriter, err error) {
	code := admin.CollectibleUsernameErrorCode(err)
	status := http.StatusInternalServerError
	switch code {
	case admin.CodeCollectibleNotFound:
		status = http.StatusNotFound
	case admin.CodeUsernameOccupied, admin.CodeCollectibleBurned,
		admin.CodeCollectiblePeerLimit, admin.CodeCollectibleNotOwned:
		status = http.StatusConflict
	case admin.CodeUsernameInvalid, admin.CodeUsernameNotCollectible,
		admin.CodeCollectibleCurrencyInvalid, admin.CodeCollectibleStateInvalid:
		status = http.StatusBadRequest
	}
	writeCodedError(w, status, code, err.Error())
}

func writeCodedError(w http.ResponseWriter, status int, code, msg string) {
	body := map[string]string{"error": msg}
	if code != "" {
		body["code"] = code
	}
	writeJSON(w, status, body)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return false
	}
	return true
}

func writeCommandResult(w http.ResponseWriter, result admin.CommandResult, err error) {
	status := http.StatusOK
	if err != nil {
		status = http.StatusBadRequest
		if result.CommandID == "" {
			result = admin.CommandResult{Status: "failed", Message: "command failed", Error: err.Error()}
		}
	}
	writeJSON(w, status, result)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
