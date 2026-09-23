package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"telesrv/internal/admin"
)

// handleStarGiftCatalogAPI lists the StarGift storefront, enabled and
// disabled, for the catalog management page.
func (s *server) handleStarGiftCatalogAPI(w http.ResponseWriter, r *http.Request) {
	if s.read == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "read store is not configured")
		return
	}
	rows, err := s.read.ListStarGiftCatalog(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows})
}

// handleStarGiftCatalogAnimationAPI proxies one gift's active-revision Lottie
// JSON from the real telesrv admin API, for the catalog list's preview cell.
func (s *server) handleStarGiftCatalogAnimationAPI(w http.ResponseWriter, r *http.Request) {
	giftID, err := parseInt64(r.PathValue("gift_id"))
	if err != nil || giftID <= 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid gift id")
		return
	}
	s.proxyAdminJSON(w, r, fmt.Sprintf("/v1/star-gift-catalog/%d/animation", giftID), 5<<20)
}

// handleStarGiftCollectiblesAPI proxies one gift's currently-published
// collectible (unique-upgrade) attribute pool, for the "Attribute pool" modal.
func (s *server) handleStarGiftCollectiblesAPI(w http.ResponseWriter, r *http.Request) {
	giftID, err := parseInt64(r.PathValue("gift_id"))
	if err != nil || giftID <= 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid gift id")
		return
	}
	s.proxyAdminJSON(w, r, fmt.Sprintf("/v1/star-gift-catalog/%d/collectibles", giftID), 4<<20)
}

// handleStarGiftCollectibleAnimationAPI proxies one published model/pattern
// attribute's Lottie JSON.
func (s *server) handleStarGiftCollectibleAnimationAPI(w http.ResponseWriter, r *http.Request) {
	giftID, err := parseInt64(r.PathValue("gift_id"))
	attributeID, attrErr := parseInt64(r.PathValue("attribute_id"))
	kind := r.PathValue("kind")
	if err != nil || giftID <= 0 || attrErr != nil || attributeID <= 0 || (kind != "model" && kind != "pattern") {
		writeAPIError(w, http.StatusBadRequest, "invalid collectible animation")
		return
	}
	s.proxyAdminJSON(w, r, fmt.Sprintf("/v1/star-gift-catalog/%d/collectibles/%s/%d/animation", giftID, kind, attributeID), 4<<20)
}

type createStarGiftCatalogEntryAPIRequest struct {
	CommandID         string `json:"command_id"`
	Reason            string `json:"reason"`
	Confirm           bool   `json:"confirm"`
	GiftID            int64  `json:"gift_id,string"`
	Title             string `json:"title"`
	Stars             int64  `json:"stars,string"`
	ConvertStars      int64  `json:"convert_stars,string"`
	Enabled           bool   `json:"enabled"`
	SortOrder         int    `json:"sort_order"`
	Limited           bool   `json:"limited"`
	AvailabilityTotal int    `json:"availability_total"`
}

func (s *server) handleCreateStarGiftCatalogEntryAPI(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, (4<<20)+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	var body createStarGiftCatalogEntryAPIRequest
	dec := json.NewDecoder(strings.NewReader(r.FormValue("metadata")))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid metadata: "+err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "animation file is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (4<<20)+1))
	if err != nil || len(data) == 0 || len(data) > 4<<20 {
		writeAPIError(w, http.StatusBadRequest, "animation file is empty or too large")
		return
	}
	req := admin.CreateStarGiftCatalogEntryRequest{
		CommandMeta:       s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "create-star-gift-catalog-entry"),
		GiftID:            body.GiftID,
		Title:             body.Title,
		Stars:             body.Stars,
		ConvertStars:      body.ConvertStars,
		Enabled:           body.Enabled,
		SortOrder:         body.SortOrder,
		Limited:           body.Limited,
		AvailabilityTotal: body.AvailabilityTotal,
	}
	result, err := s.callAdminMultipart(r.Context(), "/v1/star-gift-catalog/create", req, header.Filename, data)
	writeCommandResultAPI(w, result, err)
}

type setStarGiftCatalogEnabledAPIRequest struct {
	CommandID string    `json:"command_id"`
	Reason    string    `json:"reason"`
	Confirm   bool      `json:"confirm"`
	GiftID    flexInt64 `json:"gift_id"`
	Enabled   bool      `json:"enabled"`
}

func (s *server) handleSetStarGiftCatalogEnabledAPI(w http.ResponseWriter, r *http.Request) {
	var body setStarGiftCatalogEnabledAPIRequest
	if !decodeAction(w, r, &body) {
		return
	}
	req := admin.SetStarGiftCatalogEnabledRequest{
		CommandMeta: s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "set-star-gift-catalog-enabled"),
		GiftID:      body.GiftID.Int64(),
		Enabled:     body.Enabled,
	}
	result, err := s.callAdminAPI(r.Context(), "/v1/star-gift-catalog/set-enabled", req)
	writeCommandResultAPI(w, result, err)
}

type setStarGiftCatalogSortOrderAPIRequest struct {
	CommandID string    `json:"command_id"`
	Reason    string    `json:"reason"`
	Confirm   bool      `json:"confirm"`
	GiftID    flexInt64 `json:"gift_id"`
	SortOrder int       `json:"sort_order"`
}

func (s *server) handleSetStarGiftCatalogSortOrderAPI(w http.ResponseWriter, r *http.Request) {
	var body setStarGiftCatalogSortOrderAPIRequest
	if !decodeAction(w, r, &body) {
		return
	}
	req := admin.SetStarGiftCatalogSortOrderRequest{
		CommandMeta: s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "set-star-gift-catalog-sort-order"),
		GiftID:      body.GiftID.Int64(),
		SortOrder:   body.SortOrder,
	}
	result, err := s.callAdminAPI(r.Context(), "/v1/star-gift-catalog/set-sort-order", req)
	writeCommandResultAPI(w, result, err)
}

type publishStarGiftCollectiblesAPIRequest struct {
	CommandID    string                                     `json:"command_id"`
	Reason       string                                     `json:"reason"`
	Confirm      bool                                       `json:"confirm"`
	GiftID       flexInt64                                  `json:"gift_id"`
	UpgradeStars flexInt64                                  `json:"upgrade_stars"`
	SupplyTotal  int                                        `json:"supply_total"`
	SlugPrefix   string                                     `json:"slug_prefix"`
	Models       []admin.StarGiftCollectibleAnimationUpload `json:"models"`
	Patterns     []admin.StarGiftCollectibleAnimationUpload `json:"patterns"`
	Backdrops    []admin.StarGiftCollectibleBackdropInput   `json:"backdrops"`
}

// handlePublishStarGiftCollectiblesAPI parses one animation file per model
// and pattern (keyed by each upload's own FileKey, since a pool can have any
// number of them, unlike the single-file catalog-entry upload) and forwards
// the whole draft to the real admin API as one multipart request.
func (s *server) handlePublishStarGiftCollectiblesAPI(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid collectible multipart form: "+err.Error())
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	var body publishStarGiftCollectiblesAPIRequest
	dec := json.NewDecoder(strings.NewReader(r.FormValue("metadata")))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid metadata: "+err.Error())
		return
	}
	if len(body.Models)+len(body.Patterns) > 128 {
		writeAPIError(w, http.StatusBadRequest, "too many collectible animation files")
		return
	}
	seen := make(map[string]struct{}, len(body.Models)+len(body.Patterns))
	load := func(upload *admin.StarGiftCollectibleAnimationUpload) error {
		upload.FileKey = strings.TrimSpace(upload.FileKey)
		if upload.FileKey == "" {
			return errors.New("animation file key is required")
		}
		if _, ok := seen[upload.FileKey]; ok {
			return fmt.Errorf("duplicate animation file key %q", upload.FileKey)
		}
		seen[upload.FileKey] = struct{}{}
		file, header, err := r.FormFile(upload.FileKey)
		if err != nil {
			return fmt.Errorf("animation file %q is required", upload.FileKey)
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, (4<<20)+1))
		if err != nil || len(data) == 0 || len(data) > 4<<20 {
			return fmt.Errorf("animation file %q is empty or too large", upload.FileKey)
		}
		upload.FileName = header.Filename
		upload.Data = data
		return nil
	}
	for i := range body.Models {
		if err := load(&body.Models[i]); err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	for i := range body.Patterns {
		if err := load(&body.Patterns[i]); err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	req := admin.PublishStarGiftCollectiblesRequest{
		CommandMeta:  s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "publish-star-gift-collectibles"),
		GiftID:       body.GiftID.Int64(),
		UpgradeStars: body.UpgradeStars.Int64(),
		SupplyTotal:  body.SupplyTotal,
		SlugPrefix:   body.SlugPrefix,
		Models:       body.Models,
		Patterns:     body.Patterns,
		Backdrops:    body.Backdrops,
	}
	result, err := s.callAdminCollectibleMultipart(r.Context(), "/v1/star-gift-catalog/publish-collectibles", req)
	writeCommandResultAPI(w, result, err)
}

// callAdminCollectibleMultipart mirrors callAdminMultipart but forwards one
// file per model/pattern upload instead of a single "file" field -- a
// collectible pool's models and patterns each carry their own animation.
func (s *server) callAdminCollectibleMultipart(ctx context.Context, apiPath string, payload admin.PublishStarGiftCollectiblesRequest) (admin.CommandResult, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	meta, err := json.Marshal(payload)
	if err != nil {
		return admin.CommandResult{}, err
	}
	if err := writer.WriteField("metadata", string(meta)); err != nil {
		return admin.CommandResult{}, err
	}
	writeUploads := func(uploads []admin.StarGiftCollectibleAnimationUpload) error {
		for _, upload := range uploads {
			part, err := writer.CreateFormFile(upload.FileKey, upload.FileName)
			if err != nil {
				return err
			}
			if _, err := part.Write(upload.Data); err != nil {
				return err
			}
		}
		return nil
	}
	if err := writeUploads(payload.Models); err != nil {
		return admin.CommandResult{}, err
	}
	if err := writeUploads(payload.Patterns); err != nil {
		return admin.CommandResult{}, err
	}
	if err := writer.Close(); err != nil {
		return admin.CommandResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.AdminAPIURL+apiPath, &body)
	if err != nil {
		return admin.CommandResult{}, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+s.cfg.AdminAPIToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return admin.CommandResult{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var result admin.CommandResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, fmt.Errorf("admin api %s: status=%d body=%s", apiPath, resp.StatusCode, string(raw))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if result.Error == "" {
			result.Error = resp.Status
		}
		return result, errors.New(result.Error)
	}
	return result, nil
}

type giveStarGiftAPIRequest struct {
	CommandID           string    `json:"command_id"`
	Reason              string    `json:"reason"`
	Confirm             bool      `json:"confirm"`
	SenderUserID        flexInt64 `json:"sender_user_id"`
	UserID              flexInt64 `json:"user_id"`
	ChannelID           flexInt64 `json:"channel_id"`
	GiftID              flexInt64 `json:"gift_id"`
	HideName            bool      `json:"hide_name"`
	Message             string    `json:"message"`
	Upgrade             bool      `json:"upgrade"`
	ModelAttributeID    flexInt64 `json:"model_attribute_id"`
	PatternAttributeID  flexInt64 `json:"pattern_attribute_id"`
	BackdropAttributeID flexInt64 `json:"backdrop_attribute_id"`
}

func (s *server) handleGiveStarGiftAPI(w http.ResponseWriter, r *http.Request) {
	var body giveStarGiftAPIRequest
	if !decodeAction(w, r, &body) {
		return
	}
	req := admin.GiveStarGiftRequest{
		CommandMeta:         s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "give-star-gift"),
		SenderUserID:        body.SenderUserID.Int64(),
		UserID:              body.UserID.Int64(),
		ChannelID:           body.ChannelID.Int64(),
		GiftID:              body.GiftID.Int64(),
		HideName:            body.HideName,
		Message:             body.Message,
		Upgrade:             body.Upgrade,
		ModelAttributeID:    body.ModelAttributeID.Int64(),
		PatternAttributeID:  body.PatternAttributeID.Int64(),
		BackdropAttributeID: body.BackdropAttributeID.Int64(),
	}
	result, err := s.callAdminAPI(r.Context(), "/v1/star-gift-catalog/give", req)
	writeCommandResultAPI(w, result, err)
}

const maxGiftPackZipBytes = 32 << 20

type importGiftPackAPIRequest struct {
	CommandID string `json:"command_id"`
	Reason    string `json:"reason"`
	Confirm   bool   `json:"confirm"`
}

// handleImportGiftPackAPI forwards an uploaded pack .zip (pack.json plus its
// referenced assets) to the real admin API unchanged -- the pack itself
// carries every gift's authoring data, so there's nothing else to collect
// from the operator besides the usual reason/dry-run/confirm metadata.
func (s *server) handleImportGiftPackAPI(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxGiftPackZipBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	var body importGiftPackAPIRequest
	dec := json.NewDecoder(strings.NewReader(r.FormValue("metadata")))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid metadata: "+err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "pack zip file is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxGiftPackZipBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxGiftPackZipBytes {
		writeAPIError(w, http.StatusBadRequest, "pack zip is empty or too large")
		return
	}
	req := admin.ImportGiftPackRequest{
		CommandMeta: s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "import-gift-pack"),
	}
	result, err := s.callAdminMultipart(r.Context(), "/v1/star-gift-catalog/import-pack", req, header.Filename, data)
	writeCommandResultAPI(w, result, err)
}

type importBuiltinGiftPackAPIRequest struct {
	CommandID string `json:"command_id"`
	Reason    string `json:"reason"`
	Confirm   bool   `json:"confirm"`
	PackID    string `json:"pack_id"`
}

func (s *server) handleImportBuiltinGiftPackAPI(w http.ResponseWriter, r *http.Request) {
	var body importBuiltinGiftPackAPIRequest
	if !decodeAction(w, r, &body) {
		return
	}
	if !validPackToken(body.PackID) {
		writeAPIError(w, http.StatusBadRequest, "invalid pack id")
		return
	}
	req := admin.ImportBuiltinGiftPackRequest{
		CommandMeta: s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "import-builtin-gift-pack"),
		PackID:      body.PackID,
	}
	result, err := s.callAdminAPI(r.Context(), "/v1/gift-packs/import", req)
	writeCommandResultAPI(w, result, err)
}

// handleBuiltinGiftPacksAPI proxies the list of built-in packs for the
// "Import Pack" tab.
func (s *server) handleBuiltinGiftPacksAPI(w http.ResponseWriter, r *http.Request) {
	s.proxyAdminJSON(w, r, "/v1/gift-packs", 1<<20)
}

// handleBuiltinGiftPackAnimationAPI proxies one built-in gift's Lottie JSON
// for the pack preview.
func (s *server) handleBuiltinGiftPackAnimationAPI(w http.ResponseWriter, r *http.Request) {
	packID, slug := r.PathValue("pack_id"), r.PathValue("slug")
	if !validPackToken(packID) || !validPackToken(slug) {
		writeAPIError(w, http.StatusBadRequest, "invalid gift pack animation")
		return
	}
	s.proxyAdminJSONWithCache(w, r, "/v1/gift-packs/"+packID+"/animations/"+slug, 4<<20, "private, max-age=300")
}

// validPackToken keeps pack ids and gift slugs to the charset the registry
// uses, since they're spliced into the upstream URL path.
func validPackToken(v string) bool {
	if v == "" || len(v) > 64 {
		return false
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}
