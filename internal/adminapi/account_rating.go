package adminapi

import (
	"net/http"
	"strconv"
	"time"

	"telesrv/internal/admin"
	"telesrv/internal/domain"
)

func (s *Server) handleRecomputeAccountRating(w http.ResponseWriter, r *http.Request) {
	var req admin.RecomputeAccountRatingRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.RecomputeAccountRating(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleAdjustAccountRating(w http.ResponseWriter, r *http.Request) {
	var req admin.AdjustAccountRatingRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.svc.AdjustAccountRating(r.Context(), req)
	writeCommandResult(w, result, err)
}

func (s *Server) handleAccountRatings(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	minLevel, ok := optionalQueryInt(w, query, "min_level")
	if !ok {
		return
	}
	userID, ok := optionalQueryInt64(w, query, "user_id")
	if !ok {
		return
	}
	beforeID, ok := optionalQueryInt64(w, query, "before_id")
	if !ok {
		return
	}
	limit, ok := optionalQueryInt(w, query, "limit")
	if !ok {
		return
	}
	items, err := s.svc.AccountRatings(r.Context(), domain.AccountRatingFilter{
		MinLevel: minLevel, UserID: userID, BeforeID: beforeID, Limit: limit,
	})
	if err != nil {
		writeAccountRatingError(w, err)
		return
	}
	ratings := make([]map[string]any, 0, len(items))
	for _, item := range items {
		ratings = append(ratings, accountRatingResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ratings": ratings})
}

func (s *Server) handleAccountRating(w http.ResponseWriter, r *http.Request) {
	userID, ok := moderationPathID(w, r, "id")
	if !ok {
		return
	}
	rating, err := s.svc.AccountRating(r.Context(), userID)
	if err != nil {
		writeAccountRatingError(w, err)
		return
	}
	limit, ok := optionalQueryInt(w, r.URL.Query(), "limit")
	if !ok {
		return
	}
	events, err := s.svc.AccountRatingEvents(r.Context(), userID, limit)
	if err != nil {
		writeAccountRatingError(w, err)
		return
	}
	ledger := make([]map[string]any, 0, len(events))
	for _, item := range events {
		ledger = append(ledger, accountRatingEventResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rating": accountRatingResponse(rating), "events": ledger,
	})
}

// accountRatingResponse renders one composite rating. The score and every
// component stay decimal strings for the same exactness reason as the asset ids.
func accountRatingResponse(rating domain.AccountRating) map[string]any {
	out := map[string]any{
		"user_id":             strconv.FormatInt(rating.UserID, 10),
		"level":               rating.Level,
		"stars":               strconv.FormatInt(rating.Stars, 10),
		"current_level_stars": strconv.FormatInt(rating.CurrentLevelStars, 10),
		"has_next_level":      rating.HasNextLevel,
		"stars_component":     strconv.FormatInt(rating.StarsComponent, 10),
		"activity_component":  strconv.FormatInt(rating.ActivityComponent, 10),
		"penalty_component":   strconv.FormatInt(rating.PenaltyComponent, 10),
		"manual_component":    strconv.FormatInt(rating.ManualComponent, 10),
		"pending_stars":       strconv.FormatInt(rating.PendingStars, 10),
		"version":             strconv.FormatInt(rating.Version, 10),
	}
	if rating.HasNextLevel {
		out["next_level_stars"] = strconv.FormatInt(rating.NextLevelStars, 10)
	}
	if !rating.PendingDate.IsZero() {
		out["pending_date"] = rating.PendingDate.UTC().Format(time.RFC3339)
	}
	if !rating.ComputedAt.IsZero() {
		out["computed_at"] = rating.ComputedAt.UTC().Format(time.RFC3339)
	}
	if !rating.UpdatedAt.IsZero() {
		out["updated_at"] = rating.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func accountRatingEventResponse(event domain.AccountRatingEvent) map[string]any {
	out := map[string]any{
		"id":          strconv.FormatInt(event.ID, 10),
		"user_id":     strconv.FormatInt(event.UserID, 10),
		"kind":        string(event.Kind),
		"amount":      strconv.FormatInt(event.Amount, 10),
		"reason":      event.Reason,
		"actor":       event.Actor,
		"command_key": event.CommandKey,
	}
	if !event.CreatedAt.IsZero() {
		out["created_at"] = event.CreatedAt.UTC().Format(time.RFC3339)
	}
	return out
}

// writeAccountRatingError maps an account-rating failure onto its stable
// admin code and the matching HTTP status. An unmapped failure stays a 500
// with its own text rather than being dressed up as a client error.
func writeAccountRatingError(w http.ResponseWriter, err error) {
	code := admin.AccountRatingErrorCode(err)
	status := http.StatusInternalServerError
	switch code {
	case admin.CodeRatingNotFound:
		status = http.StatusNotFound
	case admin.CodeRatingAdjustmentInvalid, admin.CodeRatingWeightsInvalid:
		status = http.StatusBadRequest
	}
	writeCodedError(w, status, code, err.Error())
}
