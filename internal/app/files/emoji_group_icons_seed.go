package files

import (
	"context"
	"fmt"

	"telesrv/internal/domain"
)

// emojiGroupIconSpec 描述 messages.getEmojiGroups 一个分类图标:把已 seed 的
// DefaultSet_AnimatedEmoji 贴纸文档(SourceDocID,带 DocAttrSticker)在本地"复制"成一份
// 带 DocAttrCustomEmoji 的影子文档(NewID),二进制内容(blob)原样复用,不重新下载/编码。
//
// 背景:messages.getEmojiGroups 的 icon_emoji_id 语义上必须指向一份真正的自定义 emoji
// 文档——真实 Telegram 的这些 icon 文档始终带 DocumentAttributeCustomEmoji。我们最初直接
// 复用 DefaultSet_AnimatedEmoji 里现成的贴纸文档 id(见 internal/seed/catalog/emoji_groups.json),
// 但那些文档只带 DocAttrSticker,不带 DocAttrCustomEmoji——Android 客户端渲染分类图标那一小格
// 时找不到期望的自定义 emoji 元数据,一直转圈(桌面端似乎更宽松,不受影响)。
// NewID 特意选用远小于真实 Telegram snowflake id(19 位)的 13 位数字,不会与任何真实/已
// seed 的 document id 冲突。
type emojiGroupIconSpec struct {
	NewID       int64
	SourceDocID int64
	Alt         string
}

var emojiGroupIconSpecs = []emojiGroupIconSpec{
	{NewID: 9000000000001, SourceDocID: 1258816259753929, Alt: "❤"},    // Love
	{NewID: 9000000000002, SourceDocID: 5181593617004757506, Alt: "👍"}, // Approval
	{NewID: 9000000000003, SourceDocID: 5181852277115192162, Alt: "👎"}, // Disapproval
	{NewID: 9000000000004, SourceDocID: 4909118808687378938, Alt: "🎉"}, // Cheers
	{NewID: 9000000000005, SourceDocID: 5129721261156467513, Alt: "😄"}, // Laughter
	{NewID: 9000000000006, SourceDocID: 5129715527375127060, Alt: "😨"}, // Astonishment
	{NewID: 9000000000007, SourceDocID: 5129804416018285169, Alt: "😔"}, // Sadness
	{NewID: 9000000000008, SourceDocID: 5129791445217051348, Alt: "😡"}, // Anger
	{NewID: 9000000000009, SourceDocID: 5129653108615414540, Alt: "😐"}, // Neutral
	{NewID: 9000000000010, SourceDocID: 5127352539448082997, Alt: "🤔"}, // Doubt
	{NewID: 9000000000011, SourceDocID: 5127684445930783362, Alt: "🤪"}, // Silly
}

// seedEmojiGroupIcons 为 emojiGroupIconSpecs 中每一项确保存在一份带 DocAttrCustomEmoji
// 的影子文档。源贴纸未 seed(开发机没有该 sticker-seed 数据)时静默跳过该项而非报错。
func (s *Service) seedEmojiGroupIcons(ctx context.Context, stats *SeedStats) error {
	for _, spec := range emojiGroupIconSpecs {
		existing, found, err := s.media.GetDocument(ctx, spec.NewID)
		if err != nil {
			return err
		}
		// Shadows made before they copied the source's raster thumb get it now.
		if found && existing.ID != 0 && photoSizesHaveRaster(existing.Thumbs) {
			continue
		}
		src, found, err := s.media.GetDocument(ctx, spec.SourceDocID)
		if err != nil {
			return err
		}
		if !found || src.ID == 0 {
			continue
		}
		blob, found, err := s.media.GetFileBlob(ctx, fmt.Sprintf("doc:%d", spec.SourceDocID))
		if err != nil {
			return err
		}
		if !found {
			continue
		}

		attrs := []domain.DocumentAttribute{
			{Kind: domain.DocAttrCustomEmoji, Alt: spec.Alt},
		}
		for _, a := range src.Attributes {
			if a.Kind == domain.DocAttrImageSize {
				attrs = append(attrs, a)
				break
			}
		}

		doc := domain.Document{
			ID:         spec.NewID,
			AccessHash: src.AccessHash,
			Date:       src.Date,
			MimeType:   src.MimeType,
			Size:       src.Size,
			DCID:       src.DCID,
			Attributes: attrs,
			// The raster thumb is what clients draw a frozen custom emoji from.
			Thumbs: append([]domain.PhotoSize(nil), src.Thumbs...),
		}
		for _, thumb := range doc.Thumbs {
			if thumb.Kind != domain.PhotoSizeKindDefault || thumb.Type == "" {
				continue // cached thumbs get their blob from ensureSeedCachedThumbBlobs
			}
			thumbBlob, found, err := s.media.GetFileBlob(ctx, fmt.Sprintf("doc:%d:%s", spec.SourceDocID, thumb.Type))
			if err != nil {
				return err
			}
			if found {
				thumbBlob.LocationKey = fmt.Sprintf("doc:%d:%s", spec.NewID, thumb.Type)
				if err := s.media.PutFileBlob(ctx, thumbBlob); err != nil {
					return err
				}
			}
		}
		if err := s.ensureSeedCachedThumbBlobs(ctx, doc, stats); err != nil {
			return err
		}
		if err := s.media.PutDocument(ctx, doc); err != nil {
			return err
		}
		if err := s.media.PutFileBlob(ctx, domain.FileBlob{
			LocationKey: fmt.Sprintf("doc:%d", spec.NewID),
			Backend:     blob.Backend,
			ObjectKey:   blob.ObjectKey,
			Size:        blob.Size,
			MimeType:    blob.MimeType,
		}); err != nil {
			return err
		}
		stats.Documents++
	}
	return nil
}
