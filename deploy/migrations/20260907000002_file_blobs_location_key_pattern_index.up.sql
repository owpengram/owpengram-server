-- The admin storage aggregates join file_blobs to documents/photos by building
-- the key at runtime:
--
--     fb.location_key = 'doc:' || d.id::text
--     OR fb.location_key LIKE 'doc:' || d.id::text || ':%'
--
-- The equality half is served by file_blobs_pkey, but the LIKE half is not:
-- the database is initialised without an explicit locale, so it runs under
-- en_US.utf8, and a default btree on text cannot answer a prefix LIKE there.
-- Every document and photo row therefore forced a sequential scan of
-- file_blobs, which is what made the admin dashboard take ~30s on prod.
--
-- text_pattern_ops indexes the column by byte order instead of collation
-- order, which is exactly what a prefix match needs. The pkey is left alone --
-- it still serves equality and the uniqueness constraint.
CREATE INDEX CONCURRENTLY IF NOT EXISTS file_blobs_location_key_pattern_idx
    ON public.file_blobs (location_key text_pattern_ops);
