package adminapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"telesrv/internal/admin"
	"telesrv/internal/domain"
)

func TestAdminAPIRequiresBearerToken(t *testing.T) {
	srv := &Server{token: "secret", svc: fakeService{}}
	req := httptest.NewRequest(http.MethodPost, "/v1/accounts/set-frozen", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", rec.Code)
	}
}

func TestAdminAPISetAccountFrozen(t *testing.T) {
	svc := &captureFreezeService{}
	srv := &Server{token: "secret", svc: svc}
	req := httptest.NewRequest(http.MethodPost, "/v1/accounts/set-frozen", strings.NewReader(`{"command_id":"c1","actor":"ops","reason":"test","dry_run":true,"user_id":1001,"frozen":true,"freeze_until":"2030-01-02T00:00:00Z","freeze_appeal_url":"https://appeals.example.test"}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"command_id":"c1"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
	if svc.req.UserID != 1001 || !svc.req.Frozen || svc.req.Until.IsZero() || svc.req.AppealURL != "https://appeals.example.test" {
		t.Fatalf("decoded freeze request = %+v", svc.req)
	}
}

type captureModerationService struct {
	fakeService
	filter       domain.ModerationCaseFilter
	decision     domain.ModerationDecisionRequest
	appealReview domain.ModerationDecisionRequest
}

func (s *captureModerationService) ModerationCases(_ context.Context, filter domain.ModerationCaseFilter) ([]domain.ModerationCase, error) {
	s.filter = filter
	return []domain.ModerationCase{{ID: 7}}, nil
}

func (s *captureModerationService) DecideModerationCase(_ context.Context, request domain.ModerationDecisionRequest) (domain.ModerationCaseDetail, bool, error) {
	s.decision = request
	return domain.ModerationCaseDetail{Case: domain.ModerationCase{ID: request.CaseID}}, true, nil
}

func (s *captureModerationService) ReviewModerationAppeal(_ context.Context, request domain.ModerationDecisionRequest) (domain.ModerationCaseDetail, bool, error) {
	s.appealReview = request
	return domain.ModerationCaseDetail{Case: domain.ModerationCase{ID: request.CaseID}}, true, nil
}

func TestAdminAPIModerationQueueDecisionAndAppealReview(t *testing.T) {
	svc := &captureModerationService{}
	srv := &Server{token: "secret", svc: svc}
	listRequest := httptest.NewRequest(
		http.MethodGet,
		"/v1/moderation/cases?statuses=open,action_failed&assigned_to=alice&target_type=user&target_id=99&limit=25",
		nil,
	)
	listRequest.Header.Set("Authorization", "Bearer secret")
	list := httptest.NewRecorder()
	srv.routes().ServeHTTP(list, listRequest)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"ID":7`) {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	if len(svc.filter.Statuses) != 2 ||
		svc.filter.Statuses[0] != domain.ModerationCaseOpen ||
		svc.filter.Statuses[1] != domain.ModerationCaseActionFailed ||
		svc.filter.AssignedTo != "alice" ||
		svc.filter.Target != (domain.Peer{Type: domain.PeerTypeUser, ID: 99}) ||
		svc.filter.Limit != 25 {
		t.Fatalf("filter=%+v", svc.filter)
	}

	decisionRequest := httptest.NewRequest(
		http.MethodPost, "/v1/moderation/cases/7/decide",
		strings.NewReader(`{"expected_version":3,"actor":"alice","reason":"confirmed","command_id":"decision-7","kind":"violation","actions":[{"kind":"mark_scam","payload":{}}]}`),
	)
	decisionRequest.Header.Set("Authorization", "Bearer secret")
	decision := httptest.NewRecorder()
	srv.routes().ServeHTTP(decision, decisionRequest)
	if decision.Code != http.StatusOK ||
		!strings.Contains(decision.Body.String(), `"created":true`) ||
		svc.decision.CaseID != 7 || svc.decision.ExpectedVersion != 3 ||
		svc.decision.Kind != domain.ModerationDecisionViolation ||
		len(svc.decision.Actions) != 1 ||
		svc.decision.Actions[0].Kind != domain.ModerationActionMarkScam {
		t.Fatalf("decision status=%d request=%+v body=%s",
			decision.Code, svc.decision, decision.Body.String())
	}

	reviewRequest := httptest.NewRequest(
		http.MethodPost, "/v1/moderation/cases/7/appeals/8/review",
		strings.NewReader(`{"expected_version":5,"actor":"bob","reason":"appeal accepted","command_id":"appeal-8","granted":true,"actions":[{"kind":"clear_peer_flags","payload":{}}]}`),
	)
	reviewRequest.Header.Set("Authorization", "Bearer secret")
	review := httptest.NewRecorder()
	srv.routes().ServeHTTP(review, reviewRequest)
	if review.Code != http.StatusOK ||
		svc.appealReview.CaseID != 7 || svc.appealReview.AppealID != 8 ||
		svc.appealReview.Kind != domain.ModerationDecisionAppealGrant ||
		len(svc.appealReview.Actions) != 1 ||
		svc.appealReview.Actions[0].Kind != domain.ModerationActionClearPeerFlags {
		t.Fatalf("review status=%d request=%+v body=%s",
			review.Code, svc.appealReview, review.Body.String())
	}
}

type emptyModerationCollectionsService struct {
	fakeService
}

func (emptyModerationCollectionsService) ModerationCases(
	context.Context,
	domain.ModerationCaseFilter,
) ([]domain.ModerationCase, error) {
	return nil, nil
}

func (emptyModerationCollectionsService) ModerationCase(
	_ context.Context,
	caseID int64,
) (domain.ModerationCaseDetail, bool, error) {
	return domain.ModerationCaseDetail{
		Case:      domain.ModerationCase{ID: caseID},
		ReportIDs: []int64{9},
	}, true, nil
}

func (emptyModerationCollectionsService) ModerationReport(
	_ context.Context,
	reportID int64,
) (domain.ModerationReport, bool, error) {
	return domain.ModerationReport{
		ID:    reportID,
		Items: []domain.ModerationReportItem{{ItemID: 10}},
	}, true, nil
}

func (emptyModerationCollectionsService) DecideModerationCase(
	_ context.Context,
	request domain.ModerationDecisionRequest,
) (domain.ModerationCaseDetail, bool, error) {
	return domain.ModerationCaseDetail{
		Case:      domain.ModerationCase{ID: request.CaseID},
		ReportIDs: []int64{9},
	}, true, nil
}

func (emptyModerationCollectionsService) ReviewModerationAppeal(
	_ context.Context,
	request domain.ModerationDecisionRequest,
) (domain.ModerationCaseDetail, bool, error) {
	return domain.ModerationCaseDetail{
		Case:      domain.ModerationCase{ID: request.CaseID},
		ReportIDs: []int64{9},
	}, true, nil
}

func TestAdminAPIModerationCollectionsAreJSONArrays(t *testing.T) {
	srv := &Server{token: "secret", svc: emptyModerationCollectionsService{}}
	tests := []struct {
		name         string
		method       string
		path         string
		body         string
		keys         []string
		nonEmptyKeys []string
		nested       string
	}{
		{
			name: "empty queue", method: http.MethodGet,
			path: "/v1/moderation/cases", keys: []string{"cases"},
		},
		{
			name: "fresh case", method: http.MethodGet,
			path:         "/v1/moderation/cases/7",
			keys:         []string{"Decisions", "Actions", "Appeals"},
			nonEmptyKeys: []string{"ReportIDs"},
		},
		{
			name: "report without media holds", method: http.MethodGet,
			path:         "/v1/moderation/reports/9",
			keys:         []string{"MediaHolds"},
			nonEmptyKeys: []string{"Items"},
		},
		{
			name: "decision response", method: http.MethodPost,
			path: "/v1/moderation/cases/7/decide", body: `{}`,
			nested:       "case",
			keys:         []string{"Decisions", "Actions", "Appeals"},
			nonEmptyKeys: []string{"ReportIDs"},
		},
		{
			name: "appeal review response", method: http.MethodPost,
			path: "/v1/moderation/cases/7/appeals/8/review", body: `{}`,
			nested:       "case",
			keys:         []string{"Decisions", "Actions", "Appeals"},
			nonEmptyKeys: []string{"ReportIDs"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Authorization", "Bearer secret")
			rec := httptest.NewRecorder()
			srv.routes().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			var response map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if tt.nested != "" {
				nestedValue := response[tt.nested]
				var ok bool
				response, ok = nestedValue.(map[string]any)
				if !ok {
					t.Fatalf("%s=%T, want object; body=%s",
						tt.nested, nestedValue, rec.Body.String())
				}
			}
			for _, key := range tt.keys {
				value, ok := response[key]
				if !ok {
					t.Fatalf("%s missing; body=%s", key, rec.Body.String())
				}
				items, ok := value.([]any)
				if !ok || len(items) != 0 {
					t.Fatalf("%s=%#v, want empty JSON array; body=%s",
						key, value, rec.Body.String())
				}
			}
			for _, key := range tt.nonEmptyKeys {
				value, ok := response[key]
				if !ok {
					t.Fatalf("%s missing; body=%s", key, rec.Body.String())
				}
				items, ok := value.([]any)
				if !ok || len(items) == 0 {
					t.Fatalf("%s=%#v, want non-empty JSON array; body=%s",
						key, value, rec.Body.String())
				}
			}
		})
	}
}

func TestAdminAPISetVerified(t *testing.T) {
	srv := &Server{token: "secret", svc: fakeService{}}
	req := httptest.NewRequest(http.MethodPost, "/v1/accounts/set-verified", strings.NewReader(`{"command_id":"c2","actor":"ops","reason":"official","dry_run":true,"user_id":1001,"verified":true}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"command_id":"c2"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestAdminAPISetChannelVerified(t *testing.T) {
	srv := &Server{token: "secret", svc: fakeService{}}
	req := httptest.NewRequest(http.MethodPost, "/v1/channels/set-verified", strings.NewReader(`{"command_id":"c3","actor":"ops","reason":"official","dry_run":true,"channel_id":2001,"verified":true}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"command_id":"c3"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

type fakeService struct{}

type captureFreezeService struct {
	fakeService
	req admin.SetAccountFrozenRequest
}

func (s *captureFreezeService) SetAccountFrozen(_ context.Context, req admin.SetAccountFrozenRequest) (admin.CommandResult, error) {
	s.req = req
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) SetAccountFrozen(_ context.Context, req admin.SetAccountFrozenRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) GrantPremium(_ context.Context, req admin.GrantPremiumRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) SetVerified(_ context.Context, req admin.SetVerifiedRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) SetChannelVerified(_ context.Context, req admin.SetChannelVerifiedRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) CreateBot(_ context.Context, req admin.CreateBotRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) CreateBroadcast(_ context.Context, req admin.CreateBroadcastRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) DeleteBot(_ context.Context, req admin.DeleteBotRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) ExportBotToken(_ context.Context, req admin.ExportBotTokenRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) SetUserFlags(_ context.Context, req admin.SetUserFlagsRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) SetChannelFlags(_ context.Context, req admin.SetChannelFlagsRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) SetSupport(_ context.Context, req admin.SetSupportRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) SetUsername(_ context.Context, req admin.SetUsernameRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) SetProfile(_ context.Context, req admin.SetProfileRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) SetPhone(_ context.Context, req admin.SetPhoneRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) SetLoginEmail(_ context.Context, req admin.SetLoginEmailRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) SetAccountAvatar(_ context.Context, req admin.SetAccountAvatarRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) ChannelAvatar(_ context.Context, _ int64) ([]byte, string, bool, error) {
	return nil, "", false, nil
}

func (fakeService) SetChannelAvatar(_ context.Context, req admin.SetChannelAvatarRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) SetUserColor(_ context.Context, req admin.SetUserColorRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) SetUserEmojiStatus(_ context.Context, req admin.SetUserEmojiStatusRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) SetChannelSettings(_ context.Context, req admin.SetChannelSettingsRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) SetChannelUsername(_ context.Context, req admin.SetChannelUsernameRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) SetChannelColor(_ context.Context, req admin.SetChannelColorRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) SetChannelEmojiStatus(_ context.Context, req admin.SetChannelEmojiStatusRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) RevokeSessions(context.Context, admin.RevokeSessionsRequest) (admin.CommandResult, error) {
	return admin.CommandResult{}, nil
}

func (fakeService) DeletePrivateMessages(context.Context, admin.DeletePrivateMessagesRequest) (admin.CommandResult, error) {
	return admin.CommandResult{}, nil
}

func (fakeService) DeletePrivateHistory(context.Context, admin.DeletePrivateHistoryRequest) (admin.CommandResult, error) {
	return admin.CommandResult{}, nil
}

func (fakeService) AccountAvatar(context.Context, int64) ([]byte, string, bool, error) {
	return nil, "", false, nil
}

func (fakeService) SetStickerSetArchived(_ context.Context, req admin.SetStickerSetArchivedRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) SetStickerSetSortOrder(_ context.Context, req admin.SetStickerSetSortOrderRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) RenameStickerSet(_ context.Context, req admin.RenameStickerSetRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) DeleteStickerSet(_ context.Context, req admin.DeleteStickerSetRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) CreateStickerSet(_ context.Context, req admin.CreateStickerSetRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) AddStickerToSet(_ context.Context, req admin.AddStickerToSetRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) RemoveStickerFromSet(_ context.Context, req admin.RemoveStickerFromSetRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) StickerDocumentAnimation(context.Context, int64) ([]byte, string, bool, error) {
	return nil, "", false, nil
}

func (fakeService) GifCatalogDocumentPreview(context.Context, int64) ([]byte, string, bool, error) {
	return nil, "", false, nil
}

func (fakeService) CreateGifCatalogEntry(_ context.Context, req admin.CreateGifCatalogEntryRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) SetGifCatalogEnabled(_ context.Context, req admin.SetGifCatalogEnabledRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) SetGifCatalogSortOrder(_ context.Context, req admin.SetGifCatalogSortOrderRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) SetGifCatalogCategory(_ context.Context, req admin.SetGifCatalogCategoryRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) AutoCategorizeGifCatalog(_ context.Context, req admin.AutoCategorizeGifCatalogRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) DeleteUncategorizedGifs(_ context.Context, req admin.DeleteUncategorizedGifsRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) DeleteGifCatalogEntry(_ context.Context, req admin.DeleteGifCatalogEntryRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) ManualPurgeStorage(_ context.Context, req admin.ManualPurgeStorageRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) EmojiAnimation(context.Context, int64) ([]byte, bool, error) {
	return []byte(`{"v":"5.7","w":100,"h":100}`), true, nil
}

func (fakeService) ModerationCases(context.Context, domain.ModerationCaseFilter) ([]domain.ModerationCase, error) {
	return nil, nil
}

func (fakeService) ModerationCase(context.Context, int64) (domain.ModerationCaseDetail, bool, error) {
	return domain.ModerationCaseDetail{}, false, nil
}

func (fakeService) ModerationReport(context.Context, int64) (domain.ModerationReport, bool, error) {
	return domain.ModerationReport{}, false, nil
}

func (fakeService) ClaimModerationCase(context.Context, int64, int64, string) (domain.ModerationCase, error) {
	return domain.ModerationCase{}, nil
}

func (fakeService) DecideModerationCase(context.Context, domain.ModerationDecisionRequest) (domain.ModerationCaseDetail, bool, error) {
	return domain.ModerationCaseDetail{}, true, nil
}

func (fakeService) SubmitModerationAppeal(context.Context, int64, int64, string) (domain.ModerationAppeal, bool, error) {
	return domain.ModerationAppeal{}, true, nil
}

func (fakeService) ReviewModerationAppeal(context.Context, domain.ModerationDecisionRequest) (domain.ModerationCaseDetail, bool, error) {
	return domain.ModerationCaseDetail{}, true, nil
}

type captureCollectibleUsernameService struct {
	fakeService
	mint     admin.MintCollectibleUsernameRequest
	transfer admin.TransferCollectibleUsernameRequest
	revoke   admin.RevokeCollectibleUsernameRequest
	del      admin.DeleteCollectibleUsernameRequest
	filter   domain.CollectibleUsernameFilter
	assetID  int64
}

func (s *captureCollectibleUsernameService) MintCollectibleUsername(_ context.Context, req admin.MintCollectibleUsernameRequest) (admin.CommandResult, error) {
	s.mint = req
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (s *captureCollectibleUsernameService) TransferCollectibleUsername(_ context.Context, req admin.TransferCollectibleUsernameRequest) (admin.CommandResult, error) {
	s.transfer = req
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (s *captureCollectibleUsernameService) RevokeCollectibleUsername(_ context.Context, req admin.RevokeCollectibleUsernameRequest) (admin.CommandResult, error) {
	s.revoke = req
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (s *captureCollectibleUsernameService) DeleteCollectibleUsername(_ context.Context, req admin.DeleteCollectibleUsernameRequest) (admin.CommandResult, error) {
	s.del = req
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (s *captureCollectibleUsernameService) CollectibleUsernames(_ context.Context, filter domain.CollectibleUsernameFilter) ([]domain.CollectibleUsername, error) {
	s.filter = filter
	return []domain.CollectibleUsername{maxInt64Collectible()}, nil
}

func (s *captureCollectibleUsernameService) CollectibleUsernameByID(_ context.Context, id int64) (domain.CollectibleUsername, error) {
	s.assetID = id
	asset := maxInt64Collectible()
	asset.ID = id
	return asset, nil
}

func (s *captureCollectibleUsernameService) CollectibleUsernameTransfers(_ context.Context, collectibleID int64, _ int) ([]domain.CollectibleUsernameTransfer, error) {
	return []domain.CollectibleUsernameTransfer{{
		ID:            9223372036854775807,
		CollectibleID: collectibleID,
		Kind:          domain.CollectibleUsernameKindMint,
		To:            domain.Peer{Type: domain.PeerTypeUser, ID: 1001},
		Currency:      domain.CollectibleCurrencyTON,
		Amount:        9223372036854775807,
		Actor:         "ops",
	}}, nil
}

func maxInt64Collectible() domain.CollectibleUsername {
	return domain.CollectibleUsername{
		ID:             9223372036854775807,
		Username:       "durov",
		Status:         domain.CollectibleUsernameStatusOwned,
		Owner:          domain.Peer{Type: domain.PeerTypeUser, ID: 1001},
		Currency:       domain.CollectibleCurrencyTON,
		Amount:         9223372036854775807,
		CryptoCurrency: domain.CollectibleCryptoCurrencyTON,
		CryptoAmount:   9223372036854775807,
		Version:        9223372036854775807,
	}
}

func TestAdminAPICollectibleUsernameCommandsRequireToken(t *testing.T) {
	srv := &Server{token: "secret", svc: fakeService{}}
	for _, path := range []string{
		"/v1/collectible-usernames/mint",
		"/v1/collectible-usernames/transfer",
		"/v1/collectible-usernames/revoke",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		srv.routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d, want 401", path, rec.Code)
		}
	}
	for _, path := range []string{
		"/v1/collectible-usernames",
		"/v1/collectible-usernames/7",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d, want 401", path, rec.Code)
		}
	}
}

func TestAdminAPIMintCollectibleUsernameForwardsExactInt64AndDryRun(t *testing.T) {
	const maxInt64 = int64(9223372036854775807)
	svc := &captureCollectibleUsernameService{}
	srv := &Server{token: "secret", svc: svc}
	req := httptest.NewRequest(http.MethodPost, "/v1/collectible-usernames/mint", strings.NewReader(`{
		"command_id":"mint-1","actor":"ops","reason":"fragment import","dry_run":true,
		"username":"durov","owner_user_id":"1001","currency":"TON","amount":"9223372036854775807",
		"crypto_currency":"TON","crypto_amount":"250000000000",
		"url":"https://fragment.example/durov","purchase_date":1700000000
	}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"command_id":"mint-1"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"dry_run":true`) {
		t.Fatalf("dry-run was not propagated: %s", rec.Body.String())
	}
	if svc.mint.Username != "durov" || svc.mint.OwnerUserID != 1001 || svc.mint.Amount != maxInt64 ||
		svc.mint.CryptoAmount != 250000000000 || svc.mint.PurchaseDate != 1700000000 || !svc.mint.DryRun {
		t.Fatalf("decoded mint request = %+v", svc.mint)
	}
}

func TestAdminAPITransferAndRevokeCollectibleUsername(t *testing.T) {
	svc := &captureCollectibleUsernameService{}
	srv := &Server{token: "secret", svc: svc}
	transfer := httptest.NewRequest(http.MethodPost, "/v1/collectible-usernames/transfer", strings.NewReader(
		`{"command_id":"t-1","actor":"ops","reason":"sold","username":"durov","to_channel_id":"2002"}`))
	transfer.Header.Set("Authorization", "Bearer secret")
	transferRec := httptest.NewRecorder()
	srv.routes().ServeHTTP(transferRec, transfer)
	if transferRec.Code != http.StatusOK || svc.transfer.ToChannelID != 2002 || svc.transfer.Username != "durov" {
		t.Fatalf("transfer status=%d request=%+v", transferRec.Code, svc.transfer)
	}

	revoke := httptest.NewRequest(http.MethodPost, "/v1/collectible-usernames/revoke", strings.NewReader(
		`{"command_id":"r-1","actor":"ops","reason":"fraud","username":"durov","burn":true}`))
	revoke.Header.Set("Authorization", "Bearer secret")
	revokeRec := httptest.NewRecorder()
	srv.routes().ServeHTTP(revokeRec, revoke)
	if revokeRec.Code != http.StatusOK || !svc.revoke.Burn || svc.revoke.CommandID != "r-1" {
		t.Fatalf("revoke status=%d request=%+v", revokeRec.Code, svc.revoke)
	}
}

func TestAdminAPICollectibleUsernameReadsUseDecimalStrings(t *testing.T) {
	svc := &captureCollectibleUsernameService{}
	srv := &Server{token: "secret", svc: svc}
	list := httptest.NewRequest(http.MethodGet,
		"/v1/collectible-usernames?status=owned&owner_user_id=1001&q=%40Durov&limit=25&before_id=42", nil)
	list.Header.Set("Authorization", "Bearer secret")
	listRec := httptest.NewRecorder()
	srv.routes().ServeHTTP(listRec, list)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	if svc.filter.Status != domain.CollectibleUsernameStatusOwned ||
		svc.filter.Owner != (domain.Peer{Type: domain.PeerTypeUser, ID: 1001}) ||
		svc.filter.Query != "@Durov" || svc.filter.Limit != 25 || svc.filter.BeforeID != 42 {
		t.Fatalf("collectible filter = %+v", svc.filter)
	}
	if !strings.Contains(listRec.Body.String(), `"id":"9223372036854775807"`) ||
		!strings.Contains(listRec.Body.String(), `"amount":"9223372036854775807"`) {
		t.Fatalf("list body lost int64 precision: %s", listRec.Body.String())
	}

	detail := httptest.NewRequest(http.MethodGet, "/v1/collectible-usernames/77", nil)
	detail.Header.Set("Authorization", "Bearer secret")
	detailRec := httptest.NewRecorder()
	srv.routes().ServeHTTP(detailRec, detail)
	if detailRec.Code != http.StatusOK || svc.assetID != 77 {
		t.Fatalf("detail status=%d assetID=%d body=%s", detailRec.Code, svc.assetID, detailRec.Body.String())
	}
	var payload struct {
		Asset     map[string]any   `json:"asset"`
		Transfers []map[string]any `json:"transfers"`
	}
	if err := json.Unmarshal(detailRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if payload.Asset["id"] != "77" || len(payload.Transfers) != 1 ||
		payload.Transfers[0]["amount"] != "9223372036854775807" ||
		payload.Transfers[0]["collectible_id"] != "77" {
		t.Fatalf("detail payload = %+v", payload)
	}
}

func TestAdminAPIMissingCollectibleReportsCodedError(t *testing.T) {
	srv := &Server{token: "secret", svc: fakeService{}}
	asset := httptest.NewRequest(http.MethodGet, "/v1/collectible-usernames/5", nil)
	asset.Header.Set("Authorization", "Bearer secret")
	assetRec := httptest.NewRecorder()
	srv.routes().ServeHTTP(assetRec, asset)
	if assetRec.Code != http.StatusNotFound ||
		!strings.Contains(assetRec.Body.String(), `"code":"`+admin.CodeCollectibleNotFound+`"`) {
		t.Fatalf("missing asset status=%d body=%s", assetRec.Code, assetRec.Body.String())
	}
}

func (fakeService) MintCollectibleUsername(_ context.Context, req admin.MintCollectibleUsernameRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) TransferCollectibleUsername(_ context.Context, req admin.TransferCollectibleUsernameRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) RevokeCollectibleUsername(_ context.Context, req admin.RevokeCollectibleUsernameRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) DeleteCollectibleUsername(_ context.Context, req admin.DeleteCollectibleUsernameRequest) (admin.CommandResult, error) {
	return admin.CommandResult{CommandID: req.CommandID, Status: "completed", DryRun: req.DryRun}, nil
}

func (fakeService) CollectibleUsernames(context.Context, domain.CollectibleUsernameFilter) ([]domain.CollectibleUsername, error) {
	return nil, nil
}

func (fakeService) CollectibleUsernameByID(context.Context, int64) (domain.CollectibleUsername, error) {
	return domain.CollectibleUsername{}, domain.ErrCollectibleUsernameNotFound
}

func (fakeService) CollectibleUsernameTransfers(context.Context, int64, int) ([]domain.CollectibleUsernameTransfer, error) {
	return nil, nil
}
