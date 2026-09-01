-- Global Community-catalog presence token. messages.getDialogs needs collapsed
-- Communities only when at least one live Community exists; without this
-- durable invalidation token an empty deployment would repeat the expensive
-- owner-membership query on every dialogs page.

CREATE OR REPLACE FUNCTION public.telesrv_bump_community_catalog_read_model()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM public.telesrv_bump_read_model_version(
        'community_catalog',
        0,
        'community',
        0
    );
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS communities_bump_catalog_read_model ON public.communities;
CREATE TRIGGER communities_bump_catalog_read_model
AFTER INSERT OR UPDATE OR DELETE ON public.communities
FOR EACH ROW EXECUTE FUNCTION public.telesrv_bump_community_catalog_read_model();

SELECT public.telesrv_bump_read_model_version(
    'community_catalog',
    0,
    'community',
    0
);
