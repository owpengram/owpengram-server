package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestRunOperatorCommandIdempotencyIntegration proves the property
// admin_command_runner.go exists for: a command_id is a strict binding to one
// request. Gated on TELESRV_TEST_POSTGRES_DSN like the rest of the package,
// since the pre-claim/replay logic lives entirely in what Postgres actually
// does with ON CONFLICT DO NOTHING.
func TestRunOperatorCommandIdempotencyIntegration(t *testing.T) {
	store, pool := verificationReadStore(t)
	srv := &server{read: store}
	ctx := context.Background()
	unique := time.Now().UnixNano() & 0x7fffffff
	username := "cmdrunner" + strconv.FormatInt(unique, 10)
	commandID := "exec-cmdrunner-" + strconv.FormatInt(unique, 10)

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM admin_console_users WHERE username = $1`, username)
		_, _ = pool.Exec(context.Background(), `DELETE FROM admin_commands WHERE command_id = $1`, commandID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM admin_audit_logs WHERE command_id = $1`, commandID)
	})

	post := func(password string) (int, map[string]any) {
		t.Helper()
		enabled := true
		body, err := json.Marshal(adminUserActionRequest{
			CommandID: commandID, Reason: "integration", Confirm: true,
			Username: username, Password: password,
			Permissions: []string{permissionAccountsRead}, Enabled: &enabled,
		})
		if err != nil {
			t.Fatalf("marshal action: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/actions/create-admin-operator", strings.NewReader(string(body)))
		req = req.WithContext(context.WithValue(req.Context(), actorKey{}, "operator"))
		rec := httptest.NewRecorder()
		srv.handleCreateAdminUserAPI(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	// First execution creates the operator.
	code, first := post("correct horse battery staple")
	if code != http.StatusOK || first["error"] != nil {
		t.Fatalf("first create code=%d body=%+v", code, first)
	}
	if already, _ := first["already_executed"].(bool); already {
		t.Fatalf("first execution reported already_executed: %+v", first)
	}
	var created int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM admin_console_users WHERE username = $1`, username).Scan(&created); err != nil {
		t.Fatalf("count created: %v", err)
	}
	if created != 1 {
		t.Fatalf("created rows=%d, want 1", created)
	}

	// Replaying the exact same command_id + request must not create a second
	// row, and must report the original outcome as already executed.
	code, replay := post("correct horse battery staple")
	if code != http.StatusOK || replay["error"] != nil {
		t.Fatalf("replay code=%d body=%+v", code, replay)
	}
	if already, _ := replay["already_executed"].(bool); !already {
		t.Fatalf("replay did not report already_executed: %+v", replay)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM admin_console_users WHERE username = $1`, username).Scan(&created); err != nil {
		t.Fatalf("count after replay: %v", err)
	}
	if created != 1 {
		t.Fatalf("created rows after replay=%d, want still 1", created)
	}

	// The same command_id reused with a different password (a different
	// request, per the fingerprint) must be refused outright, not silently
	// re-run against the new password.
	code, conflict := post("a different password entirely")
	if code != http.StatusBadGateway {
		t.Fatalf("conflicting reuse code=%d body=%+v, want 502", code, conflict)
	}
	if msg, _ := conflict["error"].(string); msg != "COMMAND_ID_CONFLICT" {
		t.Fatalf("conflicting reuse error=%q, want COMMAND_ID_CONFLICT", msg)
	}

	// Exactly one admin_commands row and one admin_audit_logs row exist for
	// this command_id -- the replay and the conflict must not have appended
	// more.
	var commandRows, auditRows int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM admin_commands WHERE command_id = $1`, commandID).Scan(&commandRows); err != nil {
		t.Fatalf("count admin_commands: %v", err)
	}
	if commandRows != 1 {
		t.Fatalf("admin_commands rows=%d, want 1", commandRows)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM admin_audit_logs WHERE command_id = $1`, commandID).Scan(&auditRows); err != nil {
		t.Fatalf("count admin_audit_logs: %v", err)
	}
	if auditRows != 1 {
		t.Fatalf("admin_audit_logs rows=%d, want 1", auditRows)
	}
}
