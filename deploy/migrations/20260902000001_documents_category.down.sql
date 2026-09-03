DROP INDEX IF EXISTS public.documents_category_created_at_idx;
ALTER TABLE public.documents
    DROP COLUMN IF EXISTS category;
