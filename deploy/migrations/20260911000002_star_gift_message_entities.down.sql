ALTER TABLE public.star_gift_purchase_forms
    DROP CONSTRAINT IF EXISTS star_gift_purchase_forms_message_entities_array_check,
    DROP COLUMN IF EXISTS message_entities;

ALTER TABLE public.peer_star_gifts
    DROP CONSTRAINT IF EXISTS peer_star_gifts_message_entities_array_check,
    DROP COLUMN IF EXISTS message_entities;
