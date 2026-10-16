package files

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"telesrv/internal/domain"
)

// 本文件实现从外部导出的 reaction / sticker 资源目录导入媒体种子：
// JSON 元数据（外部导出 document id/access_hash/file_reference/attributes/thumbs）落 documents /
// sticker_sets / available_reactions 表，二进制 .tgs/.webp/缩略图落 blob backend。
// dc_id 统一重写为本 server 的 DC，使客户端从本 DC 下载。导入幂等（已存在则跳过）。

// SeedStats 汇报一次种子导入结果。
type SeedStats struct {
	Reactions   int
	StickerSets int
	Effects     int
	Documents   int
	Blobs       int
	Skipped     bool
}

// SeedMedia 从导出根目录导入 reaction 与 sticker 资源。maxRegularSets<=0 表示不限。
func (s *Service) SeedMedia(ctx context.Context, root string, maxRegularSets int) (SeedStats, error) {
	var stats SeedStats
	if root == "" {
		stats.Skipped = true
		return stats, nil
	}
	if _, err := os.Stat(root); err != nil {
		// 目录不存在：跳过而非失败（开发机可能未放资源）。但显式 WARN——否则「配置的 seed 目录
		// 不存在 → 静默不导入贴纸/reaction」会被埋没（DB 已有旧数据时尤其隐蔽，表现为客户端反复
		// 拉取未 seed 的集）。配置 TELESRV_STICKER_SEED_DIR 指向真实导出目录即可。
		if s.log != nil {
			s.log.Warn("sticker/reaction seed directory does not exist, skipping media seed import (set TELESRV_STICKER_SEED_DIR)",
				zap.String("dir", root), zap.Error(err))
		}
		stats.Skipped = true
		return stats, nil
	}

	phaseStarted := time.Now()
	phaseBefore := stats
	// reactions
	if n, err := s.media.CountAvailableReactions(ctx); err != nil {
		return stats, err
	} else if n == 0 {
		if err := s.seedReactions(ctx, root, &stats); err != nil {
			return stats, fmt.Errorf("seed reactions: %w", err)
		}
	} else if incomplete, err := s.availableReactionSeedNeedsRepair(ctx); err != nil {
		return stats, err
	} else if incomplete {
		if err := s.seedReactions(ctx, root, &stats); err != nil {
			return stats, fmt.Errorf("repair reactions: %w", err)
		}
	}
	s.logSeedPhase("reactions", phaseStarted, phaseBefore, stats)

	phaseStarted = time.Now()
	phaseBefore = stats
	// sticker sets（default 系统集 + 常规集 + emoji 集）：始终扫描导出目录以**增量**拾取
	// 新增的 set 目录——importStickerSetDir 跳过内容(hash)未变的已有集,只导入新集/变更集。
	// 这样向已部署(非空 store)的 data/sticker-seed 丢新集后重启即可生效,无需清库重 seed。
	// 仅当检测到旧版缩略图/可渲染预览元数据缺失时 force=true 全量重导修复。
	forceSticker := false
	previewState := fmt.Sprintf("%s:dc=%d", seedStickerPreviewStateVersion, s.dc)
	if n, err := s.media.CountStickerSets(ctx); err != nil {
		return stats, err
	} else if n > 0 {
		stale, err := s.stickerSetDocumentsNeedSeedRepair(ctx)
		if err != nil {
			return stats, err
		}
		migrated, err := s.seedStateMatches(ctx, seedStickerPreviewStateKey, previewState)
		if err != nil {
			return stats, err
		}
		forceSticker = stale || !migrated
	}
	if err := s.seedStickerSets(ctx, root, maxRegularSets, forceSticker, &stats); err != nil {
		return stats, fmt.Errorf("seed sticker sets: %w", err)
	}
	if err := s.putSeedState(ctx, seedStickerPreviewStateKey, previewState); err != nil {
		return stats, fmt.Errorf("record sticker preview seed state: %w", err)
	}
	s.logSeedPhase("sticker_sets", phaseStarted, phaseBefore, stats)

	phaseStarted = time.Now()
	phaseBefore = stats
	// 消息发送特效:全局静态目录,每次启动重建内存 s.effects(文档导入幂等)。
	if err := s.seedEffects(ctx, root, &stats); err != nil {
		return stats, fmt.Errorf("seed effects: %w", err)
	}
	s.logSeedPhase("effects", phaseStarted, phaseBefore, stats)

	phaseStarted = time.Now()
	phaseBefore = stats
	// messages.getEmojiGroups 分类图标的影子自定义 emoji 文档,依赖上面 sticker sets 阶段
	// 已 seed 的源文档,故放在其后。
	if err := s.seedEmojiGroupIcons(ctx, &stats); err != nil {
		return stats, fmt.Errorf("seed emoji group icons: %w", err)
	}
	s.logSeedPhase("emoji_group_icons", phaseStarted, phaseBefore, stats)

	if stats.Reactions == 0 && stats.StickerSets == 0 && stats.Effects == 0 {
		stats.Skipped = true
	}
	return stats, nil
}

func (s *Service) logSeedPhase(phase string, started time.Time, before, after SeedStats) {
	if s.log == nil {
		return
	}
	s.log.Info("media seed phase complete",
		zap.String("phase", phase),
		zap.Duration("elapsed", time.Since(started)),
		zap.Int("reactions", after.Reactions-before.Reactions),
		zap.Int("sticker_sets", after.StickerSets-before.StickerSets),
		zap.Int("effects", after.Effects-before.Effects),
		zap.Int("documents", after.Documents-before.Documents),
		zap.Int("blobs", after.Blobs-before.Blobs),
	)
}

// ---- reactions ----

func (s *Service) seedReactions(ctx context.Context, root string, stats *SeedStats) error {
	reactionsDir := filepath.Join(root, "telegram_reactions_export", "reactions")
	rawPath := filepath.Join(root, "telegram_reactions_export", "global_json", "available_reactions_raw.json")
	raw, err := os.ReadFile(rawPath)
	if err != nil {
		return nil // 没有 reaction 资源就跳过
	}
	var parsed struct {
		Result struct {
			Reactions []seedReactionJSON `json:"reactions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return fmt.Errorf("parse available_reactions_raw.json: %w", err)
	}
	index, err := scanSeedDir(reactionsDir)
	if err != nil {
		return err
	}
	for i, rj := range parsed.Result.Reactions {
		ar := domain.AvailableReaction{
			Reaction: rj.Reaction,
			Title:    rj.Title,
			Inactive: rj.Inactive,
			Premium:  rj.Premium,
			Order:    i,
		}
		set := func(dst *int64, d *seedDocumentJSON, fallbackThumb []byte) error {
			if d == nil || d.ID == 0 {
				return nil
			}
			doc, err := s.importDocument(ctx, *d, reactionsDir, index, stats, fallbackThumb)
			if err != nil {
				return err
			}
			*dst = doc.ID
			return nil
		}
		if err := set(&ar.StaticIconID, rj.StaticIcon, nil); err != nil {
			return err
		}
		// telegram_reactions_export's raw JSON never carries thumbs for any of
		// these .tgs animations -- see withSeedTGSImageSize's doc comment for
		// the client-visible symptom that leaves (a reaction's settled badge
		// freezing on an empty raster, since there's no real thumb to freeze
		// to). The reaction's own static_icon, imported just above, is a real
		// raster of the same reaction already sitting on disk -- give every
		// animation in this entry that as its cached thumb instead of none.
		var staticIconThumb []byte
		if rj.StaticIcon != nil {
			if path, ok := index.main[rj.StaticIcon.ID]; ok {
				if data, err := os.ReadFile(path); err == nil {
					staticIconThumb = data
				}
			}
		}
		if err := set(&ar.AppearAnimationID, rj.AppearAnimation, staticIconThumb); err != nil {
			return err
		}
		if err := set(&ar.SelectAnimationID, rj.SelectAnimation, staticIconThumb); err != nil {
			return err
		}
		if err := set(&ar.ActivateAnimationID, rj.ActivateAnimation, staticIconThumb); err != nil {
			return err
		}
		if err := set(&ar.EffectAnimationID, rj.EffectAnimation, staticIconThumb); err != nil {
			return err
		}
		if err := set(&ar.AroundAnimationID, rj.AroundAnimation, staticIconThumb); err != nil {
			return err
		}
		if err := set(&ar.CenterIconID, rj.CenterIcon, staticIconThumb); err != nil {
			return err
		}
		if err := s.media.PutAvailableReaction(ctx, ar); err != nil {
			return err
		}
		stats.Reactions++
	}
	return nil
}

func (s *Service) availableReactionSeedNeedsRepair(ctx context.Context) (bool, error) {
	reactions, err := s.media.ListAvailableReactions(ctx)
	if err != nil {
		return false, err
	}
	var docIDs []int64
	for _, r := range reactions {
		for _, id := range r.DocumentIDs() {
			if id == 0 {
				continue
			}
			docIDs = append(docIDs, id)
			if _, ok, err := s.media.GetFileBlob(ctx, fmt.Sprintf("doc:%d", id)); err != nil {
				return false, err
			} else if !ok {
				return true, nil
			}
		}
	}
	if stale, err := s.documentsNeedInlineCachedThumbs(ctx, docIDs); err != nil || stale {
		return stale, err
	}
	// Every reaction animation gets its static_icon as a raster thumb, so a
	// .tgs without one was seeded before that existed.
	docs, err := s.media.GetDocuments(ctx, docIDs)
	if err != nil {
		return false, err
	}
	for _, doc := range docs {
		if doc.MimeType == stickerMaterialMimeTGS && !photoSizesHaveRaster(doc.Thumbs) {
			return true, nil
		}
	}
	return false, nil
}

// ---- sticker sets ----

func (s *Service) seedStickerSets(ctx context.Context, root string, maxRegular int, force bool, stats *SeedStats) error {
	// default 系统集：目录名 → system_key。
	defaultDir := filepath.Join(root, "telegram_default_stickers_export")
	order := 0
	if entries, err := os.ReadDir(defaultDir); err == nil {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			systemKey := systemKeyForDefaultSet(name)
			setDir := filepath.Join(defaultDir, name)
			if err := s.importStickerSetDir(ctx, setDir, systemKey, order, force, stats); err != nil {
				return fmt.Errorf("import default set %s: %w", name, err)
			}
			order++
		}
	}

	// custom-emoji 集（telegram_emoji_export/<set>/）：不受 maxRegular 限制,按 set_info 的
	// emojis 标志归入 StickerSetKindEmoji(getEmojiStickers/getFeaturedEmojiStickers 下发)。
	emojiDir := filepath.Join(root, "telegram_emoji_export")
	if entries, err := os.ReadDir(emojiDir); err == nil {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			setDir := filepath.Join(emojiDir, name)
			if err := s.importStickerSetDir(ctx, setDir, "", order, force, stats); err != nil {
				return fmt.Errorf("import emoji set %s: %w", name, err)
			}
			order++
		}
	}

	// 常规贴纸集。
	regularDir := filepath.Join(root, "telegram_stickers_export")
	if entries, err := os.ReadDir(regularDir); err == nil {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		imported := 0
		for _, name := range names {
			if maxRegular > 0 && imported >= maxRegular {
				break
			}
			setDir := filepath.Join(regularDir, name)
			if err := s.importStickerSetDir(ctx, setDir, "", order, force, stats); err != nil {
				return fmt.Errorf("import sticker set %s: %w", name, err)
			}
			order++
			imported++
		}
	}
	return nil
}

func (s *Service) importStickerSetDir(ctx context.Context, setDir, systemKey string, order int, force bool, stats *SeedStats) error {
	infoPath := filepath.Join(setDir, "set_info.json")
	raw, err := os.ReadFile(infoPath)
	if err != nil {
		return nil // 该目录无 set_info.json → 跳过
	}
	var info struct {
		InputSetType string                   `json:"input_set_type"`
		Result       seedStickerSetResultJSON `json:"result"`
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return fmt.Errorf("parse %s: %w", infoPath, err)
	}
	sj := info.Result.Set
	if sj.ID == 0 {
		return nil
	}
	if systemKey == "" {
		systemKey = systemKeyForInputSetType(info.InputSetType)
	}
	kind := stickerSetKind(sj, systemKey)
	// 增量 seed：集已存在且内容 hash 未变则跳过（不重读文档/重传 blob）。force 时强制重导
	// （缩略图内联缓存修复路径）。这让 seedStickerSets 可在非空 store 上每次启动安全重扫,
	// 仅导入新增/变更集；但系统集分类由导出元数据决定，旧库中 hash 相同但 system_key/kind
	// 错误的记录必须重写，避免 constructor 方式取不到同一个资源集。
	if !force {
		if existing, found, err := s.media.GetStickerSetByID(ctx, sj.ID); err != nil {
			return err
		} else if found && existing.Hash == sj.Hash && existing.SystemKey == systemKey && existing.Kind == kind {
			return nil
		}
	}
	stickersDir := filepath.Join(setDir, "stickers")
	index, err := scanSeedDir(stickersDir)
	if err != nil {
		return err
	}

	docIDs := make([]int64, 0, len(info.Result.Documents))
	docs := make([]domain.Document, 0, len(info.Result.Documents))
	for _, dj := range info.Result.Documents {
		doc, err := s.importDocument(ctx, dj, stickersDir, index, stats, nil)
		if err != nil {
			return err
		}
		if doc.ID != 0 {
			docIDs = append(docIDs, doc.ID)
			docs = append(docs, doc)
		}
	}

	set := domain.StickerSet{
		ID:              sj.ID,
		AccessHash:      sj.AccessHash,
		ShortName:       sj.ShortName,
		Title:           sj.Title,
		Count:           sj.Count,
		Hash:            sj.Hash,
		Kind:            kind,
		Official:        sj.Official,
		Animated:        true, // 导出资源均为 .tgs 动画贴纸
		Emojis:          sj.Emojis,
		Masks:           sj.Masks,
		Archived:        sj.Archived,
		Installed:       seedStickerSetInstalled(kind),
		ThumbDocumentID: derefInt64(sj.ThumbDocumentID),
		Thumbs:          seedStickerSetPhotoSizes(sj.Thumbs),
		ThumbDCID:       s.dc,
		ThumbVersion:    sj.ThumbVersion,
		DocumentIDs:     docIDs,
		Packs:           seedStickerPacks(sj.Packs, info.Result.Packs),
		SortOrder:       order,
		SystemKey:       systemKey,
	}
	if set.Count == 0 {
		set.Count = len(docIDs)
	}
	if err := s.media.PutStickerSet(ctx, set); err != nil {
		return err
	}
	s.stickerSetCache.put(set, docs)
	stats.StickerSets++
	return nil
}

// ---- 单个 document 导入 ----

// fallbackThumb is a raster image (e.g. a reaction's own static_icon webp) to
// use as this document's "m" thumb when the source JSON lists no raster thumb
// and none was rendered next to the file -- see seedRasterFallbackThumb. Pass
// nil when there is no such substitute.
func (s *Service) importDocument(ctx context.Context, dj seedDocumentJSON, binDir string, index seedDirIndex, stats *SeedStats, fallbackThumb []byte) (domain.Document, error) {
	if dj.ID == 0 {
		return domain.Document{}, nil
	}
	existing, existingFound, err := s.media.GetDocument(ctx, dj.ID)
	if err != nil {
		return domain.Document{}, err
	}
	ref, _ := hex.DecodeString(dj.FileReference)
	doc := domain.Document{
		ID:            dj.ID,
		AccessHash:    dj.AccessHash,
		FileReference: ref,
		Date:          parseSeedDate(dj.Date),
		MimeType:      dj.MimeType,
		Size:          dj.Size,
		DCID:          s.dc,
		Attributes:    withSeedStickerAttribute(dj.MimeType, seedDocumentAttributes(dj.Attributes)),
	}

	// 主体 blob：doc:<document-id>
	if mainPath, ok := index.main[dj.ID]; ok {
		data, err := os.ReadFile(mainPath)
		if err != nil {
			return domain.Document{}, err
		}
		doc.Attributes = withSeedTGSImageSize(doc.MimeType, doc.Attributes, data)
		objectKey, err := s.blobs.Put(ctx, data)
		if err != nil {
			return domain.Document{}, err
		}
		if err := s.media.PutFileBlob(ctx, domain.FileBlob{
			LocationKey: fmt.Sprintf("doc:%d", doc.ID),
			Backend:     domain.MediaBackend(s.blobs.Name()),
			ObjectKey:   objectKey,
			Size:        int64(len(data)),
			MimeType:    dj.MimeType,
		}); err != nil {
			return domain.Document{}, err
		}
		s.prewarmSmallBlob(objectKey, data)
		stats.Blobs++
	}

	// 缩略图保留两类并存（与官方 sticker 一致）：
	//   - PhotoPathSize 矢量轮廓：随 document 元数据内联下发，是 TDesktop 对 animated
	//     sticker 在完整 .tgs 下载完成前唯一可即时渲染的占位（history_view_sticker 显式
	//     禁用了 stripped 内联占位，cached 字节又经 RPC 出口转成 downloadable）；丢掉它会让
	//     打开会话时 sticker 先空白、并多触发一次缩略图 getFile。
	//   - 小 PhotoSize 静态图：写 blob 并暂存为 PhotoCachedSize（RPC 出口再转 downloadable
	//     photoSize m），供 sticker 面板等需要小缩略图的场景下载。
	thumbs := make([]domain.PhotoSize, 0, len(dj.Thumbs))
	for _, tj := range dj.Thumbs {
		ps, downloadable := seedPhotoSize(tj)
		if ps.Kind == "" {
			continue
		}
		if downloadable {
			thumbPath, ok := index.thumb[dj.ID][ps.Type]
			if !ok {
				continue // 无可服务的缩略图文件，丢弃该尺寸（保留 PhotoPathSize 占位）
			}
			data, err := os.ReadFile(thumbPath)
			if err != nil {
				return domain.Document{}, err
			}
			ps.Size = len(data)
			ps = seedInlineCachedDocumentThumb(ps, data)
			// A duplicate document can be present in several catalogs. Do not replace a
			// better already-persisted preview (and its shared location key) with a lower
			// quality rendition from the catalog imported later.
			if prior, ok := seedDocumentThumbByType(existing.Thumbs, ps.Type); existingFound && ok && seedPhotoSizeBetter(prior, ps) {
				ps = prior
			} else {
				objectKey, err := s.blobs.Put(ctx, data)
				if err != nil {
					return domain.Document{}, err
				}
				if err := s.media.PutFileBlob(ctx, domain.FileBlob{
					LocationKey: fmt.Sprintf("doc:%d:%s", doc.ID, ps.Type),
					Backend:     domain.MediaBackend(s.blobs.Name()),
					ObjectKey:   objectKey,
					Size:        int64(len(data)),
					MimeType:    seedThumbMimeType(data),
				}); err != nil {
					return domain.Document{}, err
				}
				s.prewarmSmallBlob(objectKey, data)
				stats.Blobs++
			}
		}
		thumbs = append(thumbs, ps)
	}
	doc.Thumbs = thumbs
	if data, ok := seedBundledDocumentPreview(doc.ID); ok {
		doc.Thumbs = appendSeedBundledDocumentPreview(doc.Thumbs, data)
	} else if !photoSizesHaveRaster(thumbs) {
		// The source JSON listed no raster thumb. Use one rendered next to the
		// file (<id>_thumbN_PhotoSize_typem_*.jpg), else the caller's fallback.
		data := fallbackThumb
		if path, ok := index.thumb[dj.ID][seedBundledDocumentThumbType]; ok {
			if data, err = os.ReadFile(path); err != nil {
				return domain.Document{}, err
			}
		}
		if len(data) > 0 {
			ps, ok, err := s.seedRasterFallbackThumb(ctx, doc.ID, data, stats)
			if err != nil {
				return domain.Document{}, err
			}
			if ok {
				doc.Thumbs = append(doc.Thumbs, ps)
			}
		}
	}
	if existingFound {
		doc.Thumbs = mergeSeedDocumentThumbs(existing.Thumbs, doc.Thumbs)
	}
	if err := s.ensureSeedCachedThumbBlobs(ctx, doc, stats); err != nil {
		return domain.Document{}, err
	}

	if err := s.media.PutDocument(ctx, doc); err != nil {
		return domain.Document{}, err
	}
	stats.Documents++
	return doc, nil
}

func appendSeedBundledDocumentPreview(thumbs []domain.PhotoSize, data []byte) []domain.PhotoSize {
	for _, thumb := range thumbs {
		if thumb.Type == seedBundledDocumentThumbType && seedPhotoSizePreviewTier(thumb) >= 4 {
			return thumbs
		}
	}
	out := thumbs[:0]
	for _, thumb := range thumbs {
		if thumb.Type != seedBundledDocumentThumbType {
			out = append(out, thumb)
		}
	}
	return append(out, domain.PhotoSize{
		Kind:  domain.PhotoSizeKindCached,
		Type:  seedBundledDocumentThumbType,
		W:     128,
		H:     128,
		Bytes: append([]byte(nil), data...),
	})
}

func (s *Service) prewarmSmallBlob(objectKey string, data []byte) {
	if len(data) > 0 && len(data) <= blobBytesCacheMaxEntryBytes {
		s.byteCache.put(objectKey, data)
	}
}

// ---- 目录扫描：docID → 主体文件 / 可下载缩略图 ----

type seedDirIndex struct {
	main  map[int64]string            // docID -> 主体文件路径
	thumb map[int64]map[string]string // docID -> thumbType -> 缩略图文件路径
}

var seedTrailingDigits = regexp.MustCompile(`(\d{6,})`)
var seedThumbMarker = regexp.MustCompile(`_thumb\d+_`)

const seedInlineCachedDocumentThumbMaxBytes = 32 * 1024

var seedThumbType = regexp.MustCompile(`PhotoSize_type([a-z])`)

func scanSeedDir(dir string) (seedDirIndex, error) {
	idx := seedDirIndex{main: map[int64]string{}, thumb: map[int64]map[string]string{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return idx, nil // 目录不存在 → 空 index
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		full := filepath.Join(dir, name)
		if marker := seedThumbMarker.FindStringIndex(name); marker != nil {
			// 只收可下载的 PhotoSize 缩略图（jpg）；PhotoPathSize(svg) 内联在 JSON。
			if ext != ".jpg" && ext != ".jpeg" {
				continue
			}
			m := seedThumbType.FindStringSubmatch(name)
			if m == nil {
				continue
			}
			docID := docIDFromName(name[:marker[0]])
			if docID == 0 {
				continue
			}
			if idx.thumb[docID] == nil {
				idx.thumb[docID] = map[string]string{}
			}
			idx.thumb[docID][m[1]] = full
			continue
		}
		if ext == ".svg" || ext == ".json" {
			continue
		}
		docID := docIDFromName(strings.TrimSuffix(name, filepath.Ext(name)))
		if docID == 0 {
			continue
		}
		idx.main[docID] = full
	}
	return idx, nil
}

// docIDFromName 取 base name 中末尾最长的数字串作为 document id。
func docIDFromName(base string) int64 {
	matches := seedTrailingDigits.FindAllString(base, -1)
	if len(matches) == 0 {
		return 0
	}
	last := matches[len(matches)-1]
	id, err := strconv.ParseInt(last, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

func systemKeyForDefaultSet(dirName string) string {
	switch dirName {
	case "DefaultSet_AnimatedEmoji":
		return "animated_emoji"
	case "DefaultSet_AnimatedEmojiAnimations":
		return "animated_emoji_animations"
	case "DefaultSet_EmojiGenericAnimations":
		return "emoji_generic_animations"
	case "DefaultSet_EmojiDefaultStatuses":
		return domain.StickerSetSystemKeyEmojiDefaultStatuses
	case "DefaultSet_EmojiDefaultTopicIcons":
		return domain.StickerSetSystemKeyEmojiDefaultTopicIcons
	case "DefaultSet_PremiumGifts":
		return domain.StickerSetSystemKeyPremiumGifts
	case "DefaultSet_TonGifts":
		return domain.StickerSetSystemKeyTonGifts
	case "DefaultSet_Dice_Normal":
		return "dice:\U0001f3b2"
	case "DefaultSet_Dice_Dart":
		return "dice:\U0001f3af"
	case "DefaultSet_Dice_Basketball":
		return "dice:\U0001f3c0"
	case "DefaultSet_Dice_Football":
		return "dice:⚽"
	case "DefaultSet_Dice_Bowling":
		return "dice:\U0001f3b3"
	case "DefaultSet_Dice_Casino":
		return "dice:\U0001f3b0"
	default:
		return ""
	}
}

func systemKeyForInputSetType(inputSetType string) string {
	normalized := strings.TrimSpace(inputSetType)
	normalized = strings.TrimPrefix(normalized, "*")
	normalized = strings.TrimPrefix(normalized, "tg.")
	switch normalized {
	case "InputStickerSetAnimatedEmoji":
		return "animated_emoji"
	case "InputStickerSetAnimatedEmojiAnimations":
		return "animated_emoji_animations"
	case "InputStickerSetEmojiGenericAnimations":
		return "emoji_generic_animations"
	case "InputStickerSetEmojiDefaultStatuses", "InputStickerSetEmojiChannelDefaultStatuses":
		return domain.StickerSetSystemKeyEmojiDefaultStatuses
	case "InputStickerSetEmojiDefaultTopicIcons":
		return domain.StickerSetSystemKeyEmojiDefaultTopicIcons
	case "InputStickerSetPremiumGifts":
		return domain.StickerSetSystemKeyPremiumGifts
	case "InputStickerSetTonGifts":
		return domain.StickerSetSystemKeyTonGifts
	default:
		return ""
	}
}

func stickerSetKind(sj seedStickerSetJSON, systemKey string) domain.StickerSetKind {
	switch {
	case systemKey != "":
		return domain.StickerSetKindSystem
	case sj.Emojis:
		return domain.StickerSetKindEmoji
	case sj.Masks:
		return domain.StickerSetKindMasks
	default:
		return domain.StickerSetKindStickers
	}
}

func seedStickerSetInstalled(kind domain.StickerSetKind) bool {
	return false
}

// ---- JSON → domain 转换 ----

func seedDocumentAttributes(attrs []seedAttrJSON) []domain.DocumentAttribute {
	out := make([]domain.DocumentAttribute, 0, len(attrs))
	for _, a := range attrs {
		switch a.Type {
		case "DocumentAttributeImageSize":
			out = append(out, domain.DocumentAttribute{Kind: domain.DocAttrImageSize, W: a.W, H: a.H})
		case "DocumentAttributeAnimated":
			out = append(out, domain.DocumentAttribute{Kind: domain.DocAttrAnimated})
		case "DocumentAttributeSticker":
			attr := domain.DocumentAttribute{Kind: domain.DocAttrSticker, Alt: a.Alt, Mask: a.Mask}
			if a.Stickerset != nil {
				attr.StickerSetID = a.Stickerset.ID
				attr.StickerSetAccessHash = a.Stickerset.AccessHash
			}
			out = append(out, attr)
		case "DocumentAttributeCustomEmoji":
			attr := domain.DocumentAttribute{Kind: domain.DocAttrCustomEmoji, Alt: a.Alt, Free: a.Free, TextColor: a.TextColor}
			if a.Stickerset != nil {
				attr.StickerSetID = a.Stickerset.ID
				attr.StickerSetAccessHash = a.Stickerset.AccessHash
			}
			out = append(out, attr)
		case "DocumentAttributeVideo":
			out = append(out, domain.DocumentAttribute{Kind: domain.DocAttrVideo, W: a.W, H: a.H, Duration: a.Duration, RoundMessage: a.RoundMessage, SupportsStreaming: a.SupportsStreaming, NoSound: a.NoSound, VideoCodec: a.VideoCodec})
		case "DocumentAttributeAudio":
			out = append(out, domain.DocumentAttribute{Kind: domain.DocAttrAudio, AudioDuration: int(a.Duration), Voice: a.Voice, Title: a.Title, Performer: a.Performer})
		case "DocumentAttributeFilename":
			out = append(out, domain.DocumentAttribute{Kind: domain.DocAttrFilename, FileName: a.FileName})
		}
	}
	return out
}

// withSeedStickerAttribute adds a bare documentAttributeSticker to a .tgs
// document whose seed export forgot one -- telegram_reactions_export's
// available_reactions_raw.json carries only DocumentAttributeAnimated for
// every appear/select/activate/effect/around animation (438 documents, all
// of them), never the Sticker attribute a real .tgs export always has. That
// would be harmless on its own (convert_media.go's tgDocumentAttributes
// already treats DocumentAttributeAnimated as implied by the .tgs mime type
// and drops it on the wire), except it is the ONLY attribute these documents
// carry -- so what actually reaches a client is zero attributes on an
// application/x-tgsticker document. Every real client (tdesktop, tweb,
// Telegram Android) only renders a .tgs as an animated sticker when it finds
// documentAttributeSticker (or a literal "AnimatedSticker.tgs" file name,
// which this export doesn't set either) among the document's attributes --
// see tweb's appDocsManager.ts, the StickerType.Lottie branch. Without it
// the document is just an unrecognized file: wrapSticker never gets a
// player to hand back, and the reaction picker's own code assumes one,
// throwing when it tries to attach a hover listener to whatever it got
// instead -- the reported "reactions/gifts render as broken files, and
// something flies into the corner of the screen" bug.
//
// Mask/Alt/StickerSetID are left zero: these reaction animations are not
// members of any sticker set and have no short-name alt text in the source
// export, and a bare attribute is all the client actually checks for.
func withSeedStickerAttribute(mimeType string, attrs []domain.DocumentAttribute) []domain.DocumentAttribute {
	if mimeType != stickerMaterialMimeTGS {
		return attrs
	}
	for _, a := range attrs {
		if a.Kind == domain.DocAttrSticker {
			return attrs
		}
	}
	return append(attrs, domain.DocumentAttribute{Kind: domain.DocAttrSticker})
}

// withSeedTGSImageSize adds a DocumentAttributeImageSize to a .tgs document
// that is missing one, read from the Lottie animation's own "w"/"h" fields
// (a .tgs file is a gzipped Lottie JSON; every real export carries its
// intrinsic size there, same as every working sticker-set document already
// has via DocumentAttributeImageSize -- telegram_reactions_export's raw
// JSON just never copied it into the document's attributes list). Without
// it a client has no declared canvas size to work from before the file is
// actually decoded, which is what made a reaction's one-shot "fly" effect
// and its settled badge -- both playing the same center_icon document at
// once -- size and cache their decoded frames independently and race:
// one's cleanup closed an ImageBitmap the other was still mid-draw with
// ("the image source is detached"). Best-effort: a read/parse failure
// leaves attrs untouched rather than failing the whole seed import over a
// cosmetic sizing hint.
func withSeedTGSImageSize(mimeType string, attrs []domain.DocumentAttribute, data []byte) []domain.DocumentAttribute {
	if mimeType != stickerMaterialMimeTGS {
		return attrs
	}
	for _, a := range attrs {
		if a.Kind == domain.DocAttrImageSize {
			return attrs
		}
	}
	w, h, ok := tgsIntrinsicSize(data)
	if !ok {
		return attrs
	}
	return append(attrs, domain.DocumentAttribute{Kind: domain.DocAttrImageSize, W: w, H: h})
}

// tgsIntrinsicSize gunzips a .tgs file and reads the Lottie animation's own
// "w"/"h" (its composition's declared width/height in Lottie's abstract
// units, which is what every Telegram client treats as the sticker's pixel
// size). Returns ok=false on anything that doesn't look like a valid,
// positively-sized .tgs rather than erroring -- this only ever feeds an
// optional sizing hint.
func tgsIntrinsicSize(data []byte) (w, h int, ok bool) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return 0, 0, false
	}
	defer gz.Close()
	raw, err := io.ReadAll(io.LimitReader(gz, 1<<20))
	if err != nil {
		return 0, 0, false
	}
	var parsed struct {
		W int `json:"w"`
		H int `json:"h"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || parsed.W <= 0 || parsed.H <= 0 {
		return 0, 0, false
	}
	return parsed.W, parsed.H, true
}

// seedRasterFallbackThumb turns a raster image that the source JSON did not
// list (see importDocument's fallbackThumb) into the document's "m" thumb,
// stored the same way as an exported one: inline PhotoCachedSize (written to
// doc:<id>:m by ensureSeedCachedThumbBlobs, advertised as a downloadable
// photoSize on the wire), or a plain PhotoSize blob when it is too big to
// inline.
//
// Every real Telegram .tgs document has such a raster thumb, and clients
// rely on it whenever they show the sticker frozen: tweb's wrapSticker with
// static set (a reaction's settled badge, a pinned gift, an emoji pattern)
// picks it with choosePhotoSize, and without one downloads the .tgs itself
// into an <img> -- the decompressed Lottie JSON, i.e. a broken image.
//
// ok is false when data is not an image this server can size; the document
// then keeps no raster thumb rather than failing the whole seed.
func (s *Service) seedRasterFallbackThumb(ctx context.Context, docID int64, data []byte, stats *SeedStats) (domain.PhotoSize, bool, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return domain.PhotoSize{}, false, nil
	}
	ps := domain.PhotoSize{Kind: domain.PhotoSizeKindDefault, Type: seedBundledDocumentThumbType, W: cfg.Width, H: cfg.Height, Size: len(data)}
	if ps = seedInlineCachedDocumentThumb(ps, data); ps.Kind == domain.PhotoSizeKindCached {
		return ps, true, nil
	}
	objectKey, err := s.blobs.Put(ctx, data)
	if err != nil {
		return domain.PhotoSize{}, false, err
	}
	if err := s.media.PutFileBlob(ctx, domain.FileBlob{
		LocationKey: fmt.Sprintf("doc:%d:%s", docID, ps.Type),
		Backend:     domain.MediaBackend(s.blobs.Name()),
		ObjectKey:   objectKey,
		Size:        int64(len(data)),
		MimeType:    seedThumbMimeType(data),
	}); err != nil {
		return domain.PhotoSize{}, false, err
	}
	s.prewarmSmallBlob(objectKey, data)
	stats.Blobs++
	return ps, true, nil
}

func documentHasImageSize(doc domain.Document) bool {
	for _, a := range doc.Attributes {
		if a.Kind == domain.DocAttrImageSize {
			return true
		}
	}
	return false
}

// photoSizesHaveRaster reports whether thumbs include a sized raster preview
// a client can download (a PhotoPathSize outline does not count).
func photoSizesHaveRaster(thumbs []domain.PhotoSize) bool {
	for _, t := range thumbs {
		if t.W <= 0 || t.H <= 0 {
			continue
		}
		if (t.Kind == domain.PhotoSizeKindCached && len(t.Bytes) > 0) || (t.Kind == domain.PhotoSizeKindDefault && t.Size > 0) {
			return true
		}
	}
	return false
}

func seedStickerSetPhotoSizes(thumbs []seedThumbJSON) []domain.PhotoSize {
	out := make([]domain.PhotoSize, 0, len(thumbs))
	for _, t := range thumbs {
		ps, downloadable := seedPhotoSize(t)
		if ps.Kind == "" || downloadable {
			continue
		}
		out = append(out, ps)
	}
	return out
}

func seedPhotoSize(t seedThumbJSON) (domain.PhotoSize, bool) {
	switch t.Type {
	case "PhotoSize":
		return domain.PhotoSize{Kind: domain.PhotoSizeKindDefault, Type: t.SizeType, W: t.W, H: t.H, Size: t.Size}, true
	case "PhotoStrippedSize":
		b, _ := hex.DecodeString(t.Bytes)
		return domain.PhotoSize{Kind: domain.PhotoSizeKindStripped, Type: t.SizeType, Bytes: b}, false
	case "PhotoCachedSize":
		b, _ := hex.DecodeString(t.Bytes)
		return domain.PhotoSize{Kind: domain.PhotoSizeKindCached, Type: t.SizeType, W: t.W, H: t.H, Bytes: b}, false
	case "PhotoPathSize":
		b, _ := hex.DecodeString(t.Bytes)
		return domain.PhotoSize{Kind: domain.PhotoSizeKindPath, Type: t.SizeType, Bytes: b}, false
	case "PhotoSizeProgressive":
		return domain.PhotoSize{Kind: domain.PhotoSizeKindProgressive, Type: t.SizeType, W: t.W, H: t.H, Sizes: t.Sizes}, true
	default:
		return domain.PhotoSize{}, false
	}
}

func seedInlineCachedDocumentThumb(ps domain.PhotoSize, data []byte) domain.PhotoSize {
	if ps.Kind != domain.PhotoSizeKindDefault || len(data) == 0 || len(data) > seedInlineCachedDocumentThumbMaxBytes {
		return ps
	}
	ps.Kind = domain.PhotoSizeKindCached
	ps.Size = 0
	ps.Bytes = append([]byte(nil), data...)
	return ps
}

// mergeSeedDocumentThumbs makes duplicate seed imports monotonic for preview quality.
// PhotoSize.Type is also the blob location suffix, so only one winner per type may be
// advertised. Incoming metadata wins ties; a richer existing preview wins downgrades.
func mergeSeedDocumentThumbs(existing, incoming []domain.PhotoSize) []domain.PhotoSize {
	out := append([]domain.PhotoSize(nil), incoming...)
	byType := make(map[string]int, len(out))
	for i, thumb := range out {
		if thumb.Type != "" {
			byType[thumb.Type] = i
		}
	}
	for _, thumb := range existing {
		if thumb.Type != "" {
			if i, ok := byType[thumb.Type]; ok {
				if seedPhotoSizeBetter(thumb, out[i]) {
					out[i] = thumb
				}
				continue
			}
			byType[thumb.Type] = len(out)
		}
		out = append(out, thumb)
	}

	return out
}

func seedDocumentThumbByType(thumbs []domain.PhotoSize, typ string) (domain.PhotoSize, bool) {
	for _, thumb := range thumbs {
		if thumb.Type == typ {
			return thumb, true
		}
	}
	return domain.PhotoSize{}, false
}

func seedPhotoSizeBetter(a, b domain.PhotoSize) bool {
	aTier, bTier := seedPhotoSizePreviewTier(a), seedPhotoSizePreviewTier(b)
	if aTier != bTier {
		return aTier > bTier
	}
	aArea, bArea := int64(a.W)*int64(a.H), int64(b.W)*int64(b.H)
	if aArea != bArea {
		return aArea > bArea
	}
	aPayload, bPayload := len(a.Bytes)+a.Size, len(b.Bytes)+b.Size
	return aPayload > bPayload
}

func seedPhotoSizePreviewTier(thumb domain.PhotoSize) int {
	switch thumb.Kind {
	case domain.PhotoSizeKindCached:
		if len(thumb.Bytes) > 0 && thumb.W > 0 && thumb.H > 0 {
			return 4
		}
	case domain.PhotoSizeKindDefault, domain.PhotoSizeKindProgressive:
		if thumb.Size > 0 && thumb.W > 0 && thumb.H > 0 {
			return 4
		}
	case domain.PhotoSizeKindPath, domain.PhotoSizeKindStripped:
		if len(thumb.Bytes) > 0 {
			return 3
		}
	}
	return 1
}

// ensureSeedCachedThumbBlobs keeps the RPC conversion invariant: document cached
// previews are exposed as downloadable PhotoSize entries, so every advertised type
// must have a matching blob even when the source JSON carried the bytes inline.
func (s *Service) ensureSeedCachedThumbBlobs(ctx context.Context, doc domain.Document, stats *SeedStats) error {
	for _, thumb := range doc.Thumbs {
		if thumb.Kind != domain.PhotoSizeKindCached || thumb.Type == "" || len(thumb.Bytes) == 0 {
			continue
		}
		locationKey := fmt.Sprintf("doc:%d:%s", doc.ID, thumb.Type)
		mimeType := seedThumbMimeType(thumb.Bytes)
		stored, found, err := s.media.GetFileBlob(ctx, locationKey)
		if err != nil {
			return err
		}
		if found && stored.Size == int64(len(thumb.Bytes)) && stored.MimeType == mimeType {
			continue
		}
		if s.blobs == nil {
			return fmt.Errorf("blob backend not configured for cached document thumb %s", locationKey)
		}
		objectKey, err := s.blobs.Put(ctx, thumb.Bytes)
		if err != nil {
			return err
		}
		if err := s.media.PutFileBlob(ctx, domain.FileBlob{
			LocationKey: locationKey,
			Backend:     domain.MediaBackend(s.blobs.Name()),
			ObjectKey:   objectKey,
			Size:        int64(len(thumb.Bytes)),
			MimeType:    mimeType,
		}); err != nil {
			return err
		}
		s.prewarmSmallBlob(objectKey, thumb.Bytes)
		stats.Blobs++
	}
	return nil
}

func seedDocumentHasAttribute(attrs []domain.DocumentAttribute, kind domain.DocumentAttributeKind) bool {
	for _, attr := range attrs {
		if attr.Kind == kind {
			return true
		}
	}
	return false
}

func seedThumbMimeType(data []byte) string {
	switch {
	case len(data) >= 12 && data[0] == 'R' && data[1] == 'I' && data[2] == 'F' && data[3] == 'F' &&
		data[8] == 'W' && data[9] == 'E' && data[10] == 'B' && data[11] == 'P':
		return "image/webp"
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "image/jpeg"
	case len(data) >= 8 && data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G':
		return "image/png"
	case len(data) >= 6 && data[0] == 'G' && data[1] == 'I' && data[2] == 'F':
		return "image/gif"
	default:
		return "application/octet-stream"
	}
}

func (s *Service) stickerSetDocumentsNeedSeedRepair(ctx context.Context) (bool, error) {
	var ids []int64
	for _, kind := range []domain.StickerSetKind{
		domain.StickerSetKindStickers,
		domain.StickerSetKindEmoji,
		domain.StickerSetKindMasks,
		domain.StickerSetKindSystem,
	} {
		sets, err := s.media.ListStickerSets(ctx, kind)
		if err != nil {
			return false, err
		}
		for _, set := range sets {
			ids = append(ids, set.DocumentIDs...)
		}
	}
	return s.documentsNeedSeedRepair(ctx, ids)
}

func (s *Service) documentsNeedInlineCachedThumbs(ctx context.Context, ids []int64) (bool, error) {
	return s.documentsNeedSeedRepair(ctx, ids)
}

func (s *Service) documentsNeedSeedRepair(ctx context.Context, ids []int64) (bool, error) {
	if len(ids) == 0 {
		return false, nil
	}
	seen := make(map[int64]struct{}, len(ids))
	unique := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	docs, err := s.media.GetDocuments(ctx, unique)
	if err != nil {
		return false, err
	}
	for _, doc := range docs {
		// A .tgs document with no documentAttributeSticker was seeded before
		// withSeedStickerAttribute existed -- see that function's doc comment
		// for why a bare DocumentAttributeAnimated alone (the only thing
		// telegram_reactions_export's raw JSON carries) leaves every client
		// unable to recognize these as animated stickers at all. Re-seeding
		// recomputes doc.Attributes through withSeedStickerAttribute, which
		// is what actually repairs it.
		if doc.MimeType == stickerMaterialMimeTGS && (!doc.IsSticker() || !documentHasImageSize(doc)) {
			return true, nil
		}
		for _, thumb := range doc.Thumbs {
			if thumb.Kind == domain.PhotoSizeKindDefault && thumb.Size > 0 && thumb.Size <= seedInlineCachedDocumentThumbMaxBytes {
				return true, nil
			}
			if thumb.Kind == domain.PhotoSizeKindCached && len(thumb.Bytes) > 0 {
				blob, ok, err := s.media.GetFileBlob(ctx, fmt.Sprintf("doc:%d:%s", doc.ID, thumb.Type))
				if err != nil {
					return false, err
				}
				if !ok {
					return true, nil
				}
				want := seedThumbMimeType(thumb.Bytes)
				if blob.Size != int64(len(thumb.Bytes)) || (want != "application/octet-stream" && blob.MimeType != want) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func seedStickerPacks(setPacks, resultPacks []seedStickerPackJSON) []domain.StickerPack {
	packs := setPacks
	if len(packs) == 0 {
		packs = resultPacks
	}
	out := make([]domain.StickerPack, 0, len(packs))
	for _, p := range packs {
		documents := make([]int64, 0, len(p.Documents))
		for _, id := range p.Documents {
			if id > 0 {
				documents = append(documents, id)
			}
		}
		out = append(out, domain.StickerPack{Emoticon: p.Emoticon, DocumentIDs: documents})
	}
	return out
}

func parseSeedDate(s string) int {
	if s == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return int(t.Unix())
	}
	return 0
}

func derefInt64(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

// ---- seed JSON 结构 ----

type seedInputStickerSetJSON struct {
	ID         int64 `json:"id"`
	AccessHash int64 `json:"access_hash"`
}

type seedAttrJSON struct {
	Type              string                   `json:"_"`
	W                 int                      `json:"w"`
	H                 int                      `json:"h"`
	Alt               string                   `json:"alt"`
	Mask              bool                     `json:"mask"`
	Duration          float64                  `json:"duration"`
	RoundMessage      bool                     `json:"round_message"`
	SupportsStreaming bool                     `json:"supports_streaming"`
	NoSound           bool                     `json:"nosound"`
	VideoCodec        string                   `json:"video_codec"`
	Voice             bool                     `json:"voice"`
	Title             string                   `json:"title"`
	Performer         string                   `json:"performer"`
	FileName          string                   `json:"file_name"`
	Free              bool                     `json:"free"`
	TextColor         bool                     `json:"text_color"`
	Stickerset        *seedInputStickerSetJSON `json:"stickerset"`
}

type seedThumbJSON struct {
	Type     string `json:"_"`
	SizeType string `json:"type"`
	W        int    `json:"w"`
	H        int    `json:"h"`
	Size     int    `json:"size"`
	Bytes    string `json:"bytes"`
	Sizes    []int  `json:"sizes"`
}

type seedDocumentJSON struct {
	ID            int64           `json:"id"`
	AccessHash    int64           `json:"access_hash"`
	FileReference string          `json:"file_reference"`
	Date          string          `json:"date"`
	MimeType      string          `json:"mime_type"`
	Size          int64           `json:"size"`
	DCID          int             `json:"dc_id"`
	Attributes    []seedAttrJSON  `json:"attributes"`
	Thumbs        []seedThumbJSON `json:"thumbs"`
}

type seedStickerPackJSON struct {
	Emoticon  string  `json:"emoticon"`
	Documents []int64 `json:"documents"`
}

type seedStickerSetJSON struct {
	ID              int64                 `json:"id"`
	AccessHash      int64                 `json:"access_hash"`
	Title           string                `json:"title"`
	ShortName       string                `json:"short_name"`
	Count           int                   `json:"count"`
	Hash            int                   `json:"hash"`
	Archived        bool                  `json:"archived"`
	Official        bool                  `json:"official"`
	Masks           bool                  `json:"masks"`
	Emojis          bool                  `json:"emojis"`
	Thumbs          []seedThumbJSON       `json:"thumbs"`
	ThumbDCID       int                   `json:"thumb_dc_id"`
	ThumbVersion    int                   `json:"thumb_version"`
	ThumbDocumentID *int64                `json:"thumb_document_id"`
	Packs           []seedStickerPackJSON `json:"packs"`
}

type seedStickerSetResultJSON struct {
	Set       seedStickerSetJSON    `json:"set"`
	Packs     []seedStickerPackJSON `json:"packs"`
	Documents []seedDocumentJSON    `json:"documents"`
}

type seedReactionJSON struct {
	Reaction          string            `json:"reaction"`
	Title             string            `json:"title"`
	Inactive          bool              `json:"inactive"`
	Premium           bool              `json:"premium"`
	StaticIcon        *seedDocumentJSON `json:"static_icon"`
	AppearAnimation   *seedDocumentJSON `json:"appear_animation"`
	SelectAnimation   *seedDocumentJSON `json:"select_animation"`
	ActivateAnimation *seedDocumentJSON `json:"activate_animation"`
	EffectAnimation   *seedDocumentJSON `json:"effect_animation"`
	AroundAnimation   *seedDocumentJSON `json:"around_animation"`
	CenterIcon        *seedDocumentJSON `json:"center_icon"`
}
