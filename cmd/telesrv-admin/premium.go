package main

import (
	"net/http"
	"strconv"

	"telesrv/internal/admin"
)

// handlePremiumPlansAPI lists every Premium plan, enabled and disabled, for
// the plan management page.
func (s *server) handlePremiumPlansAPI(w http.ResponseWriter, r *http.Request) {
	if s.read == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "read store is not configured")
		return
	}
	plans, err := s.read.premiumPlans(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": plans})
}

// handlePremiumPaymentAPI looks up one payment intent, so the refund form
// can show what it is about to reverse before the operator confirms.
func (s *server) handlePremiumPaymentAPI(w http.ResponseWriter, r *http.Request) {
	if s.read == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "read store is not configured")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid payment id")
		return
	}
	payment, found, err := s.read.premiumPayment(r.Context(), id)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "payment not found")
		return
	}
	writeJSON(w, http.StatusOK, payment)
}

type upsertPremiumPlanAPIRequest struct {
	CommandID       string `json:"command_id"`
	Reason          string `json:"reason"`
	Confirm         bool   `json:"confirm"`
	Months          int    `json:"months"`
	DurationDays    int    `json:"duration_days"`
	AmountStars     int64  `json:"amount_stars"`
	Enabled         bool   `json:"enabled"`
	SortOrder       int    `json:"sort_order"`
	Label           string `json:"label"`
	ExpectedVersion int64  `json:"expected_version"`
}

func (s *server) handleUpsertPremiumPlanAPI(w http.ResponseWriter, r *http.Request) {
	var body upsertPremiumPlanAPIRequest
	if !decodeAction(w, r, &body) {
		return
	}
	req := admin.UpsertPremiumPlanRequest{
		CommandMeta:     s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "premium-plan"),
		Months:          body.Months,
		DurationDays:    body.DurationDays,
		AmountStars:     body.AmountStars,
		Enabled:         body.Enabled,
		SortOrder:       body.SortOrder,
		Label:           body.Label,
		ExpectedVersion: body.ExpectedVersion,
	}
	result, err := s.callAdminAPI(r.Context(), "/v1/premium/plans/upsert", req)
	writeCommandResultAPI(w, result, err)
}

type refundPremiumAPIRequest struct {
	CommandID       string `json:"command_id"`
	Reason          string `json:"reason"`
	Confirm         bool   `json:"confirm"`
	PaymentIntentID int64  `json:"payment_intent_id"`
}

func (s *server) handleRefundPremiumAPI(w http.ResponseWriter, r *http.Request) {
	var body refundPremiumAPIRequest
	if !decodeAction(w, r, &body) {
		return
	}
	req := admin.RefundPremiumRequest{
		CommandMeta:     s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "premium-refund"),
		PaymentIntentID: body.PaymentIntentID,
	}
	result, err := s.callAdminAPI(r.Context(), "/v1/premium/refund", req)
	writeCommandResultAPI(w, result, err)
}
