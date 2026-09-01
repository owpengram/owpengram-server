DROP INDEX IF EXISTS public.sticker_sets_system_key_idx;

CREATE INDEX sticker_sets_system_key_idx
  ON public.sticker_sets USING btree (system_key)
  WHERE system_key <> ''::text;
