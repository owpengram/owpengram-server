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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	metaFileName = "identity.json"
	iconBaseName = "icon"
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
