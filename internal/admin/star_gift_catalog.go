package admin

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"telesrv/internal/app/giftpack"
	"telesrv/internal/domain"
	"telesrv/internal/seed/giftpacks"
)

// CreateStarGiftCatalogEntryRequest authors a new StarGift (GiftID == 0) or a
// new revision of an existing one (GiftID > 0) from an uploaded animation.
// Only the plain-gift fields are exposed here -- auction and collectible
// (unique-attribute) authoring are a separate, unreviewed surface (see
// StarGiftCatalogService's doc comment) and are not settable through this
// request; Auction always publishes false.
type CreateStarGiftCatalogEntryRequest struct {
	CommandMeta
	GiftID            int64  `json:"gift_id,string"`
	Title             string `json:"title"`
	Stars             int64  `json:"stars,string"`
	ConvertStars      int64  `json:"convert_stars,string"`
	Enabled           bool   `json:"enabled"`
	SortOrder         int    `json:"sort_order"`
	Limited           bool   `json:"limited"`
	AvailabilityTotal int    `json:"availability_total"`
	FileName          string `json:"file_name"`
	Data              []byte `json:"-"`
}

type SetStarGiftCatalogEnabledRequest struct {
	CommandMeta
	GiftID  int64 `json:"gift_id,string"`
	Enabled bool  `json:"enabled"`
}

type SetStarGiftCatalogSortOrderRequest struct {
	CommandMeta
	GiftID    int64 `json:"gift_id,string"`
	SortOrder int   `json:"sort_order"`
}

// StarGiftCatalogAnimation returns the active revision's Lottie JSON for the
// admin panel's own preview -- never the raw client-facing catalog, which is
// enabled-only and shaped for the protocol layer, not an operator.
func (s *Service) StarGiftCatalogAnimation(ctx context.Context, giftID int64) ([]byte, bool, error) {
	if s == nil || s.starGifts == nil || giftID <= 0 {
		return nil, false, nil
	}
	return s.starGifts.AnimationJSON(ctx, giftID)
}

// CreateStarGiftCatalogEntry validates and normalizes the uploaded animation
// and the authoring fields, then publishes them as one new catalog revision.
// Never a dry-run no-op like GifCatalog's create: PrepareAnimation still runs
// so an operator finds out a bad .tgs is bad before spending a real upload,
// but nothing is written until DryRun is false.
func (s *Service) CreateStarGiftCatalogEntry(ctx context.Context, req CreateStarGiftCatalogEntryRequest) (CommandResult, error) {
	if s == nil || s.starGifts == nil {
		return CommandResult{}, domain.ErrStarGiftInvalid
	}
	if strings.TrimSpace(req.Title) == "" {
		return CommandResult{}, domain.ErrStarGiftInvalid
	}
	animation, err := s.starGifts.PrepareAnimation(req.FileName, req.Data)
	if err != nil {
		return CommandResult{}, err
	}
	write := domain.StarGiftCatalogWrite{
		GiftID:            req.GiftID,
		Title:             req.Title,
		Stars:             req.Stars,
		ConvertStars:      req.ConvertStars,
		Enabled:           req.Enabled,
		SortOrder:         req.SortOrder,
		Animation:         animation,
		Actor:             req.Actor,
		CommandID:         req.CommandID,
		Limited:           req.Limited,
		AvailabilityTotal: req.AvailabilityTotal,
	}
	now := int(s.now().Unix())
	if err := write.ValidateLifecycleAuthoring(now); err != nil {
		return CommandResult{}, err
	}
	write.NormalizeLifecycleAuthoring(now)
	return s.runCommand(ctx, req.CommandMeta, ActionCreateStarGiftCatalogEntry, 0, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{
			"gift_id": strconv.FormatInt(req.GiftID, 10),
			"title":   req.Title, "stars": strconv.FormatInt(req.Stars, 10),
			"file_name": req.FileName, "bytes": len(req.Data),
		}
		if req.DryRun {
			return CommandResult{Message: "star gift catalog entry validated", Details: details}, nil
		}
		entry, err := s.starGifts.CreateCatalogRevision(ctx, write)
		if err != nil {
			return CommandResult{Details: details}, err
		}
		details["gift_id"] = strconv.FormatInt(entry.Gift.ID, 10)
		details["revision"] = entry.Revision
		return CommandResult{Message: "star gift catalog entry created", Details: details}, nil
	})
}

// ImportGiftPackRequest carries one uploaded pack .zip (pack.json at the
// archive root plus the assets it references by relative path -- see
// internal/app/giftpack's package doc). PackZip is populated server-side
// from the multipart upload, not from the JSON body.
type ImportGiftPackRequest struct {
	CommandMeta
	PackZip []byte `json:"-"`
}

// ImportGiftPack parses and imports a community-authored gift pack. Gifts
// already present by title are skipped, not duplicated (see
// internal/app/giftpack.Import), so re-running an import -- including a
// pack that only adds a few new gifts to one already imported before -- is
// always safe.
func (s *Service) ImportGiftPack(ctx context.Context, req ImportGiftPackRequest) (CommandResult, error) {
	if s == nil || s.starGifts == nil {
		return CommandResult{}, domain.ErrStarGiftInvalid
	}
	if len(req.PackZip) == 0 {
		return CommandResult{}, fmt.Errorf("pack zip is required")
	}
	assets, err := giftpack.NewZipAssetResolver(req.PackZip)
	if err != nil {
		return CommandResult{}, err
	}
	manifestData, err := assets.Manifest()
	if err != nil {
		return CommandResult{}, fmt.Errorf("pack.json: %w", err)
	}
	manifest, err := giftpack.ParseManifest(manifestData)
	if err != nil {
		return CommandResult{}, err
	}
	return s.runCommand(ctx, req.CommandMeta, ActionImportGiftPack, 0, domain.Peer{}, req, func() (CommandResult, error) {
		result, err := giftpack.Import(ctx, s.starGifts, manifest, assets, giftpack.ImportOptions{DryRun: req.DryRun, Now: s.now})
		if err != nil {
			return CommandResult{}, err
		}
		return CommandResult{
			Message: fmt.Sprintf("gift pack %q processed", manifest.PackName),
			Details: map[string]any{"pack_name": manifest.PackName, "gifts": result.Gifts},
		}, nil
	})
}

type ImportBuiltinGiftPackRequest struct {
	CommandMeta
	PackID string `json:"pack_id"`
}

// ImportBuiltinGiftPack imports one of OwpenGram's built-in packs
// (internal/seed/giftpacks) through the exact same path ImportGiftPack uses
// for an uploaded pack.
func (s *Service) ImportBuiltinGiftPack(ctx context.Context, req ImportBuiltinGiftPackRequest) (CommandResult, error) {
	if s == nil || s.starGifts == nil {
		return CommandResult{}, domain.ErrStarGiftInvalid
	}
	manifest, assets, ok := giftpacks.Manifest(req.PackID)
	if !ok {
		return CommandResult{}, fmt.Errorf("unknown built-in gift pack %q", req.PackID)
	}
	return s.runCommand(ctx, req.CommandMeta, ActionImportBuiltinGiftPack, 0, domain.Peer{}, req, func() (CommandResult, error) {
		result, err := giftpack.Import(ctx, s.starGifts, manifest, assets, giftpack.ImportOptions{DryRun: req.DryRun, Now: s.now})
		if err != nil {
			return CommandResult{}, err
		}
		return CommandResult{
			Message: fmt.Sprintf("gift pack %q processed", manifest.PackName),
			Details: map[string]any{"pack_id": req.PackID, "pack_name": manifest.PackName, "gifts": result.Gifts},
		}, nil
	})
}

// BuiltinGiftPacks lists the built-in packs for the admin panel. A pure read
// of static content -- no s.starGifts dependency.
func (s *Service) BuiltinGiftPacks() []giftpacks.PackSummary {
	return giftpacks.List()
}

// BuiltinGiftPackAnimation returns one built-in gift's Lottie JSON, for the
// pack preview before import.
func (s *Service) BuiltinGiftPackAnimation(packID, slug string) ([]byte, bool) {
	return giftpacks.Animation(packID, slug)
}

func (s *Service) SetStarGiftCatalogEnabled(ctx context.Context, req SetStarGiftCatalogEnabledRequest) (CommandResult, error) {
	if s == nil || s.starGifts == nil || req.GiftID <= 0 {
		return CommandResult{}, domain.ErrStarGiftInvalid
	}
	return s.runCommand(ctx, req.CommandMeta, ActionSetStarGiftCatalogEnabled, 0, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{"gift_id": strconv.FormatInt(req.GiftID, 10), "enabled": req.Enabled}
		if req.DryRun {
			return CommandResult{Message: "star gift catalog state change validated", Details: details}, nil
		}
		changed, err := s.starGifts.SetCatalogEnabled(ctx, req.GiftID, req.Enabled)
		details["changed"] = changed
		return CommandResult{Message: "star gift catalog state updated", Details: details}, err
	})
}

func (s *Service) SetStarGiftCatalogSortOrder(ctx context.Context, req SetStarGiftCatalogSortOrderRequest) (CommandResult, error) {
	if s == nil || s.starGifts == nil || req.GiftID <= 0 {
		return CommandResult{}, domain.ErrStarGiftInvalid
	}
	return s.runCommand(ctx, req.CommandMeta, ActionSetStarGiftCatalogSortOrder, 0, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{"gift_id": strconv.FormatInt(req.GiftID, 10), "sort_order": req.SortOrder}
		if req.DryRun {
			return CommandResult{Message: "star gift catalog order change validated", Details: details}, nil
		}
		changed, err := s.starGifts.SetCatalogSortOrder(ctx, req.GiftID, req.SortOrder)
		details["changed"] = changed
		return CommandResult{Message: "star gift catalog order updated", Details: details}, err
	})
}

// StarGiftCollectibles is the currently-published attribute pool for one
// gift, for the admin panel's "Attribute pool" view -- found is false when
// nothing has been published yet, not an error.
func (s *Service) StarGiftCollectibles(ctx context.Context, giftID int64) (domain.StarGiftUpgradePreview, bool, error) {
	if s == nil || s.starGifts == nil || giftID <= 0 {
		return domain.StarGiftUpgradePreview{}, false, nil
	}
	return s.starGifts.CollectiblePreview(ctx, giftID)
}

func (s *Service) StarGiftCollectibleAnimation(ctx context.Context, giftID int64, kind domain.StarGiftCollectibleAttributeKind, attributeID int64) ([]byte, bool, error) {
	if s == nil || s.starGifts == nil || giftID <= 0 || attributeID <= 0 {
		return nil, false, nil
	}
	if kind != domain.StarGiftCollectibleModel && kind != domain.StarGiftCollectiblePattern {
		return nil, false, domain.ErrStarGiftCollectibleInvalid
	}
	return s.starGifts.CollectibleAnimationJSON(ctx, giftID, kind, attributeID)
}

// StarGiftCollectibleAnimationUpload is one uploaded model/pattern attribute
// in a collectible pool draft. FileName/Data carry the raw upload;
// ContentSHA is filled in from the normalized animation once PrepareAnimation
// has run, so a retried command with different file bytes is rejected by the
// idempotency boundary even though raw bytes are never themselves audited.
type StarGiftCollectibleAnimationUpload struct {
	Name           string `json:"name"`
	RarityPermille int    `json:"rarity_permille"`
	SortOrder      int    `json:"sort_order"`
	FileKey        string `json:"file_key"`
	FileName       string `json:"file_name,omitempty"`
	ContentSHA     string `json:"content_sha256,omitempty"`
	Data           []byte `json:"-"`
}

type StarGiftCollectibleBackdropInput struct {
	Name           string `json:"name"`
	BackdropID     int    `json:"backdrop_id"`
	CenterColor    int    `json:"center_color"`
	EdgeColor      int    `json:"edge_color"`
	PatternColor   int    `json:"pattern_color"`
	TextColor      int    `json:"text_color"`
	RarityPermille int    `json:"rarity_permille"`
	SortOrder      int    `json:"sort_order"`
}

// PublishStarGiftCollectiblesRequest authors a new immutable collectible
// (unique-upgrade) attribute pool for a plain gift already in the catalog.
// Models and Patterns each need their own uploaded animation; Backdrops are
// pure color data with no file.
type PublishStarGiftCollectiblesRequest struct {
	CommandMeta
	GiftID       int64                                `json:"gift_id,string"`
	UpgradeStars int64                                `json:"upgrade_stars,string"`
	SupplyTotal  int                                  `json:"supply_total"`
	SlugPrefix   string                               `json:"slug_prefix"`
	Models       []StarGiftCollectibleAnimationUpload `json:"models"`
	Patterns     []StarGiftCollectibleAnimationUpload `json:"patterns"`
	Backdrops    []StarGiftCollectibleBackdropInput   `json:"backdrops"`
}

func collectibleAttrPresent(attrs []domain.StarGiftCollectibleAttribute, id int64) bool {
	for _, attr := range attrs {
		if attr.ID == id {
			return true
		}
	}
	return false
}

func collectibleUploadDetails(uploads []StarGiftCollectibleAnimationUpload) []map[string]any {
	details := make([]map[string]any, 0, len(uploads))
	for _, upload := range uploads {
		details = append(details, map[string]any{
			"name": strings.TrimSpace(upload.Name), "rarity_permille": upload.RarityPermille,
			"sort_order": upload.SortOrder, "source_name": upload.FileName, "sha256": upload.ContentSHA,
		})
	}
	return details
}

func (s *Service) PublishStarGiftCollectibles(ctx context.Context, req PublishStarGiftCollectiblesRequest) (CommandResult, error) {
	if s == nil || s.starGifts == nil {
		return CommandResult{}, fmt.Errorf("star gift service is not configured")
	}
	toAttributes := func(kind domain.StarGiftCollectibleAttributeKind, uploads []StarGiftCollectibleAnimationUpload) ([]domain.StarGiftCollectibleAttribute, error) {
		attributes := make([]domain.StarGiftCollectibleAttribute, len(uploads))
		for i := range uploads {
			animation, err := s.starGifts.PrepareAnimation(uploads[i].FileName, uploads[i].Data)
			if err != nil {
				return nil, fmt.Errorf("prepare %s %q: %w", kind, uploads[i].Name, err)
			}
			uploads[i].ContentSHA = hex.EncodeToString(animation.SHA256)
			attributes[i] = domain.StarGiftCollectibleAttribute{
				Kind: kind, Name: strings.TrimSpace(uploads[i].Name), RarityKind: domain.StarGiftRarityPermille,
				RarityPermille: uploads[i].RarityPermille,
				SortOrder:      uploads[i].SortOrder, Animation: &animation,
			}
		}
		return attributes, nil
	}
	models, err := toAttributes(domain.StarGiftCollectibleModel, req.Models)
	if err != nil {
		return CommandResult{}, err
	}
	patterns, err := toAttributes(domain.StarGiftCollectiblePattern, req.Patterns)
	if err != nil {
		return CommandResult{}, err
	}
	backdrops := make([]domain.StarGiftCollectibleAttribute, len(req.Backdrops))
	for i, backdrop := range req.Backdrops {
		backdrops[i] = domain.StarGiftCollectibleAttribute{
			Kind: domain.StarGiftCollectibleBackdrop, Name: strings.TrimSpace(backdrop.Name), BackdropID: backdrop.BackdropID,
			CenterColor: backdrop.CenterColor, EdgeColor: backdrop.EdgeColor, PatternColor: backdrop.PatternColor,
			TextColor: backdrop.TextColor, RarityKind: domain.StarGiftRarityPermille,
			RarityPermille: backdrop.RarityPermille, SortOrder: backdrop.SortOrder,
		}
	}
	write := domain.StarGiftCollectibleWrite{
		GiftID: req.GiftID, UpgradeStars: req.UpgradeStars, SupplyTotal: req.SupplyTotal,
		SlugPrefix: strings.ToLower(strings.TrimSpace(req.SlugPrefix)), Models: models, Patterns: patterns, Backdrops: backdrops,
		Actor: req.Actor, CommandID: req.CommandID,
	}
	if err := domain.ValidateStarGiftCollectibleDraft(write); err != nil {
		return CommandResult{}, err
	}
	for i := range req.Models {
		req.Models[i].ContentSHA = hex.EncodeToString(models[i].Animation.SHA256)
	}
	for i := range req.Patterns {
		req.Patterns[i].ContentSHA = hex.EncodeToString(patterns[i].Animation.SHA256)
	}
	return s.runCommand(ctx, req.CommandMeta, ActionPublishStarGiftCollectibles, 0, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{
			"gift_id": strconv.FormatInt(req.GiftID, 10), "upgrade_stars": strconv.FormatInt(req.UpgradeStars, 10),
			"supply_total": req.SupplyTotal,
			"slug_prefix":  write.SlugPrefix, "models": collectibleUploadDetails(req.Models),
			"patterns": collectibleUploadDetails(req.Patterns), "backdrops": len(req.Backdrops),
		}
		if req.DryRun {
			return CommandResult{Message: "star gift collectible pool validated", Details: details}, nil
		}
		revision, err := s.starGifts.CreateCollectibleRevision(ctx, write)
		if err != nil {
			return CommandResult{Details: details}, err
		}
		details["revision_id"] = strconv.FormatInt(revision.ID, 10)
		details["revision"] = revision.Revision
		details["published"] = revision.Published
		return CommandResult{Message: "star gift collectible pool published", Details: details}, nil
	})
}

// GiveStarGiftRequest grants a catalog gift to a recipient (user or channel)
// from the official system account 777000 at no charge. Exactly one of
// UserID / ChannelID identifies the recipient.
type GiveStarGiftRequest struct {
	CommandMeta
	SenderUserID        int64  `json:"sender_user_id,string"`
	UserID              int64  `json:"user_id,string"`
	ChannelID           int64  `json:"channel_id,string"`
	GiftID              int64  `json:"gift_id,string"`
	HideName            bool   `json:"hide_name"`
	Message             string `json:"message"`
	Upgrade             bool   `json:"upgrade"`
	ModelAttributeID    int64  `json:"model_attribute_id,string"`
	PatternAttributeID  int64  `json:"pattern_attribute_id,string"`
	BackdropAttributeID int64  `json:"backdrop_attribute_id,string"`
}

// GiveStarGift grants a catalog gift to a recipient (user or channel) from
// the official system account 777000 without charging any Stars. Delivery
// reuses the standard gift path via StarGiftCatalogService.GrantUnique.
func (s *Service) GiveStarGift(ctx context.Context, req GiveStarGiftRequest) (CommandResult, error) {
	if req.GiftID <= 0 {
		return CommandResult{}, fmt.Errorf("gift_id is required")
	}
	if (req.UserID > 0) == (req.ChannelID > 0) {
		return CommandResult{}, fmt.Errorf("exactly one of user_id or channel_id is required")
	}
	if s == nil || s.starGifts == nil {
		return CommandResult{}, fmt.Errorf("star gift service is not configured")
	}
	sender := req.SenderUserID
	if sender <= 0 {
		sender = domain.OfficialSystemUserID
	}
	if sender != domain.OfficialSystemUserID {
		return CommandResult{}, fmt.Errorf("gift sender must be the official system account")
	}
	req.Message = strings.TrimSpace(req.Message)
	if len([]rune(req.Message)) > 128 {
		return CommandResult{}, fmt.Errorf("gift message must be <= 128 characters")
	}
	var recipient domain.Peer
	if req.ChannelID > 0 {
		recipient = domain.Peer{Type: domain.PeerTypeChannel, ID: req.ChannelID}
	} else {
		recipient = domain.Peer{Type: domain.PeerTypeUser, ID: req.UserID}
	}
	if req.Upgrade && recipient.Type != domain.PeerTypeUser {
		return CommandResult{}, fmt.Errorf("upgraded gift delivery is supported for user recipients only")
	}
	if !req.Upgrade && (req.ModelAttributeID > 0 || req.PatternAttributeID > 0 || req.BackdropAttributeID > 0) {
		return CommandResult{}, fmt.Errorf("collectible attributes require upgrade")
	}
	return s.runCommand(ctx, req.CommandMeta, ActionGiveStarGift, req.UserID, recipient, req, func() (CommandResult, error) {
		details := map[string]any{
			"sender_user_id": strconv.FormatInt(sender, 10),
			"gift_id":        strconv.FormatInt(req.GiftID, 10),
			"recipient_type": string(recipient.Type),
			"recipient_id":   strconv.FormatInt(recipient.ID, 10),
			"hide_name":      req.HideName,
			"upgrade":        req.Upgrade,
		}
		if req.Message != "" {
			details["message"] = req.Message
		}
		gift, found, err := s.starGifts.GiftByID(ctx, req.GiftID)
		if err != nil {
			return CommandResult{}, err
		}
		if !found {
			return CommandResult{}, fmt.Errorf("gift %d not found", req.GiftID)
		}
		details["gift_title"] = gift.Title
		details["gift_stars"] = strconv.FormatInt(gift.Stars, 10)
		if req.Upgrade {
			preview, ok, err := s.starGifts.CollectiblePreview(ctx, req.GiftID)
			if err != nil {
				return CommandResult{}, err
			}
			if !ok || preview.UpgradeStars <= 0 {
				return CommandResult{}, fmt.Errorf("gift %d has no published collectible upgrade", req.GiftID)
			}
			if preview.Issued >= preview.SupplyTotal {
				return CommandResult{}, fmt.Errorf("gift %d collectible supply is exhausted", req.GiftID)
			}
			if req.ModelAttributeID > 0 && !collectibleAttrPresent(preview.Models, req.ModelAttributeID) {
				return CommandResult{}, fmt.Errorf("model attribute %d is not part of gift %d", req.ModelAttributeID, req.GiftID)
			}
			if req.PatternAttributeID > 0 && !collectibleAttrPresent(preview.Patterns, req.PatternAttributeID) {
				return CommandResult{}, fmt.Errorf("pattern attribute %d is not part of gift %d", req.PatternAttributeID, req.GiftID)
			}
			if req.BackdropAttributeID > 0 && !collectibleAttrPresent(preview.Backdrops, req.BackdropAttributeID) {
				return CommandResult{}, fmt.Errorf("backdrop attribute %d is not part of gift %d", req.BackdropAttributeID, req.GiftID)
			}
			details["collectible_supply_total"] = preview.SupplyTotal
			details["collectible_issued"] = preview.Issued
			if req.ModelAttributeID > 0 {
				details["model_attribute_id"] = strconv.FormatInt(req.ModelAttributeID, 10)
			}
			if req.PatternAttributeID > 0 {
				details["pattern_attribute_id"] = strconv.FormatInt(req.PatternAttributeID, 10)
			}
			if req.BackdropAttributeID > 0 {
				details["backdrop_attribute_id"] = strconv.FormatInt(req.BackdropAttributeID, 10)
			}
		}
		if req.DryRun {
			return CommandResult{Message: "dry-run completed", Details: details}, nil
		}
		if _, err := s.starGifts.GrantUnique(ctx, domain.AdminStarGiftGrant{
			SenderID:            sender,
			Recipient:           recipient,
			GiftID:              req.GiftID,
			HideName:            req.HideName,
			Message:             req.Message,
			Upgrade:             req.Upgrade,
			CommandKey:          "admin-gift:" + req.CommandID,
			ModelAttributeID:    req.ModelAttributeID,
			PatternAttributeID:  req.PatternAttributeID,
			BackdropAttributeID: req.BackdropAttributeID,
		}); err != nil {
			return CommandResult{}, err
		}
		msg := "gift granted"
		if req.Upgrade {
			msg = "collectible gift granted"
		}
		return CommandResult{Message: msg, Details: details}, nil
	})
}
