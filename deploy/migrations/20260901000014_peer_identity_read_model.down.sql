DROP TRIGGER IF EXISTS custom_verifications_peer_identity_changed ON public.custom_verifications;
DROP TRIGGER IF EXISTS peer_usernames_peer_identity_changed ON public.peer_usernames;
DROP FUNCTION IF EXISTS public.telesrv_notify_peer_identity_row();
DROP FUNCTION IF EXISTS public.telesrv_bump_peer_identity(text, bigint);

DELETE FROM public.read_model_versions
WHERE model = 'peer_identity';
