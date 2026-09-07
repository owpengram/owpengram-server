package main

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestValidateAdminPassword(t *testing.T) {
	cases := []struct {
		name     string
		password string
		want     error
	}{
		{"ok", "correct horse battery", nil},
		// No length floor: the operator picks the password, however short.
		{"a single character", "x", nil},
		{"blank", "   ", errPasswordBlank},
		{"empty", "", errPasswordBlank},
		// bcrypt truncates silently past 72 bytes, so anything longer must be
		// refused rather than accepted as a password the operator did not set.
		{"past bcrypt's input limit", strings.Repeat("a", 73), errPasswordTooLong},
		{"73 bytes of multibyte runes", strings.Repeat("é", 37), errPasswordTooLong},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAdminPassword(tc.password)
			if !errors.Is(err, tc.want) {
				t.Fatalf("validateAdminPassword(%q) = %v, want %v", tc.password, err, tc.want)
			}
		})
	}
}

func TestValidateAdminUsername(t *testing.T) {
	valid := []string{"admin", "ops.lead", "on-call_2", strings.Repeat("a", 64)}
	for _, username := range valid {
		if err := validateAdminUsername(username); err != nil {
			t.Errorf("validateAdminUsername(%q) = %v, want nil", username, err)
		}
	}
	invalid := []string{
		"",
		"ab",                    // under the floor
		strings.Repeat("a", 65), // over the ceiling
		"has space",
		"with\ttab",
		"with\nnewline",
		"аdmin",  // Cyrillic 'а': renders like "admin" but is a different operator
		"admin*", // permission wildcard has no business in a name
		"a@b",
	}
	for _, username := range invalid {
		if err := validateAdminUsername(username); !errors.Is(err, errUsernameInvalid) {
			t.Errorf("validateAdminUsername(%q) = %v, want errUsernameInvalid", username, err)
		}
	}
}

func TestHashAdminPasswordRoundTrips(t *testing.T) {
	const password = "a sufficiently long password"
	hash, err := hashAdminPassword(password)
	if err != nil {
		t.Fatalf("hashAdminPassword: %v", err)
	}
	if strings.Contains(hash, password) {
		t.Fatal("hash contains the plaintext")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		t.Fatalf("hash does not verify against its own password: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password+"x")); err == nil {
		t.Fatal("hash verified against the wrong password")
	}
	if cost, err := bcrypt.Cost([]byte(hash)); err != nil || cost != bcryptCost {
		t.Fatalf("cost = %d (err %v), want %d", cost, err, bcryptCost)
	}
}

func TestHashAdminPasswordRejectsInvalid(t *testing.T) {
	// A short password is fine; an absent one is not.
	if _, err := hashAdminPassword("x"); err != nil {
		t.Fatalf("a one-character password was refused: %v", err)
	}
	if _, err := hashAdminPassword("   "); !errors.Is(err, errPasswordBlank) {
		t.Fatalf("err = %v, want errPasswordBlank", err)
	}
	if _, err := hashAdminPassword(strings.Repeat("a", 73)); !errors.Is(err, errPasswordTooLong) {
		t.Fatalf("err = %v, want errPasswordTooLong", err)
	}
}

// The dummy hash exists so a login against an unknown username costs the same
// bcrypt work as a real one. If it were malformed, CompareHashAndPassword would
// return early and hand back the timing signal it is there to remove.
func TestDummyBcryptHashIsWellFormedAndUnusable(t *testing.T) {
	cost, err := bcrypt.Cost([]byte(dummyBcryptHash))
	if err != nil {
		t.Fatalf("dummy hash is not a valid bcrypt hash: %v", err)
	}
	if cost != bcryptCost {
		t.Fatalf("dummy hash cost = %d, want %d -- it must cost the same as a real one", cost, bcryptCost)
	}
	for _, guess := range []string{"", "password", "admin", dummyBcryptHash} {
		if err := bcrypt.CompareHashAndPassword([]byte(dummyBcryptHash), []byte(guess)); err == nil {
			t.Fatalf("dummy hash authenticated %q", guess)
		}
	}
}

func TestNormalisePermissions(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"trims and drops empties", []string{" a ", "", "  ", "b"}, []string{"a", "b"}},
		{"de-duplicates", []string{"a", "a", "b", "a"}, []string{"a", "b"}},
		// A stored list that both names the wildcard and lists rights would read
		// narrower than it actually is wherever it is displayed.
		{"wildcard collapses everything", []string{"a", "*", "b"}, []string{permissionAll}},
		{"wildcard alone", []string{"*"}, []string{permissionAll}},
		{"empty stays empty", []string{}, []string{}},
		{"only blanks", []string{"", " "}, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalisePermissions(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("normalisePermissions(%v) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("normalisePermissions(%v) = %v, want %v", tc.in, got, tc.want)
				}
			}
		})
	}
}

// assignablePermissions drives the account editor. Offering "*" there would let
// a click hand out every right including admins.manage, which is exactly what
// the per-permission list exists to make deliberate.
func TestAssignablePermissionsExcludesWildcard(t *testing.T) {
	for _, p := range assignablePermissions() {
		if p == permissionAll {
			t.Fatal("assignablePermissions offers the wildcard")
		}
	}
	var sawAdminsManage bool
	for _, p := range assignablePermissions() {
		if p == permissionAdminsManage {
			sawAdminsManage = true
		}
	}
	if !sawAdminsManage {
		t.Fatal("assignablePermissions omits admins.manage, so it could never be granted")
	}
}

// A blank username must never authenticate, even with the correct break-glass
// secret. It briefly did, which made an empty field an unnamed second route to
// the highest-privilege login; the operator has to be asked for by name.
func TestBlankUsernameNeverAuthenticates(t *testing.T) {
	s := &server{cfg: uiConfig{Password: "letmein", Permissions: []string{permissionAll}}}
	for _, username := range []string{"", "   ", "\t"} {
		if _, ok := s.authenticateLogin(t.Context(), loginRequest{Username: username, Secret: "letmein"}); ok {
			t.Fatalf("blank username %q authenticated", username)
		}
	}
	// The same secret under the operator's actual name still works, so the
	// check above is refusing the blank name rather than the credential.
	identity, ok := s.authenticateLogin(t.Context(), loginRequest{Username: breakGlassUsername, Secret: "letmein"})
	if !ok {
		t.Fatal("the break-glass operator could not sign in by name")
	}
	if identity.actor != breakGlassUsername {
		t.Fatalf("actor = %q, want %q", identity.actor, breakGlassUsername)
	}
	if identity.userID != 0 {
		t.Fatalf("userID = %d, want 0 -- the break-glass operator has no database row", identity.userID)
	}
}

// Case is not a way to get a different operator: the name resolves to the
// break-glass login however it is typed, matching the case-insensitive unique
// index that named accounts live under.
func TestBreakGlassUsernameIsCaseInsensitive(t *testing.T) {
	s := &server{cfg: uiConfig{Password: "letmein", Permissions: []string{permissionAll}}}
	for _, username := range []string{"owpengram", "OwpenGram", "OWPENGRAM", " owpengram "} {
		identity, ok := s.authenticateLogin(t.Context(), loginRequest{Username: username, Secret: "letmein"})
		if !ok {
			t.Fatalf("%q did not resolve to the break-glass operator", username)
		}
		if identity.actor != breakGlassUsername {
			t.Fatalf("%q signed in as %q", username, identity.actor)
		}
	}
	if _, ok := s.authenticateLogin(t.Context(), loginRequest{Username: breakGlassUsername, Secret: "wrong"}); ok {
		t.Fatal("the break-glass operator authenticated with the wrong secret")
	}
}

// A session for a named account must not be trusted on the strength of its
// signature alone: the account's rights are re-read per request, and a nil read
// store has to fail closed rather than fall back to the claims.
func TestCurrentSessionPermissionsFailsClosedWithoutStore(t *testing.T) {
	s := &server{}
	if _, ok := s.currentSessionPermissions(t.Context(), sessionClaims{
		UserID:      7,
		Epoch:       1,
		Permissions: []string{permissionAll},
	}); ok {
		t.Fatal("a named-account session was accepted with no store to verify it against")
	}
}

// The break-glass operator has no row to re-read, so it keeps the configured
// rights -- that login is the way back in when the database is unreachable.
func TestCurrentSessionPermissionsAllowsBreakGlass(t *testing.T) {
	s := &server{}
	perms, ok := s.currentSessionPermissions(t.Context(), sessionClaims{
		UserID:      0,
		Permissions: []string{permissionServerManage},
	})
	if !ok {
		t.Fatal("break-glass session rejected")
	}
	if !perms.Has(permissionServerManage) {
		t.Fatal("break-glass session lost its configured permission")
	}
	if perms.Has(permissionAdminsManage) {
		t.Fatal("break-glass session gained a permission it was not configured with")
	}
}
