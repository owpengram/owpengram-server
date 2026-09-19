ALTER TABLE public.peer_star_gifts
    ADD COLUMN message_entities jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE public.star_gift_purchase_forms
    ADD COLUMN message_entities jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE public.peer_star_gifts
    ADD CONSTRAINT peer_star_gifts_message_entities_array_check
    CHECK (jsonb_typeof(message_entities) = 'array');

ALTER TABLE public.star_gift_purchase_forms
    ADD CONSTRAINT star_gift_purchase_forms_message_entities_array_check
    CHECK (jsonb_typeof(message_entities) = 'array');
