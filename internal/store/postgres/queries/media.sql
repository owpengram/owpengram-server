-- upload_parts ----------------------------------------------------------------

-- name: SaveUploadPart :exec
INSERT INTO upload_parts (owner_user_id, file_id, part, total_parts, is_big, backend, object_key, size, sha256)
VALUES (
  sqlc.arg(owner_user_id)::bigint,
  sqlc.arg(file_id)::bigint,
  sqlc.arg(part)::int,
  sqlc.arg(total_parts)::int,
  sqlc.arg(is_big)::boolean,
  sqlc.arg(backend)::text,
  sqlc.arg(object_key)::text,
  sqlc.arg(size)::bigint,
  sqlc.arg(sha256)::bytea
)
ON CONFLICT (owner_user_id, file_id, part) DO UPDATE SET
  total_parts = EXCLUDED.total_parts,
  is_big = EXCLUDED.is_big,
  backend = EXCLUDED.backend,
  object_key = EXCLUDED.object_key,
  size = EXCLUDED.size,
  sha256 = EXCLUDED.sha256,
  created_at = now();

-- name: GetUploadPartUsage :one
SELECT
  COALESCE(SUM(size), 0)::bigint AS bytes,
  COUNT(*)::int AS parts,
  COUNT(DISTINCT file_id)::int AS files
FROM upload_parts
WHERE owner_user_id = sqlc.arg(owner_user_id)::bigint;

-- name: GetUploadPartSlot :one
SELECT
  COALESCE((
    SELECT size
    FROM upload_parts
    WHERE owner_user_id = sqlc.arg(owner_user_id)::bigint
      AND file_id = sqlc.arg(file_id)::bigint
      AND part = sqlc.arg(part)::int
  ), -1)::bigint AS existing_bytes,
  COALESCE((
    SELECT object_key
    FROM upload_parts
    WHERE owner_user_id = sqlc.arg(owner_user_id)::bigint
      AND file_id = sqlc.arg(file_id)::bigint
      AND part = sqlc.arg(part)::int
  ), '')::text AS object_key,
  (
    SELECT COUNT(*)::int
    FROM upload_parts
    WHERE owner_user_id = sqlc.arg(owner_user_id)::bigint
      AND file_id = sqlc.arg(file_id)::bigint
  )::int AS file_parts;

-- name: ListUploadParts :many
SELECT part, total_parts, is_big, backend, object_key, size, sha256
FROM upload_parts
WHERE owner_user_id = sqlc.arg(owner_user_id)::bigint
  AND file_id = sqlc.arg(file_id)::bigint
ORDER BY part ASC;

-- name: DeleteUploadParts :many
DELETE FROM upload_parts
WHERE owner_user_id = sqlc.arg(owner_user_id)::bigint
  AND file_id = sqlc.arg(file_id)::bigint
RETURNING object_key;

-- name: DeleteExpiredUploadParts :many
WITH doomed AS (
  SELECT owner_user_id, file_id, part
  FROM upload_parts
  WHERE created_at < sqlc.arg(before)::timestamptz
  ORDER BY created_at ASC
  LIMIT sqlc.arg(batch_limit)::int
)
DELETE FROM upload_parts p
USING doomed d
WHERE p.owner_user_id = d.owner_user_id
  AND p.file_id = d.file_id
  AND p.part = d.part
RETURNING p.object_key;

-- file_blobs ------------------------------------------------------------------

-- name: PutFileBlob :exec
INSERT INTO file_blobs (location_key, backend, object_key, size, sha256, mime_type)
VALUES (
  sqlc.arg(location_key)::text,
  sqlc.arg(backend)::text,
  sqlc.arg(object_key)::text,
  sqlc.arg(size)::bigint,
  sqlc.arg(sha256)::bytea,
  sqlc.arg(mime_type)::text
)
ON CONFLICT (location_key) DO UPDATE SET
  backend = EXCLUDED.backend,
  object_key = EXCLUDED.object_key,
  size = EXCLUDED.size,
  sha256 = EXCLUDED.sha256,
  mime_type = EXCLUDED.mime_type;

-- name: GetFileBlob :one
SELECT location_key, backend, object_key, size, sha256, mime_type
FROM file_blobs
WHERE location_key = sqlc.arg(location_key)::text;

-- documents -------------------------------------------------------------------

-- name: PutDocument :exec
INSERT INTO documents (id, access_hash, file_reference, date, mime_type, size, dc_id, attributes, thumbs, owner_user_id)
VALUES (
  sqlc.arg(id)::bigint,
  sqlc.arg(access_hash)::bigint,
  sqlc.arg(file_reference)::bytea,
  sqlc.arg(date)::int,
  sqlc.arg(mime_type)::text,
  sqlc.arg(size)::bigint,
  sqlc.arg(dc_id)::int,
  sqlc.arg(attributes_json)::jsonb,
  sqlc.arg(thumbs_json)::jsonb,
  sqlc.arg(owner_user_id)::bigint
)
-- owner_user_id is intentionally NOT in the UPDATE SET list: a document id is
-- only ever (re-)upserted by its original uploader's own request replay, and
-- keeping the first-write owner sticky avoids any risk of a later call
-- (e.g. a forward re-touching the row) reassigning ownership.
ON CONFLICT (id) DO UPDATE SET
  access_hash = EXCLUDED.access_hash,
  file_reference = EXCLUDED.file_reference,
  date = EXCLUDED.date,
  mime_type = EXCLUDED.mime_type,
  size = EXCLUDED.size,
  dc_id = EXCLUDED.dc_id,
  attributes = EXCLUDED.attributes,
  thumbs = EXCLUDED.thumbs;

-- name: GetDocument :one
SELECT id, access_hash, file_reference, date, mime_type, size, dc_id,
  attributes::text AS attributes_json,
  thumbs::text AS thumbs_json,
  owner_user_id
FROM documents
WHERE id = sqlc.arg(id)::bigint;

-- name: GetDocuments :many
SELECT id, access_hash, file_reference, date, mime_type, size, dc_id,
  attributes::text AS attributes_json,
  thumbs::text AS thumbs_json,
  owner_user_id
FROM documents
WHERE id = ANY(sqlc.arg(ids)::bigint[]);

-- name: DeleteDocumentRow :exec
DELETE FROM documents WHERE id = sqlc.arg(id)::bigint;

-- photos ----------------------------------------------------------------------

-- name: PutPhoto :exec
INSERT INTO photos (id, access_hash, file_reference, date, dc_id, has_stickers, sizes, owner_user_id)
VALUES (
  sqlc.arg(id)::bigint,
  sqlc.arg(access_hash)::bigint,
  sqlc.arg(file_reference)::bytea,
  sqlc.arg(date)::int,
  sqlc.arg(dc_id)::int,
  sqlc.arg(has_stickers)::boolean,
  sqlc.arg(sizes_json)::jsonb,
  sqlc.arg(owner_user_id)::bigint
)
-- owner_user_id intentionally not updated on conflict, see PutDocument.
ON CONFLICT (id) DO UPDATE SET
  access_hash = EXCLUDED.access_hash,
  file_reference = EXCLUDED.file_reference,
  date = EXCLUDED.date,
  dc_id = EXCLUDED.dc_id,
  has_stickers = EXCLUDED.has_stickers,
  sizes = EXCLUDED.sizes;

-- name: GetPhoto :one
SELECT id, access_hash, file_reference, date, dc_id, has_stickers,
  sizes::text AS sizes_json,
  owner_user_id
FROM photos
WHERE id = sqlc.arg(id)::bigint;

-- name: DeletePhotoRow :exec
DELETE FROM photos WHERE id = sqlc.arg(id)::bigint;

-- media_references / storage retention -----------------------------------------

-- name: InsertMediaReference :exec
INSERT INTO media_references (media_kind, media_id, ref_kind, ref_key)
VALUES (sqlc.arg(media_kind)::text, sqlc.arg(media_id)::bigint, sqlc.arg(ref_kind)::text, sqlc.arg(ref_key)::text)
ON CONFLICT DO NOTHING;

-- name: ClearDocumentOrphan :exec
UPDATE documents SET orphaned_at = NULL
WHERE id = sqlc.arg(media_id)::bigint AND orphaned_at IS NOT NULL;

-- name: ClearPhotoOrphan :exec
UPDATE photos SET orphaned_at = NULL
WHERE id = sqlc.arg(media_id)::bigint AND orphaned_at IS NOT NULL;

-- name: RemoveMediaReference :exec
DELETE FROM media_references
WHERE media_kind = sqlc.arg(media_kind)::text
  AND media_id = sqlc.arg(media_id)::bigint
  AND ref_kind = sqlc.arg(ref_kind)::text
  AND ref_key = sqlc.arg(ref_key)::text;

-- name: OrphanDocumentIfUnreferenced :exec
UPDATE documents SET orphaned_at = now()
WHERE id = sqlc.arg(media_id)::bigint
  AND orphaned_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM media_references WHERE media_kind = 'document' AND media_id = sqlc.arg(media_id)::bigint
  );

-- name: OrphanPhotoIfUnreferenced :exec
UPDATE photos SET orphaned_at = now()
WHERE id = sqlc.arg(media_id)::bigint
  AND orphaned_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM media_references WHERE media_kind = 'photo' AND media_id = sqlc.arg(media_id)::bigint
  );

-- name: ListOrphanedDocumentIDsOlderThan :many
SELECT id FROM documents
WHERE orphaned_at IS NOT NULL AND orphaned_at < sqlc.arg(cutoff)::timestamptz
ORDER BY orphaned_at ASC
LIMIT sqlc.arg(batch_limit)::int;

-- name: ListOrphanedPhotoIDsOlderThan :many
SELECT id FROM photos
WHERE orphaned_at IS NOT NULL AND orphaned_at < sqlc.arg(cutoff)::timestamptz
ORDER BY orphaned_at ASC
LIMIT sqlc.arg(batch_limit)::int;

-- name: ListDocumentIDsForHardRetentionOlderThan :many
-- "Hard" retention mode: candidates are documents older than cutoff (by
-- upload/created_at, NOT orphaned_at -- a live reference does not exempt
-- them) that still own at least one file_blobs row. The EXISTS check is what
-- keeps this sweep from re-selecting the same document forever: once its
-- blob bytes are purged (DeleteFileBlobsForDocument removes the file_blobs
-- rows but deliberately leaves this documents row in place), it naturally
-- drops out of this query on the next pass.
SELECT d.id FROM documents d
WHERE d.created_at < sqlc.arg(cutoff)::timestamptz
  AND EXISTS (
    SELECT 1 FROM file_blobs fb
    WHERE fb.location_key = 'doc:' || d.id::text
       OR fb.location_key LIKE 'doc:' || d.id::text || ':%'
  )
ORDER BY d.created_at ASC
LIMIT sqlc.arg(batch_limit)::int;

-- name: ListPhotoIDsForHardRetentionOlderThan :many
-- See ListDocumentIDsForHardRetentionOlderThan -- same "hard" retention
-- candidate selection, for photos.
SELECT p.id FROM photos p
WHERE p.created_at < sqlc.arg(cutoff)::timestamptz
  AND EXISTS (
    SELECT 1 FROM file_blobs fb
    WHERE fb.location_key = 'photo:' || p.id::text
       OR fb.location_key LIKE 'photo:' || p.id::text || ':%'
  )
ORDER BY p.created_at ASC
LIMIT sqlc.arg(batch_limit)::int;

-- name: CountFileBlobRefs :one
SELECT COUNT(*)::int FROM file_blobs WHERE backend = sqlc.arg(backend)::text AND object_key = sqlc.arg(object_key)::text;

-- name: DeleteFileBlobRow :exec
DELETE FROM file_blobs WHERE location_key = sqlc.arg(location_key)::text;

-- name: ListFileBlobsByLocationPrefix :many
-- Matches a media's main blob (exact_key, e.g. "doc:123") plus every
-- variant keyed off it (prefix_pattern, e.g. "doc:123:%" for thumbnails /
-- "photo:456:%" for each rendition size) -- a document/photo can own
-- multiple file_blobs rows.
SELECT location_key, backend, object_key, size
FROM file_blobs
WHERE location_key = sqlc.arg(exact_key)::text
   OR location_key LIKE sqlc.arg(prefix_pattern)::text;

-- name: SumFileBlobBytes :one
-- Physical bytes actually held by the blob backend (dedup-aware: identical
-- content uploaded by different users is one row here). Used by the
-- low-space guard's cached usage gauge and the admin panel's "physical
-- usage" stat.
SELECT COALESCE(SUM(size), 0)::bigint FROM file_blobs;

-- sticker_sets ----------------------------------------------------------------

-- name: PutStickerSet :exec
INSERT INTO sticker_sets (
  id, access_hash, short_name, title, count, hash, set_kind,
  official, animated, videos, emojis, masks, installed, archived, installed_date,
  thumb_document_id, thumbs, thumb_dc_id, thumb_version, document_ids, packs, sort_order, system_key
) VALUES (
  sqlc.arg(id)::bigint,
  sqlc.arg(access_hash)::bigint,
  sqlc.arg(short_name)::text,
  sqlc.arg(title)::text,
  sqlc.arg(count)::int,
  sqlc.arg(hash)::int,
  sqlc.arg(set_kind)::text,
  sqlc.arg(official)::boolean,
  sqlc.arg(animated)::boolean,
  sqlc.arg(videos)::boolean,
  sqlc.arg(emojis)::boolean,
  sqlc.arg(masks)::boolean,
  sqlc.arg(installed)::boolean,
  sqlc.arg(archived)::boolean,
  sqlc.arg(installed_date)::int,
  sqlc.arg(thumb_document_id)::bigint,
  sqlc.arg(thumbs_json)::jsonb,
  sqlc.arg(thumb_dc_id)::int,
  sqlc.arg(thumb_version)::int,
  sqlc.arg(document_ids_json)::jsonb,
  sqlc.arg(packs_json)::jsonb,
  sqlc.arg(sort_order)::int,
  sqlc.arg(system_key)::text
)
ON CONFLICT (id) DO UPDATE SET
  access_hash = EXCLUDED.access_hash,
  short_name = EXCLUDED.short_name,
  title = EXCLUDED.title,
  count = EXCLUDED.count,
  hash = EXCLUDED.hash,
  set_kind = EXCLUDED.set_kind,
  official = EXCLUDED.official,
  animated = EXCLUDED.animated,
  videos = EXCLUDED.videos,
  emojis = EXCLUDED.emojis,
  masks = EXCLUDED.masks,
  installed = EXCLUDED.installed,
  archived = EXCLUDED.archived,
  installed_date = EXCLUDED.installed_date,
  thumb_document_id = EXCLUDED.thumb_document_id,
  thumbs = EXCLUDED.thumbs,
  thumb_dc_id = EXCLUDED.thumb_dc_id,
  thumb_version = EXCLUDED.thumb_version,
  document_ids = EXCLUDED.document_ids,
  packs = EXCLUDED.packs,
  sort_order = EXCLUDED.sort_order,
  system_key = EXCLUDED.system_key;

-- name: GetStickerSetByID :one
SELECT
  id, access_hash, short_name, title, count, hash, set_kind,
  official, animated, videos, emojis, masks, installed, archived, installed_date,
  thumb_document_id, thumbs::text AS thumbs_json, thumb_dc_id, thumb_version,
  document_ids::text AS document_ids_json, packs::text AS packs_json, sort_order, system_key
FROM sticker_sets
WHERE id = sqlc.arg(id)::bigint;

-- name: GetStickerSetByShortName :one
SELECT
  id, access_hash, short_name, title, count, hash, set_kind,
  official, animated, videos, emojis, masks, installed, archived, installed_date,
  thumb_document_id, thumbs::text AS thumbs_json, thumb_dc_id, thumb_version,
  document_ids::text AS document_ids_json, packs::text AS packs_json, sort_order, system_key
FROM sticker_sets
WHERE short_name = sqlc.arg(short_name)::text;

-- name: GetStickerSetBySystemKey :one
SELECT
  id, access_hash, short_name, title, count, hash, set_kind,
  official, animated, videos, emojis, masks, installed, archived, installed_date,
  thumb_document_id, thumbs::text AS thumbs_json, thumb_dc_id, thumb_version,
  document_ids::text AS document_ids_json, packs::text AS packs_json, sort_order, system_key
FROM sticker_sets
WHERE system_key = sqlc.arg(system_key)::text;

-- name: ListStickerSetsByKind :many
SELECT
  id, access_hash, short_name, title, count, hash, set_kind,
  official, animated, videos, emojis, masks, installed, archived, installed_date,
  thumb_document_id, thumbs::text AS thumbs_json, thumb_dc_id, thumb_version,
  document_ids::text AS document_ids_json, packs::text AS packs_json, sort_order, system_key
FROM sticker_sets
WHERE set_kind = sqlc.arg(set_kind)::text
ORDER BY sort_order ASC, id ASC;

-- name: CountStickerSets :one
SELECT count(*)::int AS total FROM sticker_sets;

-- available_reactions ---------------------------------------------------------

-- name: PutAvailableReaction :exec
INSERT INTO available_reactions (
  reaction, title, inactive, premium,
  static_icon_id, appear_animation_id, select_animation_id,
  activate_animation_id, effect_animation_id, around_animation_id, center_icon_id, sort_order
) VALUES (
  sqlc.arg(reaction)::text,
  sqlc.arg(title)::text,
  sqlc.arg(inactive)::boolean,
  sqlc.arg(premium)::boolean,
  sqlc.arg(static_icon_id)::bigint,
  sqlc.arg(appear_animation_id)::bigint,
  sqlc.arg(select_animation_id)::bigint,
  sqlc.arg(activate_animation_id)::bigint,
  sqlc.arg(effect_animation_id)::bigint,
  sqlc.arg(around_animation_id)::bigint,
  sqlc.arg(center_icon_id)::bigint,
  sqlc.arg(sort_order)::int
)
ON CONFLICT (reaction) DO UPDATE SET
  title = EXCLUDED.title,
  inactive = EXCLUDED.inactive,
  premium = EXCLUDED.premium,
  static_icon_id = EXCLUDED.static_icon_id,
  appear_animation_id = EXCLUDED.appear_animation_id,
  select_animation_id = EXCLUDED.select_animation_id,
  activate_animation_id = EXCLUDED.activate_animation_id,
  effect_animation_id = EXCLUDED.effect_animation_id,
  around_animation_id = EXCLUDED.around_animation_id,
  center_icon_id = EXCLUDED.center_icon_id,
  sort_order = EXCLUDED.sort_order;

-- name: ListAvailableReactions :many
SELECT
  reaction, title, inactive, premium,
  static_icon_id, appear_animation_id, select_animation_id,
  activate_animation_id, effect_animation_id, around_animation_id, center_icon_id, sort_order
FROM available_reactions
ORDER BY sort_order ASC, reaction ASC;

-- name: CountAvailableReactions :one
SELECT count(*)::int AS total FROM available_reactions;

-- profile_photos --------------------------------------------------------------

-- name: AddProfilePhoto :exec
INSERT INTO profile_photos (owner_peer_type, owner_peer_id, photo_id, date, active, sort_order)
VALUES (
  sqlc.arg(owner_peer_type)::text,
  sqlc.arg(owner_peer_id)::bigint,
  sqlc.arg(photo_id)::bigint,
  sqlc.arg(date)::int,
  true,
  sqlc.arg(sort_order)::bigint
)
ON CONFLICT (owner_peer_type, owner_peer_id, photo_id) DO UPDATE SET
  date = EXCLUDED.date,
  active = true,
  sort_order = EXCLUDED.sort_order;

-- name: NextProfilePhotoOrder :one
SELECT COALESCE(MAX(sort_order), 0)::bigint AS max_order
FROM profile_photos
WHERE owner_peer_type = sqlc.arg(owner_peer_type)::text
  AND owner_peer_id = sqlc.arg(owner_peer_id)::bigint;

-- name: CurrentProfilePhoto :one
SELECT photo_id
FROM profile_photos
WHERE owner_peer_type = sqlc.arg(owner_peer_type)::text
  AND owner_peer_id = sqlc.arg(owner_peer_id)::bigint
  AND active
ORDER BY sort_order DESC
LIMIT 1;

-- name: CurrentProfilePhotosForOwners :many
SELECT DISTINCT ON (pp.owner_peer_id)
  pp.owner_peer_id,
  pp.photo_id,
  ph.dc_id,
  ph.sizes::text AS sizes_json
FROM profile_photos pp
JOIN photos ph ON ph.id = pp.photo_id
WHERE pp.owner_peer_type = sqlc.arg(owner_peer_type)::text
  AND pp.owner_peer_id = ANY(sqlc.arg(owner_ids)::bigint[])
  AND pp.active
ORDER BY pp.owner_peer_id, pp.sort_order DESC;

-- name: ListProfilePhotos :many
SELECT photo_id
FROM profile_photos
WHERE owner_peer_type = sqlc.arg(owner_peer_type)::text
  AND owner_peer_id = sqlc.arg(owner_peer_id)::bigint
  AND active
  AND (sqlc.arg(max_id)::bigint <= 0 OR photo_id < sqlc.arg(max_id)::bigint)
ORDER BY sort_order DESC
OFFSET sqlc.arg(offset_count)::int
LIMIT sqlc.arg(limit_count)::int;

-- name: CountProfilePhotos :one
SELECT count(*)::int AS total
FROM profile_photos
WHERE owner_peer_type = sqlc.arg(owner_peer_type)::text
  AND owner_peer_id = sqlc.arg(owner_peer_id)::bigint
  AND active;

-- name: DeactivateProfilePhotos :many
UPDATE profile_photos
SET active = false
WHERE owner_peer_type = sqlc.arg(owner_peer_type)::text
  AND owner_peer_id = sqlc.arg(owner_peer_id)::bigint
  AND photo_id = ANY(sqlc.arg(photo_ids)::bigint[])
  AND active
RETURNING photo_id;
