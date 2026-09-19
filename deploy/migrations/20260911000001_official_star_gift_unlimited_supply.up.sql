-- Official unlimited gifts have availability_total=0. The admin UI previously
-- replaced that zero with one before import, exhausting the collectible pool
-- after its first upgrade. Repair only the identifiable affected shape: an
-- active official, unlimited catalog revision with multiple advertised
-- variants and an active one-item official collectible revision.
--
-- Published revisions are normally immutable. This migration is a one-time
-- correction of importer-produced metadata, so the guard is disabled only for
-- the bounded UPDATE and restored in the same transaction.
ALTER TABLE public.star_gift_collectible_revisions
    DISABLE TRIGGER star_gift_collectible_revision_guard;

UPDATE public.star_gift_collectible_revisions collectible
SET supply_total = catalog_revision.upgrade_variants
FROM public.star_gift_catalog catalog
JOIN public.star_gift_catalog_revisions catalog_revision
  ON catalog_revision.id = catalog.active_revision_id
WHERE catalog.collectible_revision_id = collectible.id
  AND collectible.status = 'published'
  AND collectible.official_gift_id IS NOT NULL
  AND catalog_revision.official_gift_id = collectible.official_gift_id
  AND NOT catalog_revision.limited
  AND catalog_revision.availability_total = 0
  AND catalog_revision.upgrade_variants > 1
  AND collectible.supply_total = 1;

ALTER TABLE public.star_gift_collectible_revisions
    ENABLE TRIGGER star_gift_collectible_revision_guard;

-- Updating the catalog emits the standard cross-instance read-model
-- invalidation and makes already-running RPC processes reload the repaired
-- capacity before projecting upgrade availability.
UPDATE public.star_gift_catalog catalog
SET updated_at = now()
FROM public.star_gift_catalog_revisions catalog_revision,
     public.star_gift_collectible_revisions collectible
WHERE catalog_revision.id = catalog.active_revision_id
  AND collectible.id = catalog.collectible_revision_id
  AND collectible.status = 'published'
  AND collectible.official_gift_id IS NOT NULL
  AND catalog_revision.official_gift_id = collectible.official_gift_id
  AND NOT catalog_revision.limited
  AND catalog_revision.availability_total = 0
  AND catalog_revision.upgrade_variants > 1
  AND collectible.supply_total = catalog_revision.upgrade_variants;
