// Package web serves telesrv's read-only public link landing pages.
package web

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"

	"telesrv/internal/domain"
	"telesrv/internal/links"
)

type Config struct {
	Addr          string
	PublicBaseURL string
	AppScheme     string
	WebBaseURL    string
	AppName       string
	DownloadURL   string
	StickerSets   StickerSetResolver
	Users         UsernameResolver
	Channels      PublicChannelResolver
	Privacy       AnonymousPrivacyResolver
	Photos        ProfilePhotoResolver
}

type StickerSetResolver interface {
	ResolveStickerSet(ctx context.Context, ref domain.StickerSetRef) (domain.StickerSet, []domain.Document, bool, error)
}

type UsernameResolver interface {
	ByUsername(ctx context.Context, username string) (domain.User, bool, error)
}

// PublicChannelResolver exposes only the viewer-independent public username
// projection. viewerUserID is always zero for this anonymous Web endpoint.
type PublicChannelResolver interface {
	ResolvePublicChannelUsername(ctx context.Context, viewerUserID int64, username string) (domain.Channel, bool, error)
	// ResolvePublicChannelInvite resolves a private invite-link hash (the
	// "/+<hash>" landing page) to its target channel/supergroup.
	ResolvePublicChannelInvite(ctx context.Context, hash string) (domain.Channel, domain.ChannelInvite, bool, error)
}

type AnonymousPrivacyResolver interface {
	CanSeeAnonymous(ctx context.Context, ownerUserID int64, key domain.PrivacyKey) (bool, error)
}

type ProfilePhotoResolver interface {
	CurrentProfilePhotoKind(ctx context.Context, ownerType domain.PeerType, ownerID int64, kind domain.ProfilePhotoKind) (domain.Photo, bool, error)
	GetPhoto(ctx context.Context, id int64) (domain.Photo, bool, error)
	GetFile(ctx context.Context, req domain.FileDownloadRequest) (domain.FileChunk, bool, error)
}

func Start(ctx context.Context, cfg Config, logger *zap.Logger) (*http.Server, error) {
	addr := strings.TrimSpace(cfg.Addr)
	if addr == "" {
		return nil, nil
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	handler, err := newHandler(cfg, logger)
	if err != nil {
		return nil, err
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	go func() {
		logger.Info("Public link Web endpoint enabled",
			zap.String("addr", addr),
			zap.String("public_base_url", cfg.PublicBaseURL),
			zap.String("app_scheme", cfg.AppScheme),
			zap.String("web_base_url", cfg.WebBaseURL),
			zap.String("download_url", cfg.DownloadURL))
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Warn("Public link Web endpoint exited", zap.Error(err))
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	return srv, nil
}

func NewHandler(cfg Config) (http.Handler, error) {
	return newHandler(cfg, zap.NewNop())
}

func newHandler(cfg Config, logger *zap.Logger) (http.Handler, error) {
	var err error
	if cfg.StickerSets == nil {
		return nil, fmt.Errorf("public Web sticker set resolver is nil")
	}
	if strings.TrimSpace(cfg.AppName) == "" {
		cfg.AppName = links.DefaultAppName
	}
	if cfg.PublicBaseURL, err = links.ValidateBaseURL(cfg.PublicBaseURL); err != nil {
		return nil, fmt.Errorf("public base URL: %w", err)
	}
	if cfg.AppScheme, err = links.ValidateAppScheme(cfg.AppScheme); err != nil {
		return nil, fmt.Errorf("app scheme: %w", err)
	}
	// WebBaseURL is optional: leaving it unset disables the "Open in Web"
	// button on the username landing page instead of falling back to a
	// default URL that would point at someone else's Web client.
	if strings.TrimSpace(cfg.WebBaseURL) != "" {
		if cfg.WebBaseURL, err = links.ValidateBaseURL(cfg.WebBaseURL); err != nil {
			return nil, fmt.Errorf("Web base URL: %w", err)
		}
	} else {
		cfg.WebBaseURL = ""
	}
	if cfg.AppName, err = links.ValidateAppName(cfg.AppName); err != nil {
		return nil, fmt.Errorf("app name: %w", err)
	}
	if strings.TrimSpace(cfg.DownloadURL) == "" {
		cfg.DownloadURL = links.DefaultDownloadURL
	}
	if cfg.DownloadURL, err = links.ValidateBaseURL(cfg.DownloadURL); err != nil {
		return nil, fmt.Errorf("download URL: %w", err)
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	h := &handler{
		stickerSets:   cfg.StickerSets,
		users:         cfg.Users,
		channels:      cfg.Channels,
		privacy:       cfg.Privacy,
		photos:        cfg.Photos,
		publicBaseURL: cfg.PublicBaseURL,
		appScheme:     cfg.AppScheme,
		webBaseURL:    cfg.WebBaseURL,
		appName:       cfg.AppName,
		downloadURL:   cfg.DownloadURL,
		logger:        logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("GET /_public/assets/logo.png", h.brandLogo)
	mux.HandleFunc("GET /_public/assets/fonts/{file}", h.brandFont)
	mux.HandleFunc("GET /_public/avatar/{username}/{photoID}", h.publicAvatar)
	mux.HandleFunc("GET /_public/invite-avatar/{hash}/{photoID}", h.publicInviteAvatar)
	mux.HandleFunc("GET /addstickers/{shortName}", h.addStickers)
	mux.HandleFunc("GET /addemoji/{shortName}", h.addEmoji)
	mux.HandleFunc("GET /addlist/{slug}", h.addList)
	mux.HandleFunc("GET /{username}", h.usernameLink)
	mux.HandleFunc("GET /{username}/{$}", h.usernameLink)
	return publicSecurityHeaders(mux), nil
}

type handler struct {
	stickerSets   StickerSetResolver
	users         UsernameResolver
	channels      PublicChannelResolver
	privacy       AnonymousPrivacyResolver
	photos        ProfilePhotoResolver
	publicBaseURL string
	appScheme     string
	webBaseURL    string
	appName       string
	downloadURL   string
	logger        *zap.Logger
}

func (h *handler) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (h *handler) addStickers(w http.ResponseWriter, r *http.Request) {
	h.serveSet(w, r, "addstickers")
}

func (h *handler) addEmoji(w http.ResponseWriter, r *http.Request) {
	h.serveSet(w, r, "addemoji")
}

func (h *handler) addList(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.PathValue("slug"))
	if !validSlugPath(slug) {
		http.NotFound(w, r)
		return
	}
	app := h.appURL("addlist", "slug", slug)
	data := pageData{
		AppName:      h.appName,
		Title:        "Shared Folder",
		KindLabel:    "shared folder",
		Subtitle:     slug,
		Description:  "This page opens the app so you can preview and add this shared folder.",
		CanonicalURL: h.publicURL("addlist", slug),
		AppURL:       template.URL(app),
		DownloadURL:  h.downloadURL,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60")
	if err := landingTemplate.Execute(w, data); err != nil {
		http.Error(w, "render shared folder page failed", http.StatusInternalServerError)
	}
}

func (h *handler) usernameLink(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.PathValue("username"))
	if strings.HasPrefix(raw, "+") {
		h.inviteLink(w, r, strings.TrimPrefix(raw, "+"))
		return
	}
	username := raw
	if !validUsernamePath(username) {
		h.serveUsernameNotFound(w, username)
		return
	}
	params, ok := publicResolveQuery(r.URL.RawQuery)
	if !ok {
		http.Error(w, "public link query is too large or invalid", http.StatusRequestURITooLong)
		return
	}
	peer, found, err := h.resolvePublicPeer(r.Context(), username)
	if err != nil {
		h.logger.Error("Public username lookup failed", zap.String("username", username), zap.Error(err))
		http.Error(w, "username lookup failed", http.StatusInternalServerError)
		return
	}
	if !found {
		h.serveUsernameNotFound(w, username)
		return
	}
	params.Set("domain", peer.username)
	app := schemeURLValues(h.appScheme, "resolve", params)
	description := peer.about
	if description == "" {
		description = peer.fallbackDescription(h.appName)
	}
	grad := avatarGradient(peer.id)
	data := usernamePageData{
		AppName:      h.appName,
		AppInitial:   appInitial(h.appName),
		Title:        peer.title,
		Username:     peer.username,
		Verified:     peer.verified,
		Extra:        peer.extra(),
		Description:  description,
		AboutText:    peer.about,
		CanonicalURL: h.publicUsernameURL(peer.username),
		HomeURL:      h.publicBaseURL + "/",
		DownloadURL:  h.downloadURL,
		AppURL:       template.URL(app),
		ButtonLabel:  peer.buttonLabel(),
		Initials:     peer.initials(),
		GradFrom:     grad[0],
		GradTo:       grad[1],
	}
	if h.webBaseURL != "" {
		legacy := schemeURLValues("tg", "resolve", params)
		data.WebURL = template.URL(publicWebAppURL(h.webBaseURL, legacy))
	}
	if peer.hasPhoto {
		data.PhotoURL = h.publicAvatarURL(peer.username, peer.photo.ID)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60, must-revalidate")
	if err := usernameLandingTemplate.Execute(w, data); err != nil {
		h.logger.Error("Render public username page failed", zap.String("username", peer.username), zap.Error(err))
	}
}

// inviteLink serves the "/+<hash>" private invite-link preview page: the
// channel/supergroup's photo, title, and member count, with a Join button
// that deep-links into the app. It never requires or reveals viewer-specific
// membership state.
func (h *handler) inviteLink(w http.ResponseWriter, r *http.Request, hash string) {
	if !validInviteHashPath(hash) || h.channels == nil {
		h.serveInviteNotFound(w)
		return
	}
	ch, invite, found, err := h.channels.ResolvePublicChannelInvite(r.Context(), hash)
	if err != nil {
		h.logger.Error("Public invite lookup failed", zap.String("invite_hash", hash), zap.Error(err))
		http.Error(w, "invite lookup failed", http.StatusInternalServerError)
		return
	}
	if !found {
		h.serveInviteNotFound(w)
		return
	}
	peer, ok, err := h.publicInviteChannelPeer(r.Context(), ch)
	if err != nil || !ok {
		if err != nil {
			h.logger.Error("Public invite channel projection failed", zap.String("invite_hash", hash), zap.Error(err))
		}
		h.serveInviteNotFound(w)
		return
	}
	title := peer.title
	if title == "" {
		title = strings.TrimSpace(invite.Title)
	}
	app := h.appURL("join", "invite", hash)
	description := peer.about
	if description == "" {
		description = peer.fallbackDescription(h.appName)
	}
	grad := avatarGradient(peer.id)
	data := usernamePageData{
		AppName:      h.appName,
		AppInitial:   appInitial(h.appName),
		Title:        title,
		Verified:     peer.verified,
		Extra:        peer.extra(),
		Description:  description,
		AboutText:    peer.about,
		CanonicalURL: h.publicInviteURL(hash),
		HomeURL:      h.publicBaseURL + "/",
		DownloadURL:  h.downloadURL,
		AppURL:       template.URL(app),
		ButtonLabel:  peer.inviteButtonLabel(),
		Initials:     peer.initials(),
		GradFrom:     grad[0],
		GradTo:       grad[1],
	}
	if peer.hasPhoto {
		data.PhotoURL = h.publicInviteAvatarURL(hash, peer.photo.ID)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60, must-revalidate")
	if err := usernameLandingTemplate.Execute(w, data); err != nil {
		h.logger.Error("Render public invite page failed", zap.String("invite_hash", hash), zap.Error(err))
	}
}

const maxPublicAvatarBytes = 4 << 20

func (h *handler) publicAvatar(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.PathValue("username"))
	photoID, err := strconv.ParseInt(r.PathValue("photoID"), 10, 64)
	if err != nil || photoID <= 0 || !validUsernamePath(username) || h.photos == nil {
		http.NotFound(w, r)
		return
	}
	peer, found, err := h.resolvePublicPeer(r.Context(), username)
	if err != nil {
		h.logger.Error("Public avatar peer lookup failed", zap.String("username", username), zap.Error(err))
		http.Error(w, "avatar lookup failed", http.StatusInternalServerError)
		return
	}
	if !found || !peer.hasPhoto || peer.photo.ID != photoID {
		http.NotFound(w, r)
		return
	}
	h.servePeerAvatarPhoto(w, r, peer, photoID, zap.String("username", username))
}

func (h *handler) publicInviteAvatar(w http.ResponseWriter, r *http.Request) {
	hash := strings.TrimSpace(r.PathValue("hash"))
	photoID, err := strconv.ParseInt(r.PathValue("photoID"), 10, 64)
	if err != nil || photoID <= 0 || !validInviteHashPath(hash) || h.photos == nil || h.channels == nil {
		http.NotFound(w, r)
		return
	}
	ch, _, found, err := h.channels.ResolvePublicChannelInvite(r.Context(), hash)
	if err != nil {
		h.logger.Error("Public invite avatar lookup failed", zap.String("invite_hash", hash), zap.Error(err))
		http.Error(w, "avatar lookup failed", http.StatusInternalServerError)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	peer, ok, err := h.publicInviteChannelPeer(r.Context(), ch)
	if err != nil || !ok || !peer.hasPhoto || peer.photo.ID != photoID {
		http.NotFound(w, r)
		return
	}
	h.servePeerAvatarPhoto(w, r, peer, photoID, zap.String("invite_hash", hash))
}

// servePeerAvatarPhoto streams an already-authorized public peer's profile
// photo. logField identifies the lookup key (username or invite hash) for
// diagnostics only; the caller has already verified peer.hasPhoto and that
// peer.photo.ID matches the requested photoID.
func (h *handler) servePeerAvatarPhoto(w http.ResponseWriter, r *http.Request, peer publicPeer, photoID int64, logField zap.Field) {
	size, inline, ok := bestPublicPhotoSize(peer.photo.Sizes)
	if !ok {
		http.NotFound(w, r)
		return
	}
	etag := fmt.Sprintf("\"public-avatar-%d-%s-%d\"", peer.photo.ID, size.Type, size.Size)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=300, must-revalidate")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	data := inline
	mimeType := ""
	if len(data) == 0 {
		chunk, found, err := h.photos.GetFile(r.Context(), domain.FileDownloadRequest{
			LocationKey: fmt.Sprintf("photo:%d:%s", peer.photo.ID, size.Type),
			Limit:       maxPublicAvatarBytes + 1,
		})
		if err != nil {
			h.logger.Error("Read public avatar blob failed", logField, zap.Int64("photo_id", photoID), zap.Error(err))
			http.Error(w, "avatar read failed", http.StatusInternalServerError)
			return
		}
		if !found || chunk.Total <= 0 || chunk.Total > maxPublicAvatarBytes || int64(len(chunk.Bytes)) != chunk.Total {
			h.logger.Warn("Public avatar blob is missing or outside bounds", logField, zap.Int64("photo_id", photoID), zap.Int64("total", chunk.Total), zap.Int("bytes", len(chunk.Bytes)))
			http.NotFound(w, r)
			return
		}
		data = chunk.Bytes
		mimeType = chunk.MimeType
	}
	if len(data) == 0 || len(data) > maxPublicAvatarBytes {
		http.NotFound(w, r)
		return
	}
	detected := http.DetectContentType(data)
	if !safePublicImageType(detected) {
		h.logger.Warn("Public avatar blob is not a safe raster image", logField, zap.Int64("photo_id", photoID), zap.String("detected_type", detected), zap.String("stored_type", mimeType))
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", detected)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if peer.photo.Date > 0 {
		w.Header().Set("Last-Modified", time.Unix(int64(peer.photo.Date), 0).UTC().Format(http.TimeFormat))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (h *handler) serveSet(w http.ResponseWriter, r *http.Request, pathKind string) {
	shortName := strings.TrimSpace(r.PathValue("shortName"))
	if !validShortNamePath(shortName) {
		http.NotFound(w, r)
		return
	}
	set, docs, found, err := h.stickerSets.ResolveStickerSet(r.Context(), domain.StickerSetRef{
		Kind:      domain.StickerSetRefByShortName,
		ShortName: shortName,
	})
	if err != nil {
		http.Error(w, "sticker set lookup failed", http.StatusInternalServerError)
		return
	}
	if !found || set.Deleted {
		http.NotFound(w, r)
		return
	}
	canonicalKind := linkKind(set)
	if canonicalKind != pathKind {
		http.Redirect(w, r, h.publicURL(canonicalKind, set.ShortName), http.StatusPermanentRedirect)
		return
	}
	count := set.Count
	if count == 0 {
		count = len(docs)
	}
	app := h.appURL(canonicalKind, "set", set.ShortName)
	data := pageData{
		AppName:      h.appName,
		Title:        fallbackTitle(set),
		KindLabel:    kindLabel(set),
		Subtitle:     fmt.Sprintf("@%s · %d %s", set.ShortName, count, itemNoun(set, count)),
		Description:  "This page opens the app so you can preview and install the set. Files are still fetched by the app through MTProto.",
		CanonicalURL: h.publicURL(canonicalKind, set.ShortName),
		AppURL:       template.URL(app),
		DownloadURL:  h.downloadURL,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60")
	if err := landingTemplate.Execute(w, data); err != nil {
		http.Error(w, "render sticker set page failed", http.StatusInternalServerError)
	}
}

func (h *handler) publicURL(kind, value string) string {
	return h.publicBaseURL + "/" + kind + "/" + url.PathEscape(value)
}

func (h *handler) publicUsernameURL(username string) string {
	return h.publicBaseURL + "/" + url.PathEscape(username)
}

func (h *handler) publicAvatarURL(username string, photoID int64) string {
	return h.publicBaseURL + "/_public/avatar/" + url.PathEscape(username) + "/" + strconv.FormatInt(photoID, 10)
}

func (h *handler) publicInviteURL(hash string) string {
	return h.publicBaseURL + "/+" + url.PathEscape(hash)
}

func (h *handler) publicInviteAvatarURL(hash string, photoID int64) string {
	return h.publicBaseURL + "/_public/invite-avatar/" + url.PathEscape(hash) + "/" + strconv.FormatInt(photoID, 10)
}

type publicPeerKind string

const (
	publicPeerUser       publicPeerKind = "user"
	publicPeerBot        publicPeerKind = "bot"
	publicPeerChannel    publicPeerKind = "channel"
	publicPeerSupergroup publicPeerKind = "supergroup"
)

type publicPeer struct {
	id          int64
	kind        publicPeerKind
	username    string
	title       string
	about       string
	verified    bool
	memberCount int
	photo       domain.Photo
	hasPhoto    bool
}

func (h *handler) resolvePublicPeer(ctx context.Context, username string) (publicPeer, bool, error) {
	var (
		u      domain.User
		userOK bool
		ch     domain.Channel
		chOK   bool
		err    error
	)
	if h.users != nil {
		u, userOK, err = h.users.ByUsername(ctx, username)
		if err != nil {
			return publicPeer{}, false, err
		}
	}
	if h.channels != nil {
		ch, chOK, err = h.channels.ResolvePublicChannelUsername(ctx, 0, username)
		if err != nil {
			return publicPeer{}, false, err
		}
	}
	if userOK && chOK {
		return publicPeer{}, false, fmt.Errorf("public username %q has multiple owners", username)
	}
	if userOK {
		return h.publicUserPeer(ctx, username, u)
	}
	if chOK {
		return h.publicChannelPeer(ctx, username, ch)
	}
	return publicPeer{}, false, nil
}

func (h *handler) publicUserPeer(ctx context.Context, requested string, u domain.User) (publicPeer, bool, error) {
	if u.ID == 0 || !strings.EqualFold(strings.TrimSpace(u.Username), requested) || !validUsernamePath(u.Username) {
		return publicPeer{}, false, fmt.Errorf("user username lookup returned invalid owner for %q", requested)
	}
	title := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if title == "" {
		title = u.Username
	}
	if err := validatePublicPeerText(title, u.About); err != nil {
		return publicPeer{}, false, fmt.Errorf("invalid public user %q: %w", u.Username, err)
	}
	about := strings.TrimSpace(u.About)
	photoKind := domain.ProfilePhotoKindProfile
	if !u.Bot && h.privacy != nil {
		visible, err := h.privacy.CanSeeAnonymous(ctx, u.ID, domain.PrivacyKeyAbout)
		if err != nil {
			return publicPeer{}, false, fmt.Errorf("evaluate public about privacy: %w", err)
		}
		if !visible {
			about = ""
		}
		visible, err = h.privacy.CanSeeAnonymous(ctx, u.ID, domain.PrivacyKeyProfilePhoto)
		if err != nil {
			return publicPeer{}, false, fmt.Errorf("evaluate public profile photo privacy: %w", err)
		}
		if !visible {
			photoKind = domain.ProfilePhotoKindFallback
		}
	}
	peer := publicPeer{
		id:       u.ID,
		kind:     publicPeerUser,
		username: u.Username,
		title:    title,
		about:    about,
		verified: u.Verified,
	}
	if u.Bot {
		peer.kind = publicPeerBot
	}
	if h.photos != nil {
		photo, found, err := h.photos.CurrentProfilePhotoKind(ctx, domain.PeerTypeUser, u.ID, photoKind)
		if err != nil {
			return publicPeer{}, false, fmt.Errorf("load public user photo: %w", err)
		}
		if found && photo.ID != 0 {
			if _, _, renderable := bestPublicPhotoSize(photo.Sizes); renderable {
				peer.photo, peer.hasPhoto = photo, true
			}
		}
	}
	return peer, true, nil
}

func (h *handler) publicChannelPeer(ctx context.Context, requested string, ch domain.Channel) (publicPeer, bool, error) {
	if !strings.EqualFold(strings.TrimSpace(ch.Username), requested) || !validUsernamePath(ch.Username) {
		return publicPeer{}, false, fmt.Errorf("channel username lookup returned invalid owner for %q", requested)
	}
	return h.channelToPublicPeer(ctx, ch)
}

// publicInviteChannelPeer projects a channel resolved via an invite hash.
// Unlike publicChannelPeer it does not require (or use) a public username —
// invite links exist precisely for channels/groups that may not have one.
func (h *handler) publicInviteChannelPeer(ctx context.Context, ch domain.Channel) (publicPeer, bool, error) {
	return h.channelToPublicPeer(ctx, ch)
}

func (h *handler) channelToPublicPeer(ctx context.Context, ch domain.Channel) (publicPeer, bool, error) {
	if ch.ID == 0 || ch.Deleted || ch.ParticipantsCount < 0 || (!ch.Broadcast && !ch.Megagroup) {
		return publicPeer{}, false, fmt.Errorf("channel %d is not eligible for a public projection", ch.ID)
	}
	if err := validatePublicPeerText(ch.Title, ch.About); err != nil {
		return publicPeer{}, false, fmt.Errorf("invalid public channel %q: %w", ch.Username, err)
	}
	peer := publicPeer{
		id:          ch.ID,
		kind:        publicPeerChannel,
		username:    ch.Username,
		title:       strings.TrimSpace(ch.Title),
		about:       strings.TrimSpace(ch.About),
		verified:    ch.Verified,
		memberCount: ch.ParticipantsCount,
	}
	if ch.Megagroup {
		peer.kind = publicPeerSupergroup
	}
	if h.photos != nil && ch.PhotoID != 0 {
		photo, found, err := h.photos.GetPhoto(ctx, ch.PhotoID)
		if err != nil {
			return publicPeer{}, false, fmt.Errorf("load public channel photo: %w", err)
		}
		if !found {
			return publicPeer{}, false, fmt.Errorf("channel %q current photo %d is missing", ch.Username, ch.PhotoID)
		}
		if photo.ID == ch.PhotoID {
			if _, _, renderable := bestPublicPhotoSize(photo.Sizes); renderable {
				peer.photo, peer.hasPhoto = photo, true
			}
		} else {
			return publicPeer{}, false, fmt.Errorf("channel %q photo lookup returned id %d, want %d", ch.Username, photo.ID, ch.PhotoID)
		}
	}
	return peer, true, nil
}

func validatePublicPeerText(title, about string) error {
	if strings.TrimSpace(title) == "" || utf8.RuneCountInString(title) > 256 {
		return fmt.Errorf("title is empty or too long")
	}
	if utf8.RuneCountInString(about) > 4096 {
		return fmt.Errorf("about is too long")
	}
	return nil
}

func (p publicPeer) buttonLabel() string {
	switch p.kind {
	case publicPeerBot:
		return "Start Bot"
	case publicPeerChannel:
		return "View Channel"
	case publicPeerSupergroup:
		return "View Group"
	default:
		return "Send Message"
	}
}

// inviteButtonLabel is used on invite-link preview pages, where the viewer is
// not yet a member — "Join", not "View" like the public-username page.
func (p publicPeer) inviteButtonLabel() string {
	if p.kind == publicPeerSupergroup {
		return "Join Group"
	}
	return "Join Channel"
}

func (p publicPeer) extra() string {
	switch p.kind {
	case publicPeerBot:
		return "bot"
	case publicPeerChannel:
		return groupedDecimal(p.memberCount) + " " + plural(p.memberCount, "subscriber", "subscribers")
	case publicPeerSupergroup:
		return groupedDecimal(p.memberCount) + " " + plural(p.memberCount, "member", "members")
	default:
		return ""
	}
}

func (p publicPeer) fallbackDescription(appName string) string {
	switch p.kind {
	case publicPeerBot:
		return "Open " + appName + " to start a chat with this bot."
	case publicPeerChannel:
		return "Open " + appName + " to view and join this channel."
	case publicPeerSupergroup:
		return "Open " + appName + " to view and join this group."
	default:
		return "Open " + appName + " to send a message to @" + p.username + "."
	}
}

func (p publicPeer) initials() string {
	words := strings.Fields(p.title)
	if len(words) == 0 {
		words = []string{p.username}
	}
	first := []rune(words[0])
	if len(first) == 0 {
		return "T"
	}
	out := []rune{first[0]}
	if len(words) > 1 {
		last := []rune(words[len(words)-1])
		if len(last) > 0 {
			out = append(out, last[0])
		}
	}
	return strings.ToUpper(string(out))
}

var publicAvatarGradients = [...][2]string{
	{"#FF885E", "#FF516A"},
	{"#FFCD6A", "#FFA85C"},
	{"#82B1FF", "#665FFF"},
	{"#A0DE7E", "#54CB68"},
	{"#53EDD6", "#28C9B7"},
	{"#72D5FD", "#2A9EF1"},
	{"#E0A2F3", "#D669ED"},
}

func avatarGradient(id int64) [2]string {
	if id < 0 {
		id = -id
	}
	return publicAvatarGradients[id%int64(len(publicAvatarGradients))]
}

func groupedDecimal(n int) string {
	if n < 0 {
		n = 0
	}
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + " " + s[i:]
	}
	return s
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func appInitial(name string) string {
	for _, r := range name {
		return strings.ToUpper(string(r))
	}
	return "T"
}

const (
	maxPublicLinkRawQuery = 2048
	maxPublicLinkParams   = 16
	maxPublicLinkValues   = 2
	maxPublicLinkValueLen = 512
)

func publicResolveQuery(raw string) (url.Values, bool) {
	if len(raw) > maxPublicLinkRawQuery {
		return nil, false
	}
	values, err := url.ParseQuery(raw)
	if err != nil || len(values) > maxPublicLinkParams {
		return nil, false
	}
	out := make(url.Values, len(values)+1)
	for key, items := range values {
		if strings.EqualFold(key, "domain") {
			continue
		}
		if !validPublicQueryKey(key) || len(items) > maxPublicLinkValues {
			return nil, false
		}
		for _, value := range items {
			if len(value) > maxPublicLinkValueLen || !utf8.ValidString(value) {
				return nil, false
			}
			out.Add(key, value)
		}
	}
	return out, true
}

func validPublicQueryKey(key string) bool {
	if key == "" || len(key) > 32 {
		return false
	}
	for _, r := range key {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func bestPublicPhotoSize(sizes []domain.PhotoSize) (domain.PhotoSize, []byte, bool) {
	var (
		best      domain.PhotoSize
		bestBytes []byte
		bestScore int64 = -1
	)
	for _, size := range sizes {
		if !validPhotoSizeType(size.Type) {
			continue
		}
		var inline []byte
		switch size.Kind {
		case domain.PhotoSizeKindCached:
			if len(size.Bytes) == 0 || len(size.Bytes) > maxPublicAvatarBytes {
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

func validPhotoSizeType(value string) bool {
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

func safePublicImageType(value string) bool {
	switch value {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func schemeURLValues(scheme, kind string, values url.Values) string {
	return (&url.URL{Scheme: scheme, Host: kind, RawQuery: values.Encode()}).String()
}

func publicWebAppURL(webBaseURL, legacyURL string) string {
	return strings.TrimRight(webBaseURL, "/") + "/#?tgaddr=" + url.QueryEscape(legacyURL)
}

func publicSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; font-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func validShortNamePath(shortName string) bool {
	if shortName == "" || len(shortName) > 64 {
		return false
	}
	for _, r := range shortName {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '.':
		default:
			return false
		}
	}
	return true
}

func validSlugPath(slug string) bool {
	return links.ValidChatlistSlug(slug)
}

func validUsernamePath(username string) bool {
	if username == "" || len(username) < 5 || len(username) > 32 {
		return false
	}
	for i, r := range username {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9', r == '_':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func validInviteHashPath(hash string) bool {
	if hash == "" || len(hash) > 64 {
		return false
	}
	for _, r := range hash {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

func linkKind(set domain.StickerSet) string {
	if set.Kind == domain.StickerSetKindEmoji || set.Emojis {
		return "addemoji"
	}
	return "addstickers"
}

func fallbackTitle(set domain.StickerSet) string {
	if title := strings.TrimSpace(set.Title); title != "" {
		return title
	}
	return set.ShortName
}

func kindLabel(set domain.StickerSet) string {
	switch {
	case set.Kind == domain.StickerSetKindEmoji || set.Emojis:
		return "custom emoji set"
	case set.Kind == domain.StickerSetKindMasks || set.Masks:
		return "mask set"
	default:
		return "sticker set"
	}
}

func itemNoun(set domain.StickerSet, count int) string {
	if set.Kind == domain.StickerSetKindEmoji || set.Emojis {
		if count == 1 {
			return "custom emoji"
		}
		return "custom emoji"
	}
	if count == 1 {
		return "sticker"
	}
	return "stickers"
}

func (h *handler) appURL(kind, key, value string) string {
	return schemeURL(h.appScheme, kind, key, value)
}

func schemeURL(scheme, kind, key, value string) string {
	return scheme + "://" + kind + "?" + key + "=" + url.QueryEscape(value)
}

type pageData struct {
	AppName      string
	Title        string
	KindLabel    string
	Subtitle     string
	Description  string
	CanonicalURL string
	DownloadURL  string
	AppURL       template.URL
}

type usernamePageData struct {
	AppName      string
	AppInitial   string
	Title        string
	Username     string
	Verified     bool
	Extra        string
	Description  string
	AboutText    string
	CanonicalURL string
	HomeURL      string
	DownloadURL  string
	PhotoURL     string
	Initials     string
	GradFrom     string
	GradTo       string
	ButtonLabel  string
	AppURL       template.URL
	WebURL       template.URL
}

func (h *handler) serveUsernameNotFound(w http.ResponseWriter, username string) {
	message := "This is not a valid public " + h.appName + " username."
	if username != "" {
		message = "@" + username + " is not an active public " + h.appName + " username."
	}
	h.serveNotFound(w, message)
}

func (h *handler) serveInviteNotFound(w http.ResponseWriter) {
	h.serveNotFound(w, "This invite link is not active anymore.")
}

func (h *handler) serveNotFound(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=30, must-revalidate")
	w.WriteHeader(http.StatusNotFound)
	if err := usernameNotFoundTemplate.Execute(w, struct {
		Message     string
		HomeURL     string
		AppName     string
		DownloadURL string
	}{Message: message, HomeURL: h.publicBaseURL + "/", AppName: h.appName, DownloadURL: h.downloadURL}); err != nil {
		h.logger.Error("Render public not-found page failed", zap.Error(err))
	}
}

// publicPageStyleSheet is the shared visual system for every public landing
// page (username profile, not-found, sticker/addlist): CSS variables (light
// theme + dark-scheme override), the card/avatar/button component classes,
// and the floating background orbs. Fonts are self-hosted (no Google Fonts
// CDN call from visitors' browsers) via the /_public/assets/fonts/ route.
const publicPageStyleSheet = `
  @font-face{font-family:'Plus Jakarta Sans';font-style:normal;font-weight:400;font-display:swap;src:url('/_public/assets/fonts/plus-jakarta-sans-400.woff2') format('woff2')}
  @font-face{font-family:'Plus Jakarta Sans';font-style:normal;font-weight:500;font-display:swap;src:url('/_public/assets/fonts/plus-jakarta-sans-500.woff2') format('woff2')}
  @font-face{font-family:'Plus Jakarta Sans';font-style:normal;font-weight:600;font-display:swap;src:url('/_public/assets/fonts/plus-jakarta-sans-600.woff2') format('woff2')}
  @font-face{font-family:'Plus Jakarta Sans';font-style:normal;font-weight:700;font-display:swap;src:url('/_public/assets/fonts/plus-jakarta-sans-700.woff2') format('woff2')}
  @font-face{font-family:'Plus Jakarta Sans';font-style:normal;font-weight:800;font-display:swap;src:url('/_public/assets/fonts/plus-jakarta-sans-800.woff2') format('woff2')}
  :root{
    --accent:#2563eb; --accent-bright:#38bdf8;
    --grad:linear-gradient(135deg,#38bdf8 0%,#2563eb 55%,#1e40af 100%);
    --bg:#f7f9fc; --surface:#ffffff; --surface-2:#f1f5f9; --line:#e2e8f0; --line-strong:#cbd5e1;
    --ink:#050508; --body:#3f4b5c; --muted:#64748b;
    --hero-glow:rgba(37,99,235,0.14); --hero-grid:rgba(5,5,8,0.04);
    --radius:18px; --radius-lg:24px;
    --shadow:0 28px 70px -36px rgba(5,5,8,0.35);
  }
  @media (prefers-color-scheme: dark){
    :root{
      --accent:#60a5fa; --accent-bright:#7dd3fc;
      --grad:linear-gradient(135deg,#7dd3fc 0%,#3b82f6 50%,#2563eb 100%);
      --bg:#08080e; --surface:#12121a; --surface-2:#181822; --line:#222228; --line-strong:#2e2e36;
      --ink:#f8fafc; --body:#a8b4c4; --muted:#7b8798;
      --hero-glow:rgba(59,130,246,0.25); --hero-grid:rgba(255,255,255,0.04);
      --shadow:0 32px 80px -40px rgba(0,0,0,0.75);
    }
  }
  *{box-sizing:border-box}
  html,body{height:100%}
  body{
    margin:0; color:var(--body); background:var(--bg);
    font-family:'Plus Jakarta Sans',-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;
    -webkit-font-smoothing:antialiased; line-height:1.65;
    min-height:100vh; display:flex; flex-direction:column;
    background-image:
      radial-gradient(900px 480px at 50% -8%, var(--hero-glow) 0%, rgba(0,0,0,0) 70%),
      linear-gradient(var(--hero-grid) 1px, transparent 1px),
      linear-gradient(90deg, var(--hero-grid) 1px, transparent 1px);
    background-size:auto, 44px 44px, 44px 44px;
    background-attachment:fixed;
  }
  h1,h2,h3{color:var(--ink); letter-spacing:-0.03em; font-weight:800; margin:0}
  a{color:var(--accent); text-decoration:none}
  header{
    display:flex; align-items:center; justify-content:space-between;
    padding:18px 26px; max-width:1100px; width:100%; margin:0 auto;
  }
  .brand{display:flex; align-items:center; gap:11px; font-weight:800; font-size:19px; color:var(--ink); letter-spacing:-0.02em}
  .brand img{width:34px; height:34px; border-radius:9px; display:block}
  .btn{
    display:inline-flex; align-items:center; justify-content:center; gap:8px;
    font-weight:700; font-size:0.92rem; padding:12px 20px; border-radius:11px;
    border:none; cursor:pointer; text-decoration:none; line-height:1; transition:transform .16s ease, box-shadow .16s ease, background .16s ease, border-color .16s ease, color .16s ease;
    font-family:inherit;
  }
  .btn:hover{transform:translateY(-1px)}
  .btn-primary{background:var(--grad); color:#fff; box-shadow:0 12px 28px -14px rgba(37,99,235,0.65); width:100%}
  .btn-primary:hover{box-shadow:0 16px 36px -14px rgba(37,99,235,0.75)}
  .btn-ghost{background:var(--surface); color:var(--ink); border:1px solid var(--line)}
  .btn-ghost:hover{border-color:var(--accent); color:var(--accent)}
  main{flex:1; display:flex; align-items:center; justify-content:center; padding:24px}
  .card{
    width:100%; max-width:430px; background:var(--surface);
    border:1px solid var(--line); border-radius:var(--radius-lg); padding:38px 30px 30px;
    text-align:center; box-shadow:var(--shadow);
  }
  .avatar-wrap{position:relative; width:124px; height:124px; margin:0 auto 20px}
  .avatar{position:absolute; inset:0; width:124px; height:124px; border-radius:50%; display:block}
  .avatar-photo{object-fit:cover; background:var(--surface-2)}
  .name{font-size:25px; font-weight:800; line-height:1.2; color:var(--ink); word-break:break-word; display:inline-flex; align-items:center; gap:8px; justify-content:center}
  .badge{width:20px; height:20px; flex:0 0 auto}
  .sub{color:var(--muted); font-size:15px; margin-top:6px; font-weight:500}
  .about{color:var(--body); font-size:15px; line-height:1.6; margin-top:18px; white-space:pre-wrap; word-break:break-word; text-align:center}
  .actions{margin-top:26px; display:grid; gap:10px}
  .scam{display:inline-block; margin-top:12px; color:#e5484d; border:1px solid rgba(229,72,77,0.25); background:rgba(229,72,77,0.08); font-size:12px; font-weight:700; letter-spacing:.4px; padding:4px 10px; border-radius:8px}
  .nf-emoji{font-size:60px; line-height:1; margin-bottom:14px}
  @media (max-width:480px){ .card{padding:30px 22px 24px} header{padding:16px 18px} }

  /* Floating background orbs. */
  .bg-orbs{position:fixed; inset:-60px; z-index:-1; pointer-events:none; transition:transform .3s ease-out; will-change:transform}
  .bg-orb{position:absolute; border-radius:50%; filter:blur(100px); pointer-events:none}
  .bg-orb--1{width:700px; height:700px; top:-15%; left:-10%; background:color-mix(in srgb,var(--accent-bright) 40%,transparent); animation:orbFloat1 20s ease-in-out infinite}
  .bg-orb--2{width:600px; height:600px; top:25%; right:-15%; background:color-mix(in srgb,var(--accent) 38%,transparent); animation:orbFloat2 24s ease-in-out infinite}
  .bg-orb--3{width:500px; height:500px; bottom:-15%; left:30%; background:color-mix(in srgb,var(--accent-bright) 30%,transparent); animation:orbFloat3 28s ease-in-out infinite}
  @keyframes orbFloat1{0%,100%{transform:translate(0,0) scale(1)}33%{transform:translate(60px,-40px) scale(1.08)}66%{transform:translate(-30px,30px) scale(.92)}}
  @keyframes orbFloat2{0%,100%{transform:translate(0,0) scale(1)}33%{transform:translate(-50px,-35px) scale(.93)}66%{transform:translate(45px,25px) scale(1.07)}}
  @keyframes orbFloat3{0%,100%{transform:translate(0,0) scale(1)}33%{transform:translate(40px,45px) scale(1.06)}66%{transform:translate(-55px,-25px) scale(.94)}}
  @media (max-width:720px){ .bg-orb{filter:blur(60px)} .bg-orb--1{width:350px;height:350px} .bg-orb--2{width:300px;height:300px} .bg-orb--3{width:250px;height:250px} }
  @media (prefers-reduced-motion: reduce){ .bg-orb{animation:none} }

  /* Drifting line-icon particles. */
  .bg-icons{position:fixed; inset:0; z-index:-1; overflow:hidden; pointer-events:none; color:var(--accent)}
  .bg-icon{position:absolute; left:0; top:0; will-change:transform}
`

// publicBgOrbsMarkup is the floating-orb + particle container markup, placed
// right after <body> on every public landing page.
const publicBgOrbsMarkup = `
  <div class="bg-orbs" aria-hidden="true">
    <div class="bg-orb bg-orb--1"></div>
    <div class="bg-orb bg-orb--2"></div>
    <div class="bg-orb bg-orb--3"></div>
  </div>
  <div class="bg-icons" aria-hidden="true"></div>`

// publicBgIconsScript drives the orb parallax and drifting line-icon
// particles. Self-contained vanilla JS, no external requests.
const publicBgIconsScript = `
  <script>
    (function(){
      var reduce = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
      var orbs = document.querySelector('.bg-orbs');
      var box = document.querySelector('.bg-icons');
      var mx = 0.5, my = 0.5;
      window.addEventListener('pointermove', function(e){
        mx = e.clientX / window.innerWidth;
        my = e.clientY / window.innerHeight;
        if (orbs) orbs.style.transform = 'translate(' + ((mx-0.5)*30) + 'px,' + ((my-0.5)*30) + 'px)';
      }, { passive: true });

      if (!box || reduce) return;

      var defs = [
        '<rect x="2.5" y="3.5" width="19" height="12" rx="2"/><path d="M8 20h8M12 15.5V20"/>',
        '<rect x="3.5" y="4" width="17" height="6" rx="2"/><rect x="3.5" y="14" width="17" height="6" rx="2"/><path d="M7 7h.01M7 17h.01"/>',
        '<rect x="3" y="5" width="18" height="14" rx="2.5"/><path d="M3 7l9 6.5L21 7"/>',
        '<circle cx="12" cy="12" r="9"/><path d="M3 12h18"/><path d="M12 3c2.6 2.5 4 5.7 4 9s-1.4 6.5-4 9c-2.6-2.5-4-5.7-4-9s1.4-6.5 4-9z"/>',
        '<rect x="7" y="3" width="10" height="18" rx="2.4"/><path d="M11 18h2"/>',
        '<path d="M8 6l-6 6 6 6M16 6l6 6-6 6"/>',
        '<path d="M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9z"/>',
        '<path d="M12 3l7 3v5c0 4.5-3 7.6-7 9-4-1.4-7-4.5-7-9V6l7-3z"/><path d="M8.8 12.2l2.1 2.1 4.3-4.3"/>',
        '<path d="M21.5 3.5l-19 7.5 5.5 2.5 3 5 3-2 5.5 5z"/><path d="M11 14l8.5-8.5"/>',
        '<rect x="4.5" y="11" width="15" height="9" rx="2.2"/><path d="M8 11V8a4 4 0 0 1 7.5-1.9"/><path d="M12 15v2"/>',
        '<path d="M12 3v12"/><path d="M7.5 10.5L12 15l4.5-4.5"/><path d="M5 20h14"/>',
        '<path d="M22 12h-4l-3 9L9 3l-3 9H2"/>',
        '<path d="M12 2l3.1 6.3L22 9.5l-5 4.9 1.2 7L12 17.3 5.8 21.4 7 14.4l-5-4.9 6.9-1.2z"/>',
        '<path d="M14.5 17.5L19 13l-4.5-4.5M9.5 6.5L5 11l4.5 4.5"/>',
        '<polyline points="4 17 10 11 4 5"/><line x1="12" y1="19" x2="20" y2="19"/>',
        '<path d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z"/><path d="M15 12h4M17 10v4"/>',
        '<rect x="2" y="3" width="20" height="14" rx="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/>',
        '<ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/><path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/>',
        '<path d="M21 11.5a8.38 8.38 0 01-.9 3.8 8.5 8.5 0 01-7.6 4.7 8.38 8.38 0 01-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 01-.9-3.8 8.5 8.5 0 014.7-7.6 8.38 8.38 0 013.8-.9h.5a8.48 8.48 0 018 8v.5z"/>',
        '<path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/>',
        '<rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="8.5" cy="8.5" r="1.5"/><path d="M21 15l-5-5L5 21"/>',
        '<circle cx="12" cy="12" r="10"/><path d="M12 6v12M6 12h12"/>'
      ];
      var SVGNS = 'http://www.w3.org/2000/svg';
      var ps = [];

      function build(){
        box.textContent = '';
        ps.length = 0;
        var w = window.innerWidth, h = window.innerHeight, margin = 60;
        var count = w < 720 ? 16 : 28;
        for (var i = 0; i < count; i++){
          var size = 22 + Math.random() * 30;
          var el = document.createElementNS(SVGNS, 'svg');
          el.setAttribute('class', 'bg-icon');
          el.setAttribute('viewBox', '0 0 24 24');
          el.setAttribute('fill', 'none');
          el.setAttribute('stroke', 'currentColor');
          el.setAttribute('stroke-width', '1.6');
          el.setAttribute('stroke-linecap', 'round');
          el.setAttribute('stroke-linejoin', 'round');
          el.style.width = size + 'px';
          el.style.height = size + 'px';
          el.innerHTML = defs[i % defs.length];
          var op = 0.18 + Math.random() * 0.12;
          el.style.opacity = op;
          box.appendChild(el);
          ps.push({
            x: margin + Math.random() * (w - margin * 2),
            y: margin + Math.random() * (h - margin * 2),
            vx: (Math.random() - 0.5) * 0.3,
            vy: (Math.random() - 0.5) * 0.3,
            size: size, rot: Math.random() * 360,
            rotSpeed: (Math.random() - 0.5) * 0.08, el: el
          });
        }
      }

      function tick(){
        var w = window.innerWidth, h = window.innerHeight;
        for (var i = 0; i < ps.length; i++){
          var a = ps[i];
          a.x += a.vx; a.y += a.vy; a.rot += a.rotSpeed;
          var half = a.size / 2;
          if (a.x < half){ a.x = half; a.vx *= -1; }
          if (a.x > w - half){ a.x = w - half; a.vx *= -1; }
          if (a.y < half){ a.y = half; a.vy *= -1; }
          if (a.y > h - half){ a.y = h - half; a.vy *= -1; }
          for (var j = i + 1; j < ps.length; j++){
            var b = ps[j];
            var dx = b.x - a.x, dy = b.y - a.y;
            var dist = Math.sqrt(dx * dx + dy * dy);
            var minD = (a.size + b.size) / 2 + 20;
            if (dist < minD && dist > 0.01){
              var f = (minD - dist) / minD * 0.02, nx = dx / dist, ny = dy / dist;
              a.vx -= nx * f; a.vy -= ny * f; b.vx += nx * f; b.vy += ny * f;
            }
          }
        }
        for (var k = 0; k < ps.length; k++){
          var p = ps[k];
          p.el.style.transform = 'translate(' + p.x + 'px,' + p.y + 'px) rotate(' + p.rot + 'deg)';
        }
        requestAnimationFrame(tick);
      }

      build();
      window.addEventListener('resize', build);
      requestAnimationFrame(tick);
    })();
  </script>`

var usernameLandingTemplate = template.Must(template.New("username-landing").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
  <meta name="theme-color" content="#2563eb">
  <title>{{.Title}}{{if .Username}} (@{{.Username}}){{end}} - {{.AppName}}</title>
  <meta name="description" content="{{.Description}}">
  <meta name="robots" content="index,follow,max-image-preview:large">
  <link rel="canonical" href="{{.CanonicalURL}}">
  <link rel="icon" type="image/png" href="/_public/assets/logo.png">
  <meta property="og:type" content="profile">
  <meta property="og:site_name" content="{{.AppName}}">
  <meta property="og:title" content="{{.Title}}">
  <meta property="og:description" content="{{.Description}}">
  <meta property="og:url" content="{{.CanonicalURL}}">
  {{if .PhotoURL}}<meta property="og:image" content="{{.PhotoURL}}">{{end}}
  <meta property="al:android:url" content="{{.AppURL}}">
  <meta property="al:ios:url" content="{{.AppURL}}">
  <meta name="twitter:card" content="summary">
  <meta name="twitter:title" content="{{.Title}}">
  <meta name="twitter:description" content="{{.Description}}">
  {{if .PhotoURL}}<meta name="twitter:image" content="{{.PhotoURL}}">{{end}}
  <style>` + publicPageStyleSheet + `
    .extra{color:var(--muted); font-size:14px; margin-top:2px}
  </style>
</head>
<body>` + publicBgOrbsMarkup + `
  <header>
    <a class="brand" href="{{.HomeURL}}" aria-label="{{.AppName}} home">
      <img src="/_public/assets/logo.png" alt="{{.AppName}}">
      <span>{{.AppName}}</span>
    </a>
    <a class="btn btn-ghost" href="{{.DownloadURL}}">Download</a>
  </header>
  <main>
    <div class="card">
      <div class="avatar-wrap">
        <svg class="avatar" viewBox="0 0 124 124" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
          <defs><linearGradient id="ag" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0" stop-color="{{.GradFrom}}"/><stop offset="1" stop-color="{{.GradTo}}"/>
          </linearGradient></defs>
          <circle cx="62" cy="62" r="62" fill="url(#ag)"/>
          <text x="62" y="64" text-anchor="middle" dominant-baseline="central"
                font-family="'Plus Jakarta Sans',Arial,sans-serif" font-size="46" font-weight="700" fill="#fff">{{.Initials}}</text>
        </svg>
        {{if .PhotoURL}}<img class="avatar avatar-photo" src="{{.PhotoURL}}" alt="" loading="eager" onerror="this.remove()">{{end}}
      </div>
      <div class="name">
        <span>{{.Title}}</span>
        {{if .Verified}}<svg class="badge" viewBox="0 0 24 24" fill="none" aria-label="Verified"><circle cx="12" cy="12" r="11" fill="#2563eb"/><path d="M7 12.5l3.2 3.2L17 9" stroke="#fff" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"/></svg>{{end}}
      </div>
      {{if .Username}}<div class="sub">@{{.Username}}</div>{{end}}
      {{if .Extra}}<div class="extra">{{.Extra}}</div>{{end}}
      {{if .AboutText}}<div class="about">{{.AboutText}}</div>{{end}}
      <div class="actions">
        <a class="btn btn-primary" href="{{.AppURL}}">{{.ButtonLabel}}</a>
        {{if .WebURL}}<a class="btn btn-ghost" href="{{.WebURL}}">Open in Web</a>{{end}}
      </div>
    </div>
  </main>` + publicBgIconsScript + `
</body>
</html>
`))

var usernameNotFoundTemplate = template.Must(template.New("username-not-found").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
  <meta name="theme-color" content="#2563eb">
  <meta name="robots" content="noindex,nofollow">
  <title>Nothing found - {{.AppName}}</title>
  <link rel="icon" type="image/png" href="/_public/assets/logo.png">
  <style>` + publicPageStyleSheet + `</style>
</head>
<body>` + publicBgOrbsMarkup + `
  <header>
    <a class="brand" href="{{.HomeURL}}" aria-label="{{.AppName}} home">
      <img src="/_public/assets/logo.png" alt="{{.AppName}}">
      <span>{{.AppName}}</span>
    </a>
    <a class="btn btn-ghost" href="{{.DownloadURL}}">Download</a>
  </header>
  <main>
    <div class="card">
      <div class="nf-emoji">🤔</div>
      <h2 style="font-size:22px">Nothing found</h2>
      <div class="about">{{.Message}}</div>
    </div>
  </main>` + publicBgIconsScript + `
</body>
</html>
`))

var landingTemplate = template.Must(template.New("landing").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
  <meta name="theme-color" content="#2563eb">
  <title>{{.Title}} - {{.AppName}}</title>
  <link rel="canonical" href="{{.CanonicalURL}}">
  <link rel="icon" type="image/png" href="/_public/assets/logo.png">
  <meta property="og:title" content="{{.Title}}">
  <meta property="og:description" content="{{.Description}}">
  <meta property="og:url" content="{{.CanonicalURL}}">
  <meta name="robots" content="noindex">
  <style>` + publicPageStyleSheet + `
    .kind{display:inline-block; color:var(--accent); background:color-mix(in srgb,var(--accent) 12%,transparent); font-size:12.5px; font-weight:700; letter-spacing:.3px; padding:4px 10px; border-radius:8px; margin-bottom:14px}
  </style>
</head>
<body>` + publicBgOrbsMarkup + `
  <header>
    <a class="brand" href="{{.CanonicalURL}}" aria-label="{{.AppName}}">
      <img src="/_public/assets/logo.png" alt="{{.AppName}}">
      <span>{{.AppName}}</span>
    </a>
    <a class="btn btn-ghost" href="{{.DownloadURL}}">Download</a>
  </header>
  <main>
    <div class="card">
      <div class="kind">{{.KindLabel}}</div>
      <h2 style="font-size:24px">{{.Title}}</h2>
      {{if .Subtitle}}<div class="sub">{{.Subtitle}}</div>{{end}}
      {{if .Description}}<div class="about">{{.Description}}</div>{{end}}
      <div class="actions"><a class="btn btn-primary" href="{{.AppURL}}">Open in {{.AppName}}</a></div>
    </div>
  </main>` + publicBgIconsScript + `
</body>
</html>
`))
