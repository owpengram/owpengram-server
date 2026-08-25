package main

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"telesrv/internal/admin"
)

// serverManage gates the whole Server Settings surface -- see
// permissionServerManage's doc comment in security.go for why this is one
// right rather than split review/manage like other sections.
func (s *server) serverManage(handler http.HandlerFunc) http.Handler {
	return s.requireAuthAPI(s.requirePermission(permissionServerManage, handler))
}

// serverCommandResult builds the same admin.CommandResult shape every other
// action returns, without going through internal/admin's runCommand +
// Postgres audit log: everything in this file operates on local files/
// processes directly (see routes() in server.go for why), so there is no
// owpengram-server-side admin_commands row to write. The actor/reason are
// still in meta for structured logging if that's ever added; today they are
// simply not persisted anywhere.
func serverCommandResult(meta admin.CommandMeta, action string, err error, message string, details map[string]any) admin.CommandResult {
	status := "completed"
	errText := ""
	if err != nil {
		status = "failed"
		errText = err.Error()
		if message == "" {
			message = "command failed"
		}
	}
	return admin.CommandResult{
		CommandID: meta.CommandID,
		Action:    action,
		Status:    status,
		DryRun:    meta.DryRun,
		Message:   message,
		Details:   details,
		Error:     errText,
	}
}

// --- identity (name/description/icon) ---------------------------------

func (s *server) handleServerIdentityAPI(w http.ResponseWriter, r *http.Request) {
	info, err := s.identity.Get()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleServerIconAPI serves the icon's raw bytes for the panel's own
// preview -- separate from owpengram-server's public /owpengram/server-icon
// (same underlying file, different process/auth: this one is behind the
// admin session, not open to clients).
func (s *server) handleServerIconAPI(w http.ResponseWriter, r *http.Request) {
	data, ext, ok := s.identity.Icon()
	if !ok {
		writeAPIError(w, http.StatusNotFound, "no icon configured")
		return
	}
	contentType := map[string]string{
		".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
		".webp": "image/webp", ".gif": "image/gif",
	}[ext]
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

type setServerIdentityAPIRequest struct {
	CommandID   string `json:"command_id"`
	Reason      string `json:"reason"`
	Confirm     bool   `json:"confirm"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s *server) handleSetServerIdentityAPI(w http.ResponseWriter, r *http.Request) {
	var body setServerIdentityAPIRequest
	if !decodeAction(w, r, &body) {
		return
	}
	meta := s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "set-server-identity")
	details := map[string]any{"name": body.Name, "description": body.Description}
	if meta.DryRun {
		writeJSON(w, http.StatusOK, serverCommandResult(meta, "server.set_identity", nil, "server identity validated", details))
		return
	}
	err := s.identity.SetText(body.Name, body.Description)
	writeJSON(w, http.StatusOK, serverCommandResult(meta, "server.set_identity", err, "server identity updated", details))
}

var allowedServerIconExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true,
}

const maxServerIconBytes = 2 << 20 // 2 MiB

type uploadServerIconAPIRequest struct {
	CommandID string `json:"command_id"`
	Reason    string `json:"reason"`
	Confirm   bool   `json:"confirm"`
}

// handleUploadServerIconAPI takes multipart/form-data (a "metadata" JSON
// field + a "file" field), the same shape handleSetAccountAvatarAPI uses --
// deliberately not JSON+base64 like the other Server Settings actions:
// base64 inflates a file ~33%, and decodeAction's plain io.LimitReader caps
// the request body at 1MiB regardless of maxServerIconBytes, so a real
// multi-hundred-KB icon would fail decoding ("unexpected EOF" from the
// truncated body) before this handler ever saw it. Multipart sidesteps that
// entirely -- the size cap below is enforced on the actual file bytes.
func (s *server) handleUploadServerIconAPI(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxServerIconBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	var body uploadServerIconAPIRequest
	dec := json.NewDecoder(strings.NewReader(r.FormValue("metadata")))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid metadata: "+err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "icon file is required")
		return
	}
	defer file.Close()
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !allowedServerIconExts[ext] {
		writeAPIError(w, http.StatusBadRequest, "unsupported icon extension")
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, maxServerIconBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxServerIconBytes {
		writeAPIError(w, http.StatusBadRequest, "icon file is empty or too large (max 2MiB)")
		return
	}
	meta := s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "upload-server-icon")
	details := map[string]any{"bytes": len(data), "ext": ext}
	if meta.DryRun {
		writeJSON(w, http.StatusOK, serverCommandResult(meta, "server.upload_icon", nil, "server icon validated", details))
		return
	}
	setErr := s.identity.SetIcon(data, ext)
	writeJSON(w, http.StatusOK, serverCommandResult(meta, "server.upload_icon", setErr, "server icon updated", details))
}

type removeServerIconAPIRequest struct {
	CommandID string `json:"command_id"`
	Reason    string `json:"reason"`
	Confirm   bool   `json:"confirm"`
}

func (s *server) handleRemoveServerIconAPI(w http.ResponseWriter, r *http.Request) {
	var body removeServerIconAPIRequest
	if !decodeAction(w, r, &body) {
		return
	}
	meta := s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "remove-server-icon")
	if meta.DryRun {
		writeJSON(w, http.StatusOK, serverCommandResult(meta, "server.remove_icon", nil, "server icon removal validated", nil))
		return
	}
	err := s.identity.RemoveIcon()
	writeJSON(w, http.StatusOK, serverCommandResult(meta, "server.remove_icon", err, "server icon removed", nil))
}

// --- .env editing --------------------------------------------------------

func (s *server) handleServerEnvAPI(w http.ResponseWriter, r *http.Request) {
	groups, err := s.serverCtl.ReadEnvGroups()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

type updateServerEnvAPIRequest struct {
	CommandID string            `json:"command_id"`
	Reason    string            `json:"reason"`
	Confirm   bool              `json:"confirm"`
	Values    map[string]string `json:"values"`
}

func (s *server) handleUpdateServerEnvAPI(w http.ResponseWriter, r *http.Request) {
	var body updateServerEnvAPIRequest
	if !decodeAction(w, r, &body) {
		return
	}
	meta := s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "update-server-env")
	details := map[string]any{"keys_changed": len(body.Values)}
	if meta.DryRun {
		writeJSON(w, http.StatusOK, serverCommandResult(meta, "server.update_env", nil, "would update .env -- takes effect on next Restart/Update", details))
		return
	}
	err := s.serverCtl.WriteEnvValues(body.Values)
	writeJSON(w, http.StatusOK, serverCommandResult(meta, "server.update_env", err, ".env updated -- restart the server for changes to take effect", details))
}

// --- status / restart / update -------------------------------------------

func (s *server) handleServerStatusAPI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.serverCtl.Status())
}

type restartServerAPIRequest struct {
	CommandID string `json:"command_id"`
	Reason    string `json:"reason"`
	Confirm   bool   `json:"confirm"`
}

func (s *server) handleRestartServerAPI(w http.ResponseWriter, r *http.Request) {
	var body restartServerAPIRequest
	if !decodeAction(w, r, &body) {
		return
	}
	meta := s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "restart-server")
	if meta.DryRun {
		writeJSON(w, http.StatusOK, serverCommandResult(meta, "server.restart", nil, "restart validated -- rebuilds and relaunches bin/owpengram-server", nil))
		return
	}
	log, err := s.serverCtl.Restart(r.Context())
	writeJSON(w, http.StatusOK, serverCommandResult(meta, "server.restart", err, "server restarted", map[string]any{"log": log}))
}

type updateServerAPIRequest struct {
	CommandID string `json:"command_id"`
	Reason    string `json:"reason"`
	Confirm   bool   `json:"confirm"`
}

func (s *server) handleUpdateServerAPI(w http.ResponseWriter, r *http.Request) {
	var body updateServerAPIRequest
	if !decodeAction(w, r, &body) {
		return
	}
	meta := s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "update-server")
	if meta.DryRun {
		writeJSON(w, http.StatusOK, serverCommandResult(meta, "server.update", nil, "update validated -- git pull, rebuild both binaries, relaunch bin/owpengram-server (admin panel binary is rebuilt but not self-restarted)", nil))
		return
	}
	log, err := s.serverCtl.Update(r.Context())
	writeJSON(w, http.StatusOK, serverCommandResult(meta, "server.update", err, "server updated", map[string]any{"log": log}))
}
