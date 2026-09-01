-- Upgrades our existing broadcasts/broadcast_recipients tables (created by
-- 20260714003131_system_broadcasts.up.sql, already applied in production) to
-- upstream gramsrv's richer design: formatted entities, incremental
-- materialization of "all"-target campaigns instead of one giant upfront
-- INSERT, and a lease-based delivery worker safe against duplicate sends if
-- the worker restarts mid-cycle.
--
-- This is an ALTER-based migration on purpose: it must not DROP/CREATE these
-- tables, because production already has rows in them (including 'sent'
-- rows from the old code path that never recorded a private_message_id/
-- message_box_id/pts, since that tracking didn't exist yet). The CHECK
-- constraints below explicitly carve out that legacy shape as valid
-- alongside the new fully-tracked shape, so this migration applies cleanly
-- against live data without any hand-editing.

-- broadcasts: rename total_count -> target_count (upstream's name for the
-- same "how many recipients this campaign targets" figure) and widen it to
-- bigint to match. A rename is metadata-only, so existing values survive
-- untouched.
ALTER TABLE public.broadcasts
    RENAME COLUMN total_count TO target_count;
ALTER TABLE public.broadcasts
    ALTER COLUMN target_count TYPE bigint,
    ALTER COLUMN target_count SET DEFAULT 0;
ALTER TABLE public.broadcasts
    ADD CONSTRAINT broadcasts_target_count_check CHECK (target_count >= 0);

ALTER TABLE public.broadcasts
    ADD COLUMN entities jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN snapshot_max_user_id bigint NOT NULL DEFAULT 0,
    ADD COLUMN enumeration_cursor_user_id bigint NOT NULL DEFAULT 0,
    ADD COLUMN enumeration_done boolean NOT NULL DEFAULT false,
    ADD COLUMN materialized_count bigint NOT NULL DEFAULT 0 CHECK (materialized_count >= 0),
    ADD COLUMN sent_count bigint NOT NULL DEFAULT 0 CHECK (sent_count >= 0),
    ADD COLUMN failed_count bigint NOT NULL DEFAULT 0 CHECK (failed_count >= 0);

ALTER TABLE public.broadcasts
    ADD CONSTRAINT broadcasts_entities_array_check CHECK (jsonb_typeof(entities) = 'array');

-- Backfill: every pre-existing broadcast was created by the old code path,
-- which inserted every recipient row upfront in one transaction -- so from
-- the new model's point of view enumeration is already complete and fully
-- materialized for every one of them.
UPDATE public.broadcasts
SET enumeration_done = true,
    enumeration_cursor_user_id = snapshot_max_user_id,
    materialized_count = target_count;

-- Backfill sent_count/failed_count from the actual recipient rows rather
-- than trusting target_count, so old broadcasts read correctly under the
-- new derived-elsewhere-no-more columns instead of showing zeros.
UPDATE public.broadcasts b
SET sent_count = counts.sent_count,
    failed_count = counts.failed_count
FROM (
    SELECT broadcast_id,
           count(*) FILTER (WHERE status = 'sent')::bigint AS sent_count,
           count(*) FILTER (WHERE status = 'failed')::bigint AS failed_count
    FROM public.broadcast_recipients
    GROUP BY broadcast_id
) AS counts
WHERE b.id = counts.broadcast_id;

ALTER TABLE public.broadcasts
    ADD CONSTRAINT broadcasts_enumeration_cursor_check
        CHECK (enumeration_cursor_user_id >= 0 AND enumeration_cursor_user_id <= snapshot_max_user_id),
    ADD CONSTRAINT broadcasts_sent_failed_within_materialized_check
        CHECK (sent_count + failed_count <= materialized_count);

CREATE INDEX broadcasts_enumeration_idx ON public.broadcasts (id)
    WHERE target_mode = 'all' AND NOT enumeration_done;

-- broadcast_recipients: add the lease-based delivery columns. Defaults give
-- every existing row sane values (next_attempt_at = now(), no active lease,
-- no tracked delivery identifiers) with no data loss.
ALTER TABLE public.broadcast_recipients
    ADD COLUMN next_attempt_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN lease_token varchar(64) NOT NULL DEFAULT '',
    ADD COLUMN lease_until timestamptz,
    ADD COLUMN private_message_id bigint NOT NULL DEFAULT 0,
    ADD COLUMN message_box_id integer NOT NULL DEFAULT 0,
    ADD COLUMN pts integer NOT NULL DEFAULT 0,
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

-- The original table had no CHECK on status at all, so widening the allowed
-- set to include 'processing' needs no data fixup -- every existing row is
-- already 'pending', 'sent', or 'failed'.
ALTER TABLE public.broadcast_recipients
    ADD CONSTRAINT broadcast_recipients_status_check
        CHECK (status IN ('pending', 'processing', 'sent', 'failed')),
    ADD CONSTRAINT broadcast_recipients_attempts_check CHECK (attempts >= 0);

-- The landmine: upstream's CHECK requires every 'sent' row to carry a
-- positive private_message_id/message_box_id/pts. Production already has
-- 'sent' rows from before that tracking existed, all with those columns at
-- their just-added default of 0. Rather than reject that data (or worse,
-- silently corrupt it with fabricated ids), this CHECK treats
-- "sent with all three still 0" as a legitimate legacy/untracked case,
-- alongside the real "sent with all three populated" case. New code always
-- populates them on a genuine send, so only pre-migration rows will ever
-- take the legacy branch.
ALTER TABLE public.broadcast_recipients
    ADD CONSTRAINT broadcast_recipients_sent_tracking_check
        CHECK (
            (status = 'sent' AND sent_at IS NOT NULL AND (
                (private_message_id > 0 AND message_box_id > 0 AND pts > 0)
                OR
                (private_message_id = 0 AND message_box_id = 0 AND pts = 0)
            ))
            OR
            (status <> 'sent' AND private_message_id = 0 AND message_box_id = 0 AND pts = 0 AND sent_at IS NULL)
        );

-- No legacy carve-out needed here: lease_token/lease_until default to
-- ''/NULL, and no pre-existing row is 'processing' (that status didn't
-- exist before this migration), so every existing row already satisfies the
-- "not processing => no lease" branch.
ALTER TABLE public.broadcast_recipients
    ADD CONSTRAINT broadcast_recipients_lease_check
        CHECK (
            (status = 'processing' AND lease_token <> '' AND lease_until IS NOT NULL)
            OR
            (status <> 'processing' AND lease_token = '' AND lease_until IS NULL)
        );

CREATE INDEX broadcast_recipients_pending_next_attempt_idx
    ON public.broadcast_recipients (next_attempt_at, id)
    WHERE status = 'pending';
CREATE INDEX broadcast_recipients_processing_idx
    ON public.broadcast_recipients (lease_until, id)
    WHERE status = 'processing';
