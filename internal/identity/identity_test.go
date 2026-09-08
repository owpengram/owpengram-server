package identity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreTextRoundTrip(t *testing.T) {
	s := NewStore(t.TempDir())

	info, err := s.Get()
	if err != nil {
		t.Fatal(err)
	}
	if info != (Info{}) {
		t.Fatalf("expected zero-value Info before any write, got %+v", info)
	}

	if err := s.SetText("  OwpenGram  ", "  A self-hosted server.  "); err != nil {
		t.Fatal(err)
	}
	info, err = s.Get()
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "OwpenGram" || info.Description != "A self-hosted server." {
		t.Fatalf("got %+v", info)
	}
}

func TestStoreIconRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	if _, _, ok := s.Icon(); ok {
		t.Fatal("expected no icon before any upload")
	}

	png := []byte{0x89, 'P', 'N', 'G', 1, 2, 3}
	if err := s.SetIcon(png, ".png"); err != nil {
		t.Fatal(err)
	}
	data, ext, ok := s.Icon()
	if !ok || ext != ".png" || string(data) != string(png) {
		t.Fatalf("Icon() = %v, %q, %v", data, ext, ok)
	}
	if _, err := os.Stat(filepath.Join(dir, "icon.png")); err != nil {
		t.Fatal(err)
	}

	// Replacing with a different extension removes the old file.
	jpg := []byte{0xFF, 0xD8, 0xFF}
	if err := s.SetIcon(jpg, ".jpg"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "icon.png")); !os.IsNotExist(err) {
		t.Fatal("old icon.png should have been removed")
	}
	data, ext, ok = s.Icon()
	if !ok || ext != ".jpg" || string(data) != string(jpg) {
		t.Fatalf("Icon() after replace = %v, %q, %v", data, ext, ok)
	}

	if err := s.RemoveIcon(); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.Icon(); ok {
		t.Fatal("expected no icon after RemoveIcon")
	}

	// Name/description set earlier (none here) must survive icon churn --
	// Get() after all this should still report a clean, non-error zero name.
	info, err := s.Get()
	if err != nil {
		t.Fatal(err)
	}
	if info.IconExt != "" {
		t.Fatalf("IconExt should be empty after removal, got %q", info.IconExt)
	}
}

func TestStorePreservesIconAcrossTextEdits(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.SetIcon([]byte{1, 2, 3}, ".webp"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetText("New Name", "New description"); err != nil {
		t.Fatal(err)
	}
	_, ext, ok := s.Icon()
	if !ok || ext != ".webp" {
		t.Fatalf("icon lost after unrelated SetText: ext=%q ok=%v", ext, ok)
	}
}

func TestStoreWelcomeMessageTemplatesRoundTrip(t *testing.T) {
	s := NewStore(t.TempDir())

	info, err := s.Get()
	if err != nil {
		t.Fatal(err)
	}
	if info.WelcomeMessagePhoneTemplate != "" || info.WelcomeMessageEmailTemplate != "" {
		t.Fatalf("expected empty overrides before any write, got %+v", info)
	}

	if err := s.SetWelcomeMessageTemplates("  Custom phone template  ", "Custom email template"); err != nil {
		t.Fatal(err)
	}
	info, err = s.Get()
	if err != nil {
		t.Fatal(err)
	}
	if info.WelcomeMessagePhoneTemplate != "Custom phone template" || info.WelcomeMessageEmailTemplate != "Custom email template" {
		t.Fatalf("got %+v", info)
	}

	// Clearing one override (empty string) must not disturb the other.
	if err := s.SetWelcomeMessageTemplates("", "Custom email template"); err != nil {
		t.Fatal(err)
	}
	info, err = s.Get()
	if err != nil {
		t.Fatal(err)
	}
	if info.WelcomeMessagePhoneTemplate != "" || info.WelcomeMessageEmailTemplate != "Custom email template" {
		t.Fatalf("got %+v after clearing phone override", info)
	}
}

func TestStoreLoginCodeMessageTemplateRoundTrip(t *testing.T) {
	s := NewStore(t.TempDir())

	info, err := s.Get()
	if err != nil {
		t.Fatal(err)
	}
	if info.LoginCodeMessageTemplate != "" {
		t.Fatalf("expected empty override before any write, got %+v", info)
	}

	if err := s.SetLoginCodeMessageTemplate("  Custom code template {{code}}  "); err != nil {
		t.Fatal(err)
	}
	info, err = s.Get()
	if err != nil {
		t.Fatal(err)
	}
	if info.LoginCodeMessageTemplate != "Custom code template {{code}}" {
		t.Fatalf("got %+v", info)
	}

	// Clearing (empty string) resets to "unset".
	if err := s.SetLoginCodeMessageTemplate(""); err != nil {
		t.Fatal(err)
	}
	info, err = s.Get()
	if err != nil {
		t.Fatal(err)
	}
	if info.LoginCodeMessageTemplate != "" {
		t.Fatalf("expected override cleared, got %+v", info)
	}
}

func TestStoreLoginCodeMessageTemplatePreservedAcrossTextEdits(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.SetLoginCodeMessageTemplate("code tpl {{code}}"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetText("New Name", "New description"); err != nil {
		t.Fatal(err)
	}
	info, err := s.Get()
	if err != nil {
		t.Fatal(err)
	}
	if info.LoginCodeMessageTemplate != "code tpl {{code}}" {
		t.Fatalf("login code message template lost after unrelated SetText: %+v", info)
	}
}

func TestStoreWelcomeMessageTemplatesPreservedAcrossTextEdits(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.SetWelcomeMessageTemplates("phone tpl", "email tpl"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetText("New Name", "New description"); err != nil {
		t.Fatal(err)
	}
	info, err := s.Get()
	if err != nil {
		t.Fatal(err)
	}
	if info.WelcomeMessagePhoneTemplate != "phone tpl" || info.WelcomeMessageEmailTemplate != "email tpl" {
		t.Fatalf("welcome message templates lost after unrelated SetText: %+v", info)
	}
}

// TestStoreSetupPendingSurvivesIdentityWrites is the regression case for the
// bug where "setup complete" was inferred from identity.json's own content
// (a non-empty name): the wizard's own Identity step calls SetText well
// before Done is ever reached, which made that content-based check flip to
// "done" mid-wizard -- a restart-and-reload partway through skipped
// straight to the normal shell. SetupPending must stay true across any
// number of unrelated identity writes and clear only via MarkSetupComplete.
func TestStoreSetupPendingSurvivesIdentityWrites(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	// Not pending at all until something (quickstart's bootstrap_env, in
	// production) creates the marker -- an install this store never saw
	// bootstrapped is treated as predating the wizard, not as mid-wizard.
	if s.SetupPending() {
		t.Fatal("expected SetupPending() == false before the marker file exists")
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, setupPendingFileName), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !s.SetupPending() {
		t.Fatal("expected SetupPending() == true once the marker file exists")
	}

	// The Identity step's save, and everything else short of Done.
	if err := s.SetText("Demo Server", "A test server"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetIcon([]byte{1, 2, 3}, ".png"); err != nil {
		t.Fatal(err)
	}
	if !s.SetupPending() {
		t.Fatal("expected SetupPending() to stay true after unrelated identity writes")
	}

	if err := s.MarkSetupComplete(); err != nil {
		t.Fatal(err)
	}
	if s.SetupPending() {
		t.Fatal("expected SetupPending() == false after MarkSetupComplete")
	}

	// Idempotent: calling it again once already gone is not an error.
	if err := s.MarkSetupComplete(); err != nil {
		t.Fatalf("MarkSetupComplete should be idempotent, got: %v", err)
	}
}

// TestStoreTemporaryPasswordMatches covers the one-time-login password
// quickstart generates: it must match only its own exact value, never an
// operator-chosen one. MarkSetupComplete deliberately does NOT erase this
// marker (see that method's doc comment) -- retiring the password is
// validSecret's job, combining this with SetupPending; on its own,
// TemporaryPasswordMatches keeps recognizing the same stored value even
// after the wizard finishes, which is exactly what lets that combination
// work at every login from then on, not just the first one after Done.
func TestStoreTemporaryPasswordMatches(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	if s.TemporaryPasswordMatches("anything") {
		t.Fatal("expected no match before the marker file exists")
	}
	if s.TemporaryPasswordMatches("") {
		t.Fatal("an empty password must never match")
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, passwordTemporaryFileName), []byte("generated-pw-123"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !s.TemporaryPasswordMatches("generated-pw-123") {
		t.Fatal("expected a match against the exact stored value")
	}
	if s.TemporaryPasswordMatches("something-an-operator-typed") {
		t.Fatal("a different password must never match the marker")
	}

	if err := s.MarkSetupComplete(); err != nil {
		t.Fatal(err)
	}
	if !s.TemporaryPasswordMatches("generated-pw-123") {
		t.Fatal("expected the match to survive MarkSetupComplete -- see its doc comment for why")
	}
	if s.SetupPending() {
		t.Fatal("expected SetupPending() == false after MarkSetupComplete regardless")
	}
}
