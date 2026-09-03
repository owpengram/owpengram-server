-- Adds a per-document media category column so the storage retention sweep
-- can use a different retention age per category (Photo/Video/RoundVideo/
-- Gif/Music/Voice/File/Avatar -- see TELESRV_STORAGE_RETENTION_MAX_AGE_*)
-- instead of one shared age for every document regardless of kind.
--
-- The value mirrors domain.MediaCategory (int16): 0 = None/unclassified
-- (stickers and anything else classifyDocumentCategory doesn't tag -- these
-- always fall back to the shared global age, there is no per-category
-- override for this bucket), 2 = Video, 3 = Gif, 4 = File, 5 = Music,
-- 6 = Voice, 7 = RoundVideo. Deliberately NOT backfilled for existing rows:
-- they stay at the default 0 (global age) until they are next
-- created/edited, which is an acceptable one-time transitional behavior --
-- backfilling would require re-deriving the category from each document's
-- already-stored attributes for potentially every row in the table.
ALTER TABLE public.documents
    ADD COLUMN category smallint NOT NULL DEFAULT 0;

CREATE INDEX documents_category_created_at_idx ON public.documents (category, created_at);
