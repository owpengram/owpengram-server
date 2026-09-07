package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The panel is deny-by-default: an API route must say which right it belongs
// to. Registering one with a bare requireAuthAPI would make it answer to every
// signed-in operator regardless of what they were granted -- which is how a
// scoped account quietly gets the run of the place.
//
// This reads the source rather than the routing table because that is where the
// mistake is made: it fails on the line someone is about to add, and names it.
func TestEveryAPIRouteDeclaresAScope(t *testing.T) {
	// Every /api route must be registered through a wrapper that names a
	// permission. Whitelisting the wrappers rather than blacklisting the bare
	// one is what makes this hold for helpers added later: a new wrapper is
	// unknown here until someone adds it deliberately, so it fails closed.
	allowed := []string{
		"s.scopedRoute(",
		"s.scopedRouteAll(",
		"s.requirePermission(",
		"s.requireAdminsManage(",
		"s.serverManage(",
		"s.verificationRead(",
		"s.botVerificationRead(",
		"s.botVerificationManage(",
	}
	// Routes that are reachable before a session exists, each for a stated
	// reason: /api/login is the way in (it carries its own credential), and the
	// two branding routes feed the login screen with the server name and icon
	// that owpengram-server already publishes to every client.
	exempt := map[string]bool{
		"POST /api/login":          true,
		"GET /api/public/branding": true,
		"GET /api/public/icon":     true,
	}

	route := regexp.MustCompile(`mux\.Handle(Func)?\("([A-Z]+ /api/[^"]*)"`)

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	var offenders []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, line := range strings.Split(string(source), "\n") {
			m := route.FindStringSubmatch(line)
			if m == nil || exempt[m[2]] {
				continue
			}
			guarded := false
			for _, wrapper := range allowed {
				if strings.Contains(line, wrapper) {
					guarded = true
					break
				}
			}
			if !guarded {
				offenders = append(offenders, name+": "+strings.TrimSpace(line))
			}
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("these routes are registered without a permission -- wrap them in s.scopedRoute(permission, ...):\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// Every right the account editor offers must be one the routes actually check,
// and vice versa -- a name in one list and not the other is either a right
// nobody can be granted or a checkbox that grants nothing.
func TestAssignablePermissionsMatchWhatRoutesEnforce(t *testing.T) {
	assignable := make(map[string]bool, len(assignablePermissions()))
	for _, p := range assignablePermissions() {
		if p == permissionSessionOnly {
			t.Fatal("permissionSessionOnly is not a grantable right and must not be offered")
		}
		if assignable[p] {
			t.Fatalf("permission %q is offered twice", p)
		}
		assignable[p] = true
	}
	if len(assignable) == 0 {
		t.Fatal("no assignable permissions")
	}
	// Spot-check the pairs the sections are built around, so a rename that
	// misses one half is caught here rather than by an operator who suddenly
	// cannot open a page.
	for _, required := range []string{
		permissionAccountsRead, permissionAccountsManage,
		permissionChannelsRead, permissionChannelsManage,
		permissionBotsRead, permissionBotsManage,
		permissionMessagesRead, permissionMessagesManage,
		permissionContentRead, permissionContentManage,
		permissionUsernamesRead, permissionUsernamesManage,
		permissionStorageRead, permissionStorageManage,
		permissionBroadcastsRead, permissionBroadcastsSend,
		permissionModerationReview, permissionDashboardRead,
		permissionAdminsManage, permissionServerManage,
	} {
		if !assignable[required] {
			t.Errorf("permission %q is enforced somewhere but cannot be granted", required)
		}
	}
}

// permissionSessionOnly must stay the empty string: scopedRoute distinguishes
// "a session is enough" from a real right by that emptiness, and panelPermissions
// drops empty entries, so it can never be smuggled into an account's list.
func TestSessionOnlyIsNotGrantable(t *testing.T) {
	if permissionSessionOnly != "" {
		t.Fatalf("permissionSessionOnly = %q, want the empty string", permissionSessionOnly)
	}
	perms := newPanelPermissions([]string{permissionSessionOnly, permissionAccountsRead})
	if perms.Has(permissionSessionOnly) {
		t.Fatal("an empty permission was treated as granted")
	}
	if !perms.Has(permissionAccountsRead) {
		t.Fatal("a real permission alongside it was lost")
	}
}
