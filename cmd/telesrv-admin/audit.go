package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// handleAuditLogsAPI lists the global action trail for whoever holds
// audit.read. Filters are optional and combined: actor, action and a status
// of completed or failed, plus a limit that is capped so a spent month of
// commands cannot be dumped into the browser in one request.
func (s *server) handleAuditLogsAPI(w http.ResponseWriter, r *http.Request) {
	if s.read == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "read store is not configured")
		return
	}
	q := r.URL.Query()
	limit, err := parseAuditLogLimit(q.Get("limit"), 200)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := s.read.auditLogsGlobal(r.Context(), strings.TrimSpace(q.Get("actor")), strings.TrimSpace(q.Get("action")), strings.TrimSpace(q.Get("status")), limit)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows})
}

// parseAuditLogLimit parses an optional "limit" query value within 1..max,
// defaulting to max when absent.
func parseAuditLogLimit(raw string, max int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return max, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("limit must be a positive integer")
	}
	if n > max {
		n = max
	}
	return n, nil
}
