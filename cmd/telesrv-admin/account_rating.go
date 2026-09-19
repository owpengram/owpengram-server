package main

import (
	"errors"
	"net/http"
	"strconv"

	"telesrv/internal/admin"
)

type recomputeAccountRatingAPIRequest struct {
	CommandID string    `json:"command_id"`
	Reason    string    `json:"reason"`
	Confirm   bool      `json:"confirm"`
	UserID    flexInt64 `json:"user_id"`
}

func (s *server) handleRecomputeAccountRatingAPI(w http.ResponseWriter, r *http.Request) {
	var body recomputeAccountRatingAPIRequest
	if !decodeAction(w, r, &body) {
		return
	}
	req := admin.RecomputeAccountRatingRequest{
		CommandMeta: s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "recompute-account-rating"),
		UserID:      body.UserID.Int64(),
	}
	result, err := s.callAdminAPI(r.Context(), "/v1/account-ratings/recompute", req)
	writeCommandResultAPI(w, result, err)
}

type adjustAccountRatingAPIRequest struct {
	CommandID string    `json:"command_id"`
	Reason    string    `json:"reason"`
	Confirm   bool      `json:"confirm"`
	UserID    flexInt64 `json:"user_id"`
	Amount    flexInt64 `json:"amount"`
}

func (s *server) handleAdjustAccountRatingAPI(w http.ResponseWriter, r *http.Request) {
	var body adjustAccountRatingAPIRequest
	if !decodeAction(w, r, &body) {
		return
	}
	req := admin.AdjustAccountRatingRequest{
		CommandMeta: s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "adjust-account-rating"),
		UserID:      body.UserID.Int64(),
		Amount:      body.Amount.Int64(),
	}
	result, err := s.callAdminAPI(r.Context(), "/v1/account-ratings/adjust", req)
	writeCommandResultAPI(w, result, err)
}

// handleAccountRatingsAPI pages the leaderboard. next_before_id is the last
// user id: the keyset predicate resolves the full (level, stars, user_id)
// cursor from it, so one opaque-looking value is enough to continue the page.
func (s *server) handleAccountRatingsAPI(w http.ResponseWriter, r *http.Request) {
	if s.read == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "read store is not configured")
		return
	}
	query := r.URL.Query()
	minLevel, err := parseInt(query.Get("min_level"))
	if err != nil || minLevel < 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid min_level")
		return
	}
	userID, err := parseInt64(query.Get("user_id"))
	if err != nil || userID < 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid user_id")
		return
	}
	beforeID, err := parseInt64(query.Get("before_id"))
	if err != nil || beforeID < 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid before_id")
		return
	}
	limit, err := parseInt(query.Get("limit"))
	if err != nil || limit < 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid limit")
		return
	}
	rows, hasMore, err := s.read.ListAccountRatings(r.Context(), minLevel, userID, beforeID, limit, query.Get("q"))
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	nextBeforeID := ""
	if hasMore && len(rows) > 0 {
		nextBeforeID = strconv.FormatInt(rows[len(rows)-1].UserID, 10)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rows":           rows,
		"has_more":       hasMore,
		"next_before_id": nextBeforeID,
	})
}

func (s *server) handleAccountRatingDetailAPI(w http.ResponseWriter, r *http.Request) {
	if s.read == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "read store is not configured")
		return
	}
	userID, err := parseInt64(r.PathValue("user_id"))
	if err != nil || userID <= 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid user_id")
		return
	}
	detail, err := s.read.AccountRatingDetail(r.Context(), userID)
	if err != nil {
		if errors.Is(err, errReadNotFound) {
			writeAPIError(w, http.StatusNotFound, "account rating not found")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rating": detail.Rating,
		"events": detail.Events,
	})
}
