-- The operator's gift-pack shelf.
--
-- Packs used to be compiled into the server and listed unconditionally, so
-- every install started with the same gifts on offer. They are archives now:
-- a fresh server's shelf is empty, an operator uploads a pack .zip, and only
-- then can it be previewed and imported -- either whole or one gift at a
-- time.
--
-- The archive lives here rather than in blob storage because a pack is small,
-- rarely read, and always read as one unit; keeping it in the same
-- transaction as its metadata means a stored pack can never be a row
-- pointing at a blob that was never written.
CREATE TABLE IF NOT EXISTS public.gift_packs (
    pack_id     text PRIMARY KEY,
    name        text NOT NULL,
    author      text NOT NULL DEFAULT '',
    description text NOT NULL DEFAULT '',
    file_name   text NOT NULL DEFAULT '',
    gift_count  integer NOT NULL DEFAULT 0,
    size_bytes  integer NOT NULL DEFAULT 0,
    manifest    jsonb NOT NULL,
    archive     bytea NOT NULL,
    uploaded_by text NOT NULL DEFAULT '',
    uploaded_at timestamp with time zone NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS gift_packs_uploaded_at_idx ON public.gift_packs (uploaded_at DESC);
