DROP TRIGGER IF EXISTS channels_peer_identity_created ON public.channels;
DROP TRIGGER IF EXISTS users_peer_identity_created ON public.users;
DROP FUNCTION IF EXISTS public.telesrv_seed_peer_identity_on_insert();

-- Backfilled read_model_versions rows are intentionally retained. 20260901000014 defines
-- one token for every durable peer; deleting them would violate the older
-- migration's contract after rollback and make negative facts uncacheable.
