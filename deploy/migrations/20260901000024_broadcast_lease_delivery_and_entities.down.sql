DROP INDEX IF EXISTS public.broadcast_recipients_processing_idx;
DROP INDEX IF EXISTS public.broadcast_recipients_pending_next_attempt_idx;

ALTER TABLE public.broadcast_recipients
    DROP CONSTRAINT IF EXISTS broadcast_recipients_lease_check,
    DROP CONSTRAINT IF EXISTS broadcast_recipients_sent_tracking_check,
    DROP CONSTRAINT IF EXISTS broadcast_recipients_attempts_check,
    DROP CONSTRAINT IF EXISTS broadcast_recipients_status_check;

ALTER TABLE public.broadcast_recipients
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS pts,
    DROP COLUMN IF EXISTS message_box_id,
    DROP COLUMN IF EXISTS private_message_id,
    DROP COLUMN IF EXISTS lease_until,
    DROP COLUMN IF EXISTS lease_token,
    DROP COLUMN IF EXISTS next_attempt_at;

DROP INDEX IF EXISTS public.broadcasts_enumeration_idx;

ALTER TABLE public.broadcasts
    DROP CONSTRAINT IF EXISTS broadcasts_sent_failed_within_materialized_check,
    DROP CONSTRAINT IF EXISTS broadcasts_enumeration_cursor_check,
    DROP CONSTRAINT IF EXISTS broadcasts_entities_array_check;

ALTER TABLE public.broadcasts
    DROP COLUMN IF EXISTS failed_count,
    DROP COLUMN IF EXISTS sent_count,
    DROP COLUMN IF EXISTS materialized_count,
    DROP COLUMN IF EXISTS enumeration_done,
    DROP COLUMN IF EXISTS enumeration_cursor_user_id,
    DROP COLUMN IF EXISTS snapshot_max_user_id,
    DROP COLUMN IF EXISTS entities;

ALTER TABLE public.broadcasts
    DROP CONSTRAINT IF EXISTS broadcasts_target_count_check;
ALTER TABLE public.broadcasts
    ALTER COLUMN target_count TYPE integer,
    ALTER COLUMN target_count SET DEFAULT 0;
ALTER TABLE public.broadcasts
    RENAME COLUMN target_count TO total_count;
