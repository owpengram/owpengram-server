DROP TRIGGER IF EXISTS communities_bump_catalog_read_model ON public.communities;
DROP FUNCTION IF EXISTS public.telesrv_bump_community_catalog_read_model();
DELETE FROM public.read_model_versions
WHERE model = 'community_catalog'
  AND owner_user_id = 0
  AND peer_type = 'community'
  AND peer_id = 0;
