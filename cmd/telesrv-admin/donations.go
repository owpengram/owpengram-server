package main

import (
	"net/http"
	"net/url"
	"strconv"

	"telesrv/internal/admin"
	"telesrv/internal/domain"
)

// handleDonationWalletStatusAPI reports whether a crypto donations wallet
// exists and how many deposit addresses have been assigned. The wallet is
// auto-provisioned at server startup if missing (see docs/donations.md) --
// there is deliberately no admin action to create or regenerate one here.
func (s *server) handleDonationWalletStatusAPI(w http.ResponseWriter, r *http.Request) {
	if s.read == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "read store is not configured")
		return
	}
	status, err := s.read.donationWalletStatus(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// handleDonationChainsAPI lists every configured chain, enabled or not, plus
// every configured stablecoin row, for the chain config editor.
func (s *server) handleDonationChainsAPI(w http.ResponseWriter, r *http.Request) {
	if s.read == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "read store is not configured")
		return
	}
	chains, err := s.read.donationChains(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tokens, err := s.read.donationTokens(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chains": chains, "tokens": tokens})
}

// handleDonationDepositsAPI lists deposits across every user, newest first,
// for the transactions/donations history table.
func (s *server) handleDonationDepositsAPI(w http.ResponseWriter, r *http.Request) {
	if s.read == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "read store is not configured")
		return
	}
	query := r.URL.Query()
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
	rows, hasMore, err := s.read.donationDeposits(r.Context(), beforeID, limit)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	nextBeforeID := ""
	if hasMore && len(rows) > 0 {
		nextBeforeID = strconv.FormatInt(rows[len(rows)-1].ID, 10)
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows, "has_more": hasMore, "next_before_id": nextBeforeID})
}

type updateDonationChainAPIRequest struct {
	CommandID             string `json:"command_id"`
	Reason                string `json:"reason"`
	Confirm               bool   `json:"confirm"`
	ChainKey              string `json:"chain_key"`
	RPCURL                string `json:"rpc_url"`
	WSURL                 string `json:"ws_url"`
	Enabled               bool   `json:"enabled"`
	ConfirmationsRequired int    `json:"confirmations_required"`
	PriceFeedAddress      string `json:"price_feed_address"`
	ManualUSDRateMicros   int64  `json:"manual_usd_rate_micros"`
	ExplorerURL           string `json:"explorer_url"`
	PriceSource           string `json:"price_source"`
	PriceSourceID         string `json:"price_source_id"`
}

func (s *server) handleUpdateDonationChainAPI(w http.ResponseWriter, r *http.Request) {
	var body updateDonationChainAPIRequest
	if !decodeAction(w, r, &body) {
		return
	}
	req := admin.UpdateDonationChainRequest{
		CommandMeta:           s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "donation-chain"),
		ChainKey:              body.ChainKey,
		RPCURL:                body.RPCURL,
		WSURL:                 body.WSURL,
		Enabled:               body.Enabled,
		ConfirmationsRequired: body.ConfirmationsRequired,
		PriceFeedAddress:      body.PriceFeedAddress,
		ManualUSDRateMicros:   body.ManualUSDRateMicros,
		ExplorerURL:           body.ExplorerURL,
		PriceSource:           body.PriceSource,
		PriceSourceID:         body.PriceSourceID,
	}
	result, err := s.callAdminAPI(r.Context(), "/v1/donations/chains/update", req)
	writeCommandResultAPI(w, result, err)
}

type createDonationChainAPIRequest struct {
	CommandID             string `json:"command_id"`
	Reason                string `json:"reason"`
	Confirm               bool   `json:"confirm"`
	ChainKey              string `json:"chain_key"`
	Name                  string `json:"name"`
	ChainID               int64  `json:"chain_id"`
	NativeSymbol          string `json:"native_symbol"`
	NativeDecimals        int    `json:"native_decimals"`
	RPCURL                string `json:"rpc_url"`
	WSURL                 string `json:"ws_url"`
	ConfirmationsRequired int    `json:"confirmations_required"`
	PriceFeedAddress      string `json:"price_feed_address"`
	ManualUSDRateMicros   int64  `json:"manual_usd_rate_micros"`
	Enabled               bool   `json:"enabled"`
	ExplorerURL           string `json:"explorer_url"`
	PriceSource           string `json:"price_source"`
	PriceSourceID         string `json:"price_source_id"`
}

func (s *server) handleCreateDonationChainAPI(w http.ResponseWriter, r *http.Request) {
	var body createDonationChainAPIRequest
	if !decodeAction(w, r, &body) {
		return
	}
	req := admin.CreateDonationChainRequest{
		CommandMeta:           s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "donation-chain-create"),
		ChainKey:              body.ChainKey,
		Name:                  body.Name,
		ChainID:               body.ChainID,
		NativeSymbol:          body.NativeSymbol,
		NativeDecimals:        body.NativeDecimals,
		RPCURL:                body.RPCURL,
		WSURL:                 body.WSURL,
		ConfirmationsRequired: body.ConfirmationsRequired,
		PriceFeedAddress:      body.PriceFeedAddress,
		ManualUSDRateMicros:   body.ManualUSDRateMicros,
		Enabled:               body.Enabled,
		ExplorerURL:           body.ExplorerURL,
		PriceSource:           body.PriceSource,
		PriceSourceID:         body.PriceSourceID,
	}
	result, err := s.callAdminAPI(r.Context(), "/v1/donations/chains/create", req)
	writeCommandResultAPI(w, result, err)
}

type deleteDonationChainAPIRequest struct {
	CommandID string `json:"command_id"`
	Reason    string `json:"reason"`
	Confirm   bool   `json:"confirm"`
	ChainKey  string `json:"chain_key"`
}

func (s *server) handleDeleteDonationChainAPI(w http.ResponseWriter, r *http.Request) {
	var body deleteDonationChainAPIRequest
	if !decodeAction(w, r, &body) {
		return
	}
	req := admin.DeleteDonationChainRequest{
		CommandMeta: s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "donation-chain-delete"),
		ChainKey:    body.ChainKey,
	}
	result, err := s.callAdminAPI(r.Context(), "/v1/donations/chains/delete", req)
	writeCommandResultAPI(w, result, err)
}

type sweepDonationChainAPIRequest struct {
	CommandID   string `json:"command_id"`
	Reason      string `json:"reason"`
	Confirm     bool   `json:"confirm"`
	ChainKey    string `json:"chain_key"`
	Destination string `json:"destination"`
}

func (s *server) handleSweepDonationChainAPI(w http.ResponseWriter, r *http.Request) {
	var body sweepDonationChainAPIRequest
	if !decodeAction(w, r, &body) {
		return
	}
	req := admin.SweepDonationChainRequest{
		CommandMeta: s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "donation-sweep"),
		ChainKey:    body.ChainKey,
		Destination: body.Destination,
	}
	result, err := s.callAdminAPI(r.Context(), "/v1/donations/sweep", req)
	writeCommandResultAPI(w, result, err)
}

type upsertDonationTokenAPIRequest struct {
	CommandID       string `json:"command_id"`
	Reason          string `json:"reason"`
	Confirm         bool   `json:"confirm"`
	ChainKey        string `json:"chain_key"`
	Symbol          string `json:"symbol"`
	ContractAddress string `json:"contract_address"`
	Decimals        int    `json:"decimals"`
}

func (s *server) handleUpsertDonationTokenAPI(w http.ResponseWriter, r *http.Request) {
	var body upsertDonationTokenAPIRequest
	if !decodeAction(w, r, &body) {
		return
	}
	req := admin.UpsertDonationTokenRequest{
		CommandMeta:     s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "donation-token"),
		ChainKey:        body.ChainKey,
		Symbol:          body.Symbol,
		ContractAddress: body.ContractAddress,
		Decimals:        body.Decimals,
	}
	result, err := s.callAdminAPI(r.Context(), "/v1/donations/tokens/upsert", req)
	writeCommandResultAPI(w, result, err)
}

type deleteDonationTokenAPIRequest struct {
	CommandID string `json:"command_id"`
	Reason    string `json:"reason"`
	Confirm   bool   `json:"confirm"`
	ChainKey  string `json:"chain_key"`
	Symbol    string `json:"symbol"`
}

func (s *server) handleDeleteDonationTokenAPI(w http.ResponseWriter, r *http.Request) {
	var body deleteDonationTokenAPIRequest
	if !decodeAction(w, r, &body) {
		return
	}
	req := admin.DeleteDonationTokenRequest{
		CommandMeta: s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "donation-token-delete"),
		ChainKey:    body.ChainKey,
		Symbol:      body.Symbol,
	}
	result, err := s.callAdminAPI(r.Context(), "/v1/donations/tokens/delete", req)
	writeCommandResultAPI(w, result, err)
}

// handleDonationSettingsAPI proxies the deployment's Star price so the page
// quotes the same number the server credits at, instead of a second copy of
// the constant drifting in the frontend.
func (s *server) handleDonationSettingsAPI(w http.ResponseWriter, r *http.Request) {
	var out map[string]any
	if err := s.callAdminAPIGet(r.Context(), "/v1/donations/settings", &out); err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDonationPricePreviewAPI proxies a price-source lookup so an
// operator can confirm a coin id resolves before saving a network against
// it -- a wrong id otherwise fails silently in the background refresher.
func (s *server) handleDonationPricePreviewAPI(w http.ResponseWriter, r *http.Request) {
	sourceID := r.URL.Query().Get("source_id")
	if sourceID == "" {
		writeAPIError(w, http.StatusBadRequest, "source_id is required")
		return
	}
	var out map[string]any
	if err := s.callAdminAPIGet(r.Context(), "/v1/donations/price-preview?source_id="+url.QueryEscape(sourceID), &out); err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDonationChainBalanceAPI proxies to the main server's live on-chain
// balance read (see callAdminAPIGet's doc comment for why this, alone among
// donation reads, can't be served from readStore's direct Postgres access).
func (s *server) handleDonationChainBalanceAPI(w http.ResponseWriter, r *http.Request) {
	chainKey := r.PathValue("key")
	if chainKey == "" {
		writeAPIError(w, http.StatusBadRequest, "chain key is required")
		return
	}
	var balance domain.DonationChainBalance
	if err := s.callAdminAPIGet(r.Context(), "/v1/donations/chains/"+url.PathEscape(chainKey)+"/balance", &balance); err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, balance)
}
