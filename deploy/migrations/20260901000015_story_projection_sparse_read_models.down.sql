DROP TRIGGER IF EXISTS users_story_projection_versions_changed ON public.users;
DROP TRIGGER IF EXISTS channels_story_projection_version_changed ON public.channels;
DROP TRIGGER IF EXISTS telesrv_stories_story_peer_read_model ON public.stories;
DROP TRIGGER IF EXISTS telesrv_story_hidden_peers_story_peer_read_model ON public.story_hidden_peers;

DROP FUNCTION IF EXISTS public.telesrv_maintain_user_story_projection_versions();
DROP FUNCTION IF EXISTS public.telesrv_maintain_channel_story_projection_version();
DROP FUNCTION IF EXISTS public.telesrv_notify_story_row_read_models();
DROP FUNCTION IF EXISTS public.telesrv_notify_story_hidden_row_read_models();
DROP FUNCTION IF EXISTS public.telesrv_bump_story_hidden_list(bigint);
DROP FUNCTION IF EXISTS public.telesrv_bump_story_peer(text, bigint);

CREATE OR REPLACE FUNCTION public.telesrv_notify_story_peer_read_model()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    o_type text;
    o_id bigint;
BEGIN
    IF TG_OP = 'DELETE' THEN
        o_type := OLD.owner_peer_type;
        o_id := OLD.owner_peer_id;
    ELSE
        o_type := NEW.owner_peer_type;
        o_id := NEW.owner_peer_id;
    END IF;
    PERFORM public.telesrv_bump_read_model_version('story_peer', 0, o_type, o_id);
    RETURN NULL;
END;
$$;

CREATE TRIGGER telesrv_stories_story_peer_read_model
AFTER INSERT OR UPDATE OR DELETE ON public.stories
FOR EACH ROW EXECUTE FUNCTION public.telesrv_notify_story_peer_read_model();

CREATE TRIGGER telesrv_story_hidden_peers_story_peer_read_model
AFTER INSERT OR UPDATE OR DELETE ON public.story_hidden_peers
FOR EACH ROW EXECUTE FUNCTION public.telesrv_notify_story_peer_read_model();

DELETE FROM public.read_model_versions WHERE model = 'story_hidden_list';
-- story_peer rows seeded for peers without stories are harmless under the
-- previous contract and are retained so rollback cannot erase real versions.
