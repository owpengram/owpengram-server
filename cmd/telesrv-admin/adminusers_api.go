package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"telesrv/internal/admin"
)

// Operator accounts are written here rather than through callAdminAPI like the
// domain mutations are, on purpose. They are not a Telegram entity: they are
// the console's own authentication, and routing them through the domain API
// would mean the console cannot fix its own locked-out operators whenever that
// service is unreachable -- exactly when you need to. Reads already go straight
// to Postgres for the same reason, so this keeps one owner for one table.
//
// They do follow the panel's command convention: every mutation is a
// /api/actions/* route that takes a reason, runs as a dry run first and returns
// an admin.CommandResult. Granting somebody the run of the console deserves the
// same "here is what this will do, confirm it" step as freezing an account.

// requireAdminsManage is the single gate for every operator-account route, so
// none of them can be registered without it by accident.
func (s *server) requireAdminsManage(next http.Handler) http.Handler {
	return s.scopedRoute(permissionAdminsManage, next)
}

// errAdminUsernameTaken maps the unique-index violation to something the panel
// can show, without leaking the constraint name.
var errAdminUsernameTaken = errors.New("username is already taken")

// errLastManagerStanding guards against an edit that would leave nobody able to
// administer operators. The break-glass credential could still recover it, but
// that is a recovery path, not a thing to walk into by accident.
var errLastManagerStanding = errors.New("this would leave no enabled account able to manage operators")

// errUsernameReserved guards the break-glass name, which authentication
// resolves before the table is consulted.
var errUsernameReserved = errors.New("this username is reserved for the built-in operator")

// createAdminConsoleUser inserts a new operator. token_epoch starts at 1; there
// are no sessions to invalidate yet.
func (s *server) createAdminConsoleUser(ctx context.Context, username, password string, permissions []string, enabled bool) (AdminConsoleUser, error) {
	if err := validateAdminUsername(username); err != nil {
		return AdminConsoleUser{}, err
	}
	// authenticateLogin resolves this name to the environment credential before
	// it ever reaches the table, so a row by this name could never be logged
	// into. Refuse it rather than storing an account that silently does nothing.
	if strings.EqualFold(strings.TrimSpace(username), breakGlassUsername) {
		return AdminConsoleUser{}, errUsernameReserved
	}
	hash, err := hashAdminPassword(password)
	if err != nil {
		return AdminConsoleUser{}, err
	}
	permissions = normalisePermissions(permissions)

	var u AdminConsoleUser
	err = s.read.pool.QueryRow(ctx, `
INSERT INTO admin_console_users (username, password_hash, permissions, enabled)
VALUES ($1, $2, $3, $4)
RETURNING `+adminConsoleUserColumns,
		strings.TrimSpace(username), hash, permissions, enabled).
		Scan(&u.ID, &u.Username, &u.Permissions, &u.Enabled, &u.TokenEpoch,
			&u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return AdminConsoleUser{}, errAdminUsernameTaken
	}
	if err != nil {
		return AdminConsoleUser{}, fmt.Errorf("create admin console user: %w", err)
	}
	if u.Permissions == nil {
		u.Permissions = []string{}
	}
	return u, nil
}

// updateAdminConsoleUser changes permissions and/or enabled state.
//
// It deliberately does NOT move token_epoch. currentSessionPermissions re-reads
// this row on every request, so a narrowed permission set applies from the
// operator's next request and a disabled account is refused outright -- both
// without ending a session. Bumping the epoch here would only sign someone out
// mid-task to achieve what the re-read already achieves.
//
// A password change is different and does bump it: the password is not
// re-checked per request, so nothing else would retire the old sessions.
func (s *server) updateAdminConsoleUser(ctx context.Context, id int64, permissions []string, enabled bool) (AdminConsoleUser, error) {
	permissions = normalisePermissions(permissions)

	var u AdminConsoleUser
	err := s.read.pool.QueryRow(ctx, `
UPDATE admin_console_users
SET permissions = $2,
    enabled     = $3,
    updated_at  = now()
WHERE id = $1
RETURNING `+adminConsoleUserColumns,
		id, permissions, enabled).
		Scan(&u.ID, &u.Username, &u.Permissions, &u.Enabled, &u.TokenEpoch,
			&u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminConsoleUser{}, errAdminUserNotFound
	}
	if err != nil {
		return AdminConsoleUser{}, fmt.Errorf("update admin console user: %w", err)
	}
	if u.Permissions == nil {
		u.Permissions = []string{}
	}
	return u, nil
}

// setAdminConsoleUserPassword replaces the hash and bumps the epoch, so a
// password change signs out whoever was using the old one -- which is the
// point of changing it after a suspected compromise.
func (s *server) setAdminConsoleUserPassword(ctx context.Context, id int64, password string) error {
	hash, err := hashAdminPassword(password)
	if err != nil {
		return err
	}
	tag, err := s.read.pool.Exec(ctx, `
UPDATE admin_console_users
SET password_hash = $2, token_epoch = token_epoch + 1, updated_at = now()
WHERE id = $1`, id, hash)
	if err != nil {
		return fmt.Errorf("set admin console user password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errAdminUserNotFound
	}
	return nil
}

// normalisePermissions trims, de-duplicates and collapses to the wildcard when
// it is present, so "*" plus a list cannot be stored as something that reads
// narrower than it is.
func normalisePermissions(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if p == permissionAll {
			return []string{permissionAll}
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

// --- HTTP surface -----------------------------------------------------------

// adminUserActionRequest carries the panel's usual command envelope alongside
// the operator fields. ID is absent when creating.
type adminUserActionRequest struct {
	CommandID   string   `json:"command_id"`
	Reason      string   `json:"reason"`
	Confirm     bool     `json:"confirm"`
	ID          int64    `json:"id"`
	Username    string   `json:"username"`
	Password    string   `json:"password"`
	Permissions []string `json:"permissions"`
	Enabled     *bool    `json:"enabled"`
}

func (s *server) handleListAdminUsersAPI(w http.ResponseWriter, r *http.Request) {
	if s.read == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "read store is not configured")
		return
	}
	users, err := s.read.ListAdminConsoleUsers(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		// The built-in operator has no database row, so it would otherwise be
		// invisible here -- a list of who can sign in that omits the account
		// with the most rights is worse than no list. It is reported first and
		// flagged as system; the panel renders it read-only, and every mutation
		// below refuses it anyway.
		"system": map[string]any{
			"username":    breakGlassUsername,
			"permissions": newPanelPermissions(s.cfg.Permissions).List(),
			"enabled":     true,
			"system":      true,
		},
		"rows": users,
		// The vocabulary the panel offers when editing an account, so the list
		// of assignable rights lives in one place instead of being duplicated
		// in the frontend and drifting from what the routes actually check.
		"available_permissions": assignablePermissions(),
	})
}

// handleCreateAdminUserAPI runs as a dry run unless confirmed.
func (s *server) handleCreateAdminUserAPI(w http.ResponseWriter, r *http.Request) {
	var body adminUserActionRequest
	if !s.decodeAdminUserAction(w, r, &body) {
		return
	}
	meta := s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "admin-operator-create")
	enabled := body.Enabled == nil || *body.Enabled
	permissions := normalisePermissions(body.Permissions)

	// Validate on the dry run too, so "this will fail" is discovered before the
	// operator is asked to confirm rather than after.
	if err := validateAdminUsername(strings.TrimSpace(body.Username)); err != nil {
		writeCommandResultAPI(w, admin.CommandResult{CommandID: meta.CommandID, Action: "create-admin-operator"}, err)
		return
	}
	if strings.EqualFold(strings.TrimSpace(body.Username), breakGlassUsername) {
		writeCommandResultAPI(w, admin.CommandResult{CommandID: meta.CommandID, Action: "create-admin-operator"}, errUsernameReserved)
		return
	}
	if err := validateAdminPassword(body.Password); err != nil {
		writeCommandResultAPI(w, admin.CommandResult{CommandID: meta.CommandID, Action: "create-admin-operator"}, err)
		return
	}

	if meta.DryRun {
		writeJSON(w, http.StatusOK, admin.CommandResult{
			CommandID: meta.CommandID,
			Action:    "create-admin-operator",
			Status:    "ok",
			DryRun:    true,
			Message: fmt.Sprintf("Would create operator %q with %d permission(s), %s.",
				strings.TrimSpace(body.Username), len(permissions), enabledWord(enabled)),
			Details: map[string]any{
				"username":    strings.TrimSpace(body.Username),
				"permissions": permissions,
				"enabled":     enabled,
			},
		})
		return
	}

	user, err := s.createAdminConsoleUser(r.Context(), body.Username, body.Password, permissions, enabled)
	if err != nil {
		writeCommandResultAPI(w, admin.CommandResult{CommandID: meta.CommandID, Action: "create-admin-operator"}, err)
		return
	}
	writeJSON(w, http.StatusOK, admin.CommandResult{
		CommandID: meta.CommandID,
		Action:    "create-admin-operator",
		Status:    "ok",
		Message:   fmt.Sprintf("Created operator %q.", user.Username),
		Details:   map[string]any{"id": user.ID, "username": user.Username, "permissions": user.Permissions, "enabled": user.Enabled},
	})
}

// handleUpdateAdminUserAPI changes rights and/or enabled state, dry run first.
func (s *server) handleUpdateAdminUserAPI(w http.ResponseWriter, r *http.Request) {
	var body adminUserActionRequest
	if !s.decodeAdminUserAction(w, r, &body) {
		return
	}
	meta := s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "admin-operator-access")
	const action = "set-admin-operator-access"
	enabled := body.Enabled == nil || *body.Enabled
	permissions := normalisePermissions(body.Permissions)

	if body.ID <= 0 {
		writeCommandResultAPI(w, admin.CommandResult{CommandID: meta.CommandID, Action: action}, errAdminUserNotFound)
		return
	}
	if err := s.guardManagerRemoval(r.Context(), body.ID, permissions, enabled); err != nil {
		writeCommandResultAPI(w, admin.CommandResult{CommandID: meta.CommandID, Action: action}, err)
		return
	}

	if meta.DryRun {
		writeJSON(w, http.StatusOK, admin.CommandResult{
			CommandID: meta.CommandID,
			Action:    action,
			Status:    "ok",
			DryRun:    true,
			Message: fmt.Sprintf("Would set operator #%d to %d permission(s), %s. Takes effect on their next request.",
				body.ID, len(permissions), enabledWord(enabled)),
			Details: map[string]any{"id": body.ID, "permissions": permissions, "enabled": enabled},
		})
		return
	}

	user, err := s.updateAdminConsoleUser(r.Context(), body.ID, permissions, enabled)
	if err != nil {
		writeCommandResultAPI(w, admin.CommandResult{CommandID: meta.CommandID, Action: action}, err)
		return
	}
	writeJSON(w, http.StatusOK, admin.CommandResult{
		CommandID: meta.CommandID,
		Action:    action,
		Status:    "ok",
		Message:   fmt.Sprintf("Updated %q. The new access applies from their next request.", user.Username),
		Details:   map[string]any{"id": user.ID, "username": user.Username, "permissions": user.Permissions, "enabled": user.Enabled},
	})
}

// handleSetAdminUserPasswordAPI resets a password, dry run first.
func (s *server) handleSetAdminUserPasswordAPI(w http.ResponseWriter, r *http.Request) {
	var body adminUserActionRequest
	if !s.decodeAdminUserAction(w, r, &body) {
		return
	}
	meta := s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "admin-operator-password")
	const action = "set-admin-operator-password"

	if body.ID <= 0 {
		writeCommandResultAPI(w, admin.CommandResult{CommandID: meta.CommandID, Action: action}, errAdminUserNotFound)
		return
	}
	if err := validateAdminPassword(body.Password); err != nil {
		writeCommandResultAPI(w, admin.CommandResult{CommandID: meta.CommandID, Action: action}, err)
		return
	}

	if meta.DryRun {
		writeJSON(w, http.StatusOK, admin.CommandResult{
			CommandID: meta.CommandID,
			Action:    action,
			Status:    "ok",
			DryRun:    true,
			Message:   fmt.Sprintf("Would set a new password for operator #%d. Their existing sessions would be signed out.", body.ID),
			// The password itself is never echoed, not even back to the
			// operator who just typed it.
			Details: map[string]any{"id": body.ID},
		})
		return
	}

	if err := s.setAdminConsoleUserPassword(r.Context(), body.ID, body.Password); err != nil {
		writeCommandResultAPI(w, admin.CommandResult{CommandID: meta.CommandID, Action: action}, err)
		return
	}
	writeJSON(w, http.StatusOK, admin.CommandResult{
		CommandID: meta.CommandID,
		Action:    action,
		Status:    "ok",
		Message:   fmt.Sprintf("Password changed for operator #%d. Their existing sessions are signed out.", body.ID),
		Details:   map[string]any{"id": body.ID},
	})
}

// decodeAdminUserAction shares the store check, body decode and reason
// requirement across the three mutations.
func (s *server) decodeAdminUserAction(w http.ResponseWriter, r *http.Request, body *adminUserActionRequest) bool {
	if s.read == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "read store is not configured")
		return false
	}
	if err := decodeJSON(r, body); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return false
	}
	if strings.TrimSpace(body.Reason) == "" {
		writeAPIError(w, http.StatusBadRequest, "a reason is required")
		return false
	}
	return true
}

func enabledWord(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

// guardManagerRemoval refuses an edit that would leave nobody able to manage
// operators. Counted over the other accounts, so demoting or disabling the
// only remaining manager is what trips it.
func (s *server) guardManagerRemoval(ctx context.Context, id int64, permissions []string, enabled bool) error {
	stillManages := enabled && newPanelPermissions(permissions).Has(permissionAdminsManage)
	if stillManages {
		return nil
	}
	others, err := s.read.CountEnabledAdminConsoleUsersWith(ctx, permissionAdminsManage, id)
	if err != nil {
		return err
	}
	if others == 0 {
		return errLastManagerStanding
	}
	return nil
}
