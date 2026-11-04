package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// Private content -- an account's chats, media, stories and gifts, a
// channel's posts and comments -- is the most sensitive thing the console
// can show. Holding messages.read is not enough to see it: every time an
// operator opens it they type their own console password again, and that
// opening is written to the audit log against the account or channel. Each
// conversation opened afterwards is audited on its own too.
//
// A successful re-check returns a view grant: a short-lived token, signed
// with the session key, bound to this session and to one account or channel.
// The panel keeps it in memory only, so leaving the page (or reloading)
// means typing the password again.

const (
	viewGrantTTL       = 10 * time.Minute
	viewReasonMinRunes = 15
	viewReasonMaxRunes = 500
	viewGrantHeader    = "X-View-Grant"
	viewUnlockMaxFails = 5
	viewUnlockLockout  = 10 * time.Minute
)

type claimsKey struct{}

func claimsFromContext(ctx context.Context) (sessionClaims, bool) {
	claims, ok := ctx.Value(claimsKey{}).(sessionClaims)
	return claims, ok
}

type viewGrant struct {
	Actor string `json:"a"`
	Nonce string `json:"n"`
	Kind  string `json:"k"` // user, channel
	ID    int64  `json:"i"`
	Exp   int64  `json:"e"`
	// Unlock is the command id of the audited unlock this grant came from.
	// Every later read is checked against that row and repeats its reason.
	Unlock string `json:"u"`
}

// viewPurposes are the only reasons an operator may give for opening someone's
// private content. "other" still needs the written explanation like the rest.
var viewPurposes = map[string]string{
	"report":   "Investigating a report",
	"legal":    "Legal or law-enforcement request",
	"owner":    "Request from the account owner",
	"security": "Security incident or abuse",
	"other":    "Other",
}

func (s *server) signViewGrant(g viewGrant) (string, error) {
	body, err := json.Marshal(g)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(body)
	return payload + "." + base64.RawURLEncoding.EncodeToString(s.viewGrantMAC(payload)), nil
}

func (s *server) viewGrantMAC(payload string) []byte {
	mac := hmac.New(sha256.New, s.cfg.SessionKey)
	mac.Write([]byte("view-grant:"))
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}

// viewGrantFromRequest returns the request's grant if it is genuine, unexpired
// and was issued to this very session.
func (s *server) viewGrantFromRequest(r *http.Request) (viewGrant, bool) {
	raw := r.Header.Get(viewGrantHeader)
	if raw == "" {
		raw = r.URL.Query().Get("grant")
	}
	payload, sig, ok := strings.Cut(raw, ".")
	if !ok {
		return viewGrant{}, false
	}
	want := s.viewGrantMAC(payload)
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(want, got) {
		return viewGrant{}, false
	}
	body, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return viewGrant{}, false
	}
	var g viewGrant
	if json.Unmarshal(body, &g) != nil || time.Now().Unix() > g.Exp || g.Unlock == "" {
		return viewGrant{}, false
	}
	claims, ok := claimsFromContext(r.Context())
	if !ok || claims.Actor != g.Actor || subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(g.Nonce)) != 1 {
		return viewGrant{}, false
	}
	return g, true
}

// requireViewGrant answers 403 "VIEW_LOCKED" unless the request carries a
// grant for the target named by the path's {id} (kind "" accepts any grant
// of this session, for the media route).
func (s *server) requireViewGrant(kind string, next func(http.ResponseWriter, *http.Request, viewGrant)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g, ok := s.viewGrantFromRequest(r)
		if ok && kind != "" {
			id, err := parseInt64(r.PathValue("id"))
			ok = err == nil && g.Kind == kind && g.ID == id
		}
		if !ok {
			writeAPIError(w, http.StatusForbidden, "VIEW_LOCKED")
			return
		}
		next(w, r, g)
	})
}

// unlockLimiter slows down password guessing through the re-check: a few
// misses lock that operator out of it for a while.
type unlockLimiter struct {
	mu    sync.Mutex
	fails map[string]int
	until map[string]time.Time
}

var viewUnlockLimiter = &unlockLimiter{fails: map[string]int{}, until: map[string]time.Time{}}

func (l *unlockLimiter) locked(actor string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return time.Now().Before(l.until[actor])
}

func (l *unlockLimiter) record(actor string, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ok {
		delete(l.fails, actor)
		return
	}
	l.fails[actor]++
	if l.fails[actor] >= viewUnlockMaxFails {
		l.fails[actor] = 0
		l.until[actor] = time.Now().Add(viewUnlockLockout)
	}
}

// checkOperatorPassword verifies the signed-in operator's own password.
func (s *server) checkOperatorPassword(ctx context.Context, claims sessionClaims, password string) bool {
	if password == "" {
		return false
	}
	if claims.UserID == 0 {
		return strings.EqualFold(claims.Actor, breakGlassUsername) && s.validSecret(password)
	}
	if s.read == nil {
		return false
	}
	cred, err := s.read.AdminConsoleCredentialByUsername(ctx, claims.Actor)
	if err != nil {
		_ = bcrypt.CompareHashAndPassword([]byte(dummyBcryptHash), []byte(password))
		return false
	}
	return cred.ID == claims.UserID && cred.Enabled && bcrypt.CompareHashAndPassword([]byte(cred.PasswordHash), []byte(password)) == nil
}

// requestOrigin is who-asked metadata every private-content audit row carries.
func requestOrigin(r *http.Request) map[string]any {
	out := map[string]any{"remote_addr": r.RemoteAddr, "user_agent": r.UserAgent()}
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		out["forwarded_for"] = fwd
	}
	return out
}

// recordViewAudit writes one completed (or failed) row to the audit log and
// returns its command id.
func (s *server) recordViewAudit(ctx context.Context, actor, action, kind string, id int64, reason string, details map[string]any, failure string) (string, error) {
	if s.read == nil {
		return "", errors.New("read store is not configured")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Viewed private content"
	}
	if len(reason) > 1000 {
		reason = reason[:1000]
	}
	if details == nil {
		details = map[string]any{}
	}
	request, err := json.Marshal(details)
	if err != nil {
		return "", err
	}
	status := "completed"
	if failure != "" {
		status = "failed"
	}
	var targetUser, peerID int64
	peerType := ""
	if kind == "user" {
		targetUser = id
	} else {
		peerType, peerID = kind, id
	}
	commandID := newCommandID("view")
	tx, err := s.read.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
INSERT INTO admin_commands (command_id, actor, action, target_user_id, target_peer_type, target_peer_id,
	dry_run, reason, request, result, status, error, created_at, completed_at)
VALUES ($1, $2, $3, $4, $5, $6, false, $7, $8::jsonb, '{}'::jsonb, $9, $10, now(), now())`,
		commandID, actor, action, targetUser, peerType, peerID, reason, string(request), status, failure); err != nil {
		return "", fmt.Errorf("record view command: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO admin_audit_logs (
	command_id, actor, action, target_user_id, target_peer_type, target_peer_id,
	dry_run, reason, request, result, status, error, created_at
)
SELECT command_id, actor, action, target_user_id, target_peer_type, target_peer_id,
	dry_run, reason, request, result, status, error, now()
FROM admin_commands WHERE command_id = $1`, commandID); err != nil {
		return "", fmt.Errorf("record view audit: %w", err)
	}
	return commandID, tx.Commit(ctx)
}

// unlockReason returns the reason of the audited unlock a grant came from, or
// "" when that row does not exist (a grant must trace back to one).
func (s *server) unlockReason(ctx context.Context, g viewGrant) string {
	if s.read == nil || g.Unlock == "" {
		return ""
	}
	var reason string
	if err := s.read.pool.QueryRow(ctx, `
SELECT reason FROM admin_audit_logs
WHERE command_id = $1 AND actor = $2 AND status = 'completed' AND action LIKE 'private.unlock_%'`,
		g.Unlock, g.Actor).Scan(&reason); err != nil {
		return ""
	}
	return reason
}

type viewUnlockRequest struct {
	Password    string `json:"password"`
	Kind        string `json:"kind"`
	ID          int64  `json:"id"`
	Purpose     string `json:"purpose"`
	Reason      string `json:"reason"`
	Acknowledge bool   `json:"acknowledge"`
	// The operator has to confirm, separately, that they understand the
	// consequences and that the person will be told.
	AckConsequences bool `json:"acknowledge_consequences"`
	AckNotified     bool `json:"acknowledge_notified"`
}

// validateViewReason enforces a real, written justification before the
// password is even looked at: private messages are never opened "just
// because".
func validateViewReason(purpose, reason string, acknowledge, consequences, notified bool) (string, error) {
	label, ok := viewPurposes[purpose]
	if !ok {
		return "", errors.New("Choose why you need access.")
	}
	reason = strings.Join(strings.Fields(reason), " ")
	n := utf8.RuneCountInString(reason)
	if n < viewReasonMinRunes {
		return "", fmt.Errorf("Explain the reason in at least %d characters.", viewReasonMinRunes)
	}
	if n > viewReasonMaxRunes {
		return "", fmt.Errorf("Keep the reason under %d characters.", viewReasonMaxRunes)
	}
	if !acknowledge || !consequences || !notified {
		return "", errors.New("Tick every confirmation first.")
	}
	return label + ": " + reason, nil
}

func (s *server) handleViewUnlockAPI(w http.ResponseWriter, r *http.Request) {
	var body viewUnlockRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if (body.Kind != "user" && body.Kind != "channel") || body.ID <= 0 {
		writeAPIError(w, http.StatusBadRequest, "kind must be user or channel, with an id")
		return
	}
	claims, ok := claimsFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	action := "private.unlock_" + body.Kind
	auditReason, err := validateViewReason(body.Purpose, body.Reason, body.Acknowledge, body.AckConsequences, body.AckNotified)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	details := requestOrigin(r)
	details["purpose"] = body.Purpose
	if viewUnlockLimiter.locked(claims.Actor) {
		writeAPIError(w, http.StatusTooManyRequests, "Too many wrong passwords. Try again in a few minutes.")
		return
	}
	if !s.checkOperatorPassword(r.Context(), claims, body.Password) {
		viewUnlockLimiter.record(claims.Actor, false)
		_, _ = s.recordViewAudit(r.Context(), claims.Actor, action, body.Kind, body.ID, auditReason, details, "wrong password")
		writeAPIError(w, http.StatusForbidden, "Wrong password.")
		return
	}
	viewUnlockLimiter.record(claims.Actor, true)
	exp := time.Now().Add(viewGrantTTL)
	details["expires_at"] = exp.UTC()
	// The person is told before anything opens. If the notice can't be
	// delivered, nothing is opened either.
	notified, err := s.notifyPrivateView(r.Context(), body.Kind, body.ID, auditReason)
	if err != nil {
		_, _ = s.recordViewAudit(r.Context(), claims.Actor, action, body.Kind, body.ID, auditReason, details, "could not notify the owner: "+err.Error())
		writeAPIError(w, http.StatusBadGateway, "Could not notify the owner, so access was not opened: "+err.Error())
		return
	}
	details["notified_user_id"] = notified
	unlockID, err := s.recordViewAudit(r.Context(), claims.Actor, action, body.Kind, body.ID, auditReason, details, "")
	if err != nil {
		// No audit row, no access.
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	grant, err := s.signViewGrant(viewGrant{Actor: claims.Actor, Nonce: claims.Nonce, Kind: body.Kind, ID: body.ID, Exp: exp.Unix(), Unlock: unlockID})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"grant": grant, "expires_at": exp.UTC(), "reason": auditReason})
}

// notifyPrivateView tells the owner -- the account itself, or a channel's
// creator -- from the system account that their private content is being
// opened, and why. It returns who was told (0 when there is nobody to tell,
// e.g. a channel whose creator account is gone).
func (s *server) notifyPrivateView(ctx context.Context, kind string, id int64, reason string) (int64, error) {
	stamp := time.Now().UTC().Format("2006-01-02 15:04 UTC")
	recipient := id
	what := "your account's private content (chats, media, posts and gifts)"
	if kind == "channel" {
		if s.read == nil {
			return 0, errors.New("read store is not configured")
		}
		var title string
		var broadcast, megagroup bool
		if err := s.read.pool.QueryRow(ctx, `SELECT creator_user_id, title, broadcast, megagroup FROM channels WHERE id = $1`, id).
			Scan(&recipient, &title, &broadcast, &megagroup); err != nil {
			return 0, fmt.Errorf("look up channel: %w", err)
		}
		noun := "group"
		if broadcast && !megagroup {
			noun = "channel"
		}
		what = fmt.Sprintf("the messages and media of your %s \u00ab%s\u00bb", noun, title)
		if recipient <= 0 {
			return 0, nil
		}
	}
	text := fmt.Sprintf("A server administrator has opened %s.\n\nReason: %s\nTime: %s\n\nThis access is recorded in the server's audit log and closes automatically within %d minutes.",
		what, reason, stamp, int(viewGrantTTL/time.Minute))
	var b [8]byte
	_, _ = rand.Read(b[:])
	payload, err := json.Marshal(map[string]any{"user_id": recipient, "message": text, "random_id": int64(binary.LittleEndian.Uint64(b[:]) >> 1)})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.AdminAPIURL+"/v1/system-notice", bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.cfg.AdminAPIToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return 0, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return recipient, nil
}

// auditChatOpen writes the per-conversation row when a chat's first page is
// read (later pages of the same chat are scrolling, not a new opening).
func (s *server) auditChatOpen(r *http.Request, g viewGrant, details map[string]any) bool {
	if r.URL.Query().Get("before_date") != "" {
		return true
	}
	// The row repeats the unlock's reason and links to it, so every chat read
	// traces back to the justification that allowed it.
	reason := s.unlockReason(r.Context(), g)
	if reason == "" {
		return false
	}
	for k, v := range requestOrigin(r) {
		details[k] = v
	}
	details["unlock_command_id"] = g.Unlock
	_, err := s.recordViewAudit(r.Context(), g.Actor, "private.read_chat", g.Kind, g.ID, reason, details, "")
	return err == nil
}

func chatCursor(r *http.Request) (int64, int) {
	beforeDate, _ := parseInt64(r.URL.Query().Get("before_date"))
	beforeID, _ := parseInt(r.URL.Query().Get("before_id"))
	return beforeDate, beforeID
}

func (s *server) handleUserChatsAPI(w http.ResponseWriter, r *http.Request, g viewGrant) {
	chats, err := s.read.UserChats(r.Context(), g.ID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chats": chats})
}

func (s *server) handleUserChatMessagesAPI(w http.ResponseWriter, r *http.Request, g viewGrant) {
	kind := r.PathValue("kind")
	peerID, err := parseInt64(r.PathValue("peer"))
	if err != nil || peerID <= 0 || (kind != "user" && kind != "channel") {
		writeAPIError(w, http.StatusBadRequest, "invalid chat")
		return
	}
	beforeDate, beforeID := chatCursor(r)
	var page ChatPage
	if kind == "user" {
		page, err = s.read.PrivateChatPage(r.Context(), g.ID, peerID, beforeDate, beforeID)
	} else {
		member, merr := s.read.IsChannelMember(r.Context(), g.ID, peerID)
		if merr != nil {
			writeAPIError(w, http.StatusInternalServerError, merr.Error())
			return
		}
		if !member {
			writeAPIError(w, http.StatusNotFound, "this account is not in that chat")
			return
		}
		page, err = s.read.ChannelChatPage(r.Context(), peerID, 0, beforeDate, beforeID)
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !s.auditChatOpen(r, g, map[string]any{"chat_type": kind, "chat_id": peerID}) {
		writeAPIError(w, http.StatusInternalServerError, "could not write the audit log")
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *server) handleUserMediaAPI(w http.ResponseWriter, r *http.Request, g viewGrant) {
	beforeDate, beforeID := chatCursor(r)
	items, hasMore, err := s.read.UserMediaPage(r.Context(), g.ID, beforeDate, beforeID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "has_more": hasMore})
}

func (s *server) handlePeerProfileAPI(w http.ResponseWriter, r *http.Request, g viewGrant) {
	content, err := s.read.PeerProfileContent(r.Context(), g.Kind, g.ID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, content)
}

func (s *server) handleChannelMessagesAPI(w http.ResponseWriter, r *http.Request, g viewGrant) {
	beforeDate, beforeID := chatCursor(r)
	page, err := s.read.ChannelChatPage(r.Context(), g.ID, 0, beforeDate, beforeID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !s.auditChatOpen(r, g, map[string]any{"chat_type": "channel", "chat_id": g.ID}) {
		writeAPIError(w, http.StatusInternalServerError, "could not write the audit log")
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// handleChannelCommentsAPI reads the comment thread under one post: the
// replies to it in the channel's linked discussion group.
func (s *server) handleChannelCommentsAPI(w http.ResponseWriter, r *http.Request, g viewGrant) {
	postID, err := parseInt(r.URL.Query().Get("post_id"))
	if err != nil || postID <= 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid post_id")
		return
	}
	groupID, top, err := s.read.PostDiscussion(r.Context(), g.ID, postID)
	if err != nil || groupID <= 0 || top <= 0 {
		writeAPIError(w, http.StatusNotFound, "this post has no comments")
		return
	}
	beforeDate, beforeID := chatCursor(r)
	page, err := s.read.ChannelChatPage(r.Context(), groupID, top, beforeDate, beforeID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !s.auditChatOpen(r, g, map[string]any{"chat_type": "comments", "post_id": postID, "group_id": groupID}) {
		writeAPIError(w, http.StatusInternalServerError, "could not write the audit log")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"group_id": groupID, "page": page})
}

var privateMediaKeyPattern = regexp.MustCompile(`^(photo:[0-9]{1,20}:[A-Za-z0-9_]{1,8}|doc:[0-9]{1,20}(:[A-Za-z0-9_]{1,8})?)$`)

// viewMediaInline lists what the viewer may render in the page. Anything else
// is handed out as a download, never interpreted by the browser.
var viewMediaInline = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true,
	"video/mp4": true, "video/webm": true,
	"audio/mpeg": true, "audio/ogg": true, "audio/wave": true, "audio/wav": true, "audio/mp4": true,
}

// handleViewMediaAPI streams one file referenced by a message, story or
// profile photo, for a session holding any view grant.
func (s *server) handleViewMediaAPI(w http.ResponseWriter, r *http.Request, _ viewGrant) {
	s.streamStoredFile(w, r, r.URL.Query().Get("key"))
}

// streamStoredFile proxies one photo/document file from the main server's
// storage, serving only safe types inline. Callers decide whether the key
// may be seen at all.
func (s *server) streamStoredFile(w http.ResponseWriter, r *http.Request, key string) {
	if !privateMediaKeyPattern.MatchString(key) {
		writeAPIError(w, http.StatusBadRequest, "invalid key")
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, s.cfg.AdminAPIURL+"/v1/private-media?key="+key, nil)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.AdminAPIToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		writeAPIError(w, resp.StatusCode, http.StatusText(resp.StatusCode))
		return
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 49<<20))
	if err != nil || len(data) == 0 {
		writeAPIError(w, http.StatusBadGateway, "could not read the file")
		return
	}
	contentType := http.DetectContentType(data)
	// Animated stickers are gzip'd Lottie; the viewer plays the JSON.
	if contentType == "application/x-gzip" && r.URL.Query().Get("lottie") == "1" {
		if zr, err := gzip.NewReader(bytes.NewReader(data)); err == nil {
			if plain, err := io.ReadAll(io.LimitReader(zr, 16<<20)); err == nil {
				data, contentType = plain, "application/json"
			}
			zr.Close()
		}
	}
	if contentType == "application/ogg" {
		contentType = "audio/ogg"
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if !viewMediaInline[contentType] && contentType != "application/json" {
		contentType = "application/octet-stream"
		w.Header().Set("Content-Disposition", "attachment")
	}
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}

// requireViewGrantForQuery guards the older message routes that name their
// target in the query string instead of the path.
func (s *server) requireViewGrantForQuery(kind, param string, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g, ok := s.viewGrantFromRequest(r)
		if ok {
			id, err := strconv.ParseInt(r.URL.Query().Get(param), 10, 64)
			ok = err == nil && g.Kind == kind && g.ID == id
		}
		if !ok {
			writeAPIError(w, http.StatusForbidden, "VIEW_LOCKED")
			return
		}
		next(w, r)
	})
}

func (s *server) registerPrivateViewRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/private/unlock", s.scopedRoute(permissionMessagesRead, http.HandlerFunc(s.handleViewUnlockAPI)))
	mux.Handle("GET /api/private/users/{id}/chats", s.scopedRoute(permissionMessagesRead, s.requireViewGrant("user", s.handleUserChatsAPI)))
	mux.Handle("GET /api/private/users/{id}/chats/{kind}/{peer}", s.scopedRoute(permissionMessagesRead, s.requireViewGrant("user", s.handleUserChatMessagesAPI)))
	mux.Handle("GET /api/private/users/{id}/media", s.scopedRoute(permissionMessagesRead, s.requireViewGrant("user", s.handleUserMediaAPI)))
	mux.Handle("GET /api/private/users/{id}/profile", s.scopedRoute(permissionMessagesRead, s.requireViewGrant("user", s.handlePeerProfileAPI)))
	mux.Handle("GET /api/private/channels/{id}/messages", s.scopedRoute(permissionMessagesRead, s.requireViewGrant("channel", s.handleChannelMessagesAPI)))
	mux.Handle("GET /api/private/channels/{id}/comments", s.scopedRoute(permissionMessagesRead, s.requireViewGrant("channel", s.handleChannelCommentsAPI)))
	mux.Handle("GET /api/private/channels/{id}/profile", s.scopedRoute(permissionMessagesRead, s.requireViewGrant("channel", s.handlePeerProfileAPI)))
	mux.Handle("GET /api/private/media", s.scopedRoute(permissionMessagesRead, s.requireViewGrant("", s.handleViewMediaAPI)))
}
