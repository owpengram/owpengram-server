// Package identity stores the admin-editable server name/description/icon
// shown to clients over the same-port HTTP endpoints in internal/mtprotoedge
// (/owpengram/server-info, /owpengram/server-icon). It is deliberately not
// part of internal/config's Config: config is loaded once at process start
// from .env, while identity is meant to be edited from the admin web panel
// and take effect immediately, with no server restart -- so it lives as
// plain files on disk, read fresh on every request instead of cached in
// memory.
package identity

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	metaFileName = "identity.json"
	iconBaseName = "icon"
	// setupPendingFileName marks an install as not yet through the
	// first-run wizard. Deliberately a sentinel file next to identity.json,
	// not a field inside it: identity.json's own content changes *during*
	// the wizard -- the Identity step saves a name well before Done is ever
	// reached -- so a signal derived from that content (e.g. "name is set")
	// flips to "done" the moment that one step is saved, not when the
	// wizard actually finishes. This file is created once, by quickstart's
	// bootstrap_env() the moment it creates a fresh .env (see
	// tui-panel/server-panel.py), and removed once, by MarkSetupComplete --
	// nothing in between (including a server restart mid-wizard) touches
	// it, so "still pending" survives every step until Done really is
	// reached.
	setupPendingFileName = ".setup_pending"
	// passwordTemporaryFileName holds the exact value of the password
	// quickstart's bootstrap_env() generated for the very first login on a
	// fresh install (see tui-panel/server-panel.py) -- created alongside
	// setupPendingFileName, never on its own. Storing the value itself
	// (rather than just the file's existence) is what lets
	// TemporaryPasswordMatches tell "still the generated one" apart from
	// "an operator has since set their own", however that happened -- a
	// manually typed .env edit included, since that never goes through this
	// package at all.
	passwordTemporaryFileName = ".admin_password_temporary"
)

// Info is the editable identity shown to clients.
type Info struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// IconExt is the icon file's extension (e.g. ".png"), empty when no
	// icon has been uploaded. Kept alongside Name/Description so Store can
	// find the icon file without a directory listing.
	IconExt string `json:"icon_ext,omitempty"`
	// WelcomeMessagePhoneTemplate/WelcomeMessageEmailTemplate are raw
	// admin-panel overrides for the login-notification message sent from
	// the official system account (777000) on every completed phone/email
	// sign-in -- see domain.ResolveWelcomeMessageTemplate. Empty means "not
	// configured": the resolver falls through to the TELESRV_WELCOME_MESSAGE_*
	// env var, then the compiled-in default. Deliberately stored raw (not
	// pre-resolved), so a deployment that never touches the panel keeps
	// tracking whatever the fallback currently is, including future changes
	// to the compiled-in default.
	WelcomeMessagePhoneTemplate string `json:"welcome_message_phone_template,omitempty"`
	WelcomeMessageEmailTemplate string `json:"welcome_message_email_template,omitempty"`
	// LoginCodeMessageTemplate is the raw admin-panel override for the
	// 777000 login-code delivery message (see
	// domain.ResolveLoginCodeMessageTemplate). Unlike the welcome-message
	// templates above there is only one -- the message never varies by
	// delivery channel. Empty means "not configured": the resolver falls
	// through to the TELESRV_LOGIN_CODE_MESSAGE_TEMPLATE env var, then the
	// compiled-in default. Stored raw, same "not pre-resolved" contract as
	// the welcome-message overrides.
	LoginCodeMessageTemplate string `json:"login_code_message_template,omitempty"`
}

// Store reads/writes Info and the icon file under a directory (typically
// Config.IdentityDir). All methods are safe to call from multiple goroutines
// and multiple processes (the admin binary writes, the main server binary
// reads) -- writes are atomic via a temp file + rename.
type Store struct {
	dir string
}

func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

func (s *Store) metaPath() string {
	return filepath.Join(s.dir, metaFileName)
}

func (s *Store) iconPath(ext string) string {
	return filepath.Join(s.dir, iconBaseName+ext)
}

func (s *Store) setupPendingPath() string {
	return filepath.Join(s.dir, setupPendingFileName)
}

func (s *Store) passwordTemporaryPath() string {
	return filepath.Join(s.dir, passwordTemporaryFileName)
}

// SetupPending reports whether the first-run wizard still has work to do.
// A deployment that predates this feature (upgraded from an older admin
// binary, or one that was never bootstrapped through quickstart at all)
// never had this file created for it, so it reads as "not pending" --
// already done, no wizard -- regardless of what its identity.json happens
// to contain. A nil Store (a minimal test fixture, say) reads the same way
// -- "not pending" is the answer that costs nothing if it's wrong.
func (s *Store) SetupPending() bool {
	if s == nil {
		return false
	}
	_, err := os.Stat(s.setupPendingPath())
	return err == nil
}

// TemporaryPasswordMatches reports whether password is exactly the value
// quickstart auto-generated for the very first login. cmd/telesrv-admin's
// validSecret pairs this with SetupPending: the generated password
// authenticates only until the wizard finishes, so a string that was
// printed once to a terminal and never chosen by anyone doesn't go on
// being a standing credential forever. It never matches a password an
// operator set themselves, at any point -- there's no file to fool it
// with, only an exact value comparison. A nil Store never matches, same
// reasoning as SetupPending.
func (s *Store) TemporaryPasswordMatches(password string) bool {
	if s == nil || password == "" {
		return false
	}
	stored, err := os.ReadFile(s.passwordTemporaryPath())
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(stored, []byte(password)) == 1
}

// Get reads the current identity. A missing file is not an error -- it just
// means nothing has been configured yet, so Info{} (all empty) is returned.
func (s *Store) Get() (Info, error) {
	data, err := os.ReadFile(s.metaPath())
	if os.IsNotExist(err) {
		return Info{}, nil
	}
	if err != nil {
		return Info{}, fmt.Errorf("identity: read: %w", err)
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		return Info{}, fmt.Errorf("identity: decode: %w", err)
	}
	return info, nil
}

// SetText updates name/description, preserving whatever icon is already
// configured.
func (s *Store) SetText(name, description string) error {
	info, err := s.Get()
	if err != nil {
		return err
	}
	info.Name = strings.TrimSpace(name)
	info.Description = strings.TrimSpace(description)
	return s.save(info)
}

// SetWelcomeMessageTemplates updates the login-notification template
// overrides, preserving whatever name/description/icon is already
// configured. An empty string in either argument clears that method's
// override (falls back to the env var / compiled-in default -- see Info's
// field comments), following the same "empty means unset" convention as the
// rest of Info.
func (s *Store) SetWelcomeMessageTemplates(phone, email string) error {
	info, err := s.Get()
	if err != nil {
		return err
	}
	info.WelcomeMessagePhoneTemplate = strings.TrimSpace(phone)
	info.WelcomeMessageEmailTemplate = strings.TrimSpace(email)
	return s.save(info)
}

// SetLoginCodeMessageTemplate updates the login-code delivery message's
// admin-panel override, preserving whatever else is already configured. An
// empty string clears the override (falls back to the env var / compiled-in
// default -- same "empty means unset" convention as the rest of Info).
// Unlike SetWelcomeMessageTemplates there is no per-method split: every
// login code, regardless of delivery channel, uses the same template.
//
// Callers must validate template with domain.ValidateLoginCodeMessageTemplate
// before calling this -- this method does not itself reject a template
// missing the {{code}} placeholder, since internal/identity does not depend
// on internal/domain (see the package doc comment).
func (s *Store) SetLoginCodeMessageTemplate(template string) error {
	info, err := s.Get()
	if err != nil {
		return err
	}
	info.LoginCodeMessageTemplate = strings.TrimSpace(template)
	return s.save(info)
}

// MarkSetupComplete removes the pending marker so SetupPending reads false
// from here on. Idempotent -- calling it again once the marker is already
// gone is a no-op, not an error.
//
// Deliberately leaves the temporary-password marker in place: validSecret
// needs TemporaryPasswordMatches to keep recognizing that exact value
// *after* setup completes, which is the whole mechanism that retires it --
// deleting the marker here would make that check quietly stop matching and
// the password would keep working forever, the opposite of the point.
// Nothing about leaving it costs anything: it never matches a different
// password (an operator's real one, whenever they set it), and this
// package's only reader of it is that one comparison.
func (s *Store) MarkSetupComplete() error {
	if err := os.Remove(s.setupPendingPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("identity: remove setup-pending marker: %w", err)
	}
	return nil
}

// SetIcon replaces the icon file (removing any previous one under a
// different extension) and records its extension in identity.json.
// ext must include the leading dot (e.g. ".png").
func (s *Store) SetIcon(data []byte, ext string) error {
	info, err := s.Get()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("identity: mkdir: %w", err)
	}
	if info.IconExt != "" && info.IconExt != ext {
		_ = os.Remove(s.iconPath(info.IconExt))
	}
	if err := writeFileAtomic(s.iconPath(ext), data, 0o644); err != nil {
		return fmt.Errorf("identity: write icon: %w", err)
	}
	info.IconExt = ext
	return s.save(info)
}

// RemoveIcon deletes the configured icon, if any.
func (s *Store) RemoveIcon() error {
	info, err := s.Get()
	if err != nil {
		return err
	}
	if info.IconExt == "" {
		return nil
	}
	_ = os.Remove(s.iconPath(info.IconExt))
	info.IconExt = ""
	return s.save(info)
}

// Icon returns the icon's raw bytes and its file extension, or ("", nil,
// false) when no icon is configured.
func (s *Store) Icon() (data []byte, ext string, ok bool) {
	info, err := s.Get()
	if err != nil || info.IconExt == "" {
		return nil, "", false
	}
	raw, err := os.ReadFile(s.iconPath(info.IconExt))
	if err != nil {
		return nil, "", false
	}
	return raw, info.IconExt, true
}

func (s *Store) save(info Info) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("identity: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("identity: encode: %w", err)
	}
	if err := writeFileAtomic(s.metaPath(), data, 0o644); err != nil {
		return fmt.Errorf("identity: write: %w", err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
