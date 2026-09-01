-- Split story peer projection into a shared active-story gate and one sparse
-- hidden-peer snapshot per viewer. Both positive and negative facts receive a
-- durable token; active expiry is still enforced by expire_date at read time.

CREATE OR REPLACE FUNCTION public.telesrv_bump_story_peer(
    p_peer_type text,
    p_peer_id bigint
) RETURNS void
LANGUAGE plpgsql
AS $$
BEGIN
    IF COALESCE(p_peer_type, '') NOT IN ('user', 'channel')
       OR COALESCE(p_peer_id, 0) = 0 THEN
        RETURN;
    END IF;
    PERFORM public.telesrv_bump_read_model_version(
        'story_peer', 0, p_peer_type, p_peer_id
    );
END;
$$;

CREATE OR REPLACE FUNCTION public.telesrv_bump_story_hidden_list(
    p_viewer_user_id bigint
) RETURNS void
LANGUAGE plpgsql
AS $$
BEGIN
    IF COALESCE(p_viewer_user_id, 0) = 0 THEN
        RETURN;
    END IF;
    PERFORM public.telesrv_bump_read_model_version(
        'story_hidden_list', p_viewer_user_id, 'user', p_viewer_user_id
    );
END;
$$;

CREATE OR REPLACE FUNCTION public.telesrv_notify_story_row_read_models()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM public.telesrv_bump_story_peer(OLD.owner_peer_type, OLD.owner_peer_id);
        RETURN OLD;
    END IF;
    IF TG_OP = 'UPDATE'
       AND (OLD.owner_peer_type, OLD.owner_peer_id)
           IS DISTINCT FROM (NEW.owner_peer_type, NEW.owner_peer_id) THEN
        PERFORM public.telesrv_bump_story_peer(OLD.owner_peer_type, OLD.owner_peer_id);
    END IF;
    PERFORM public.telesrv_bump_story_peer(NEW.owner_peer_type, NEW.owner_peer_id);
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.telesrv_notify_story_hidden_row_read_models()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM public.telesrv_bump_story_peer(OLD.owner_peer_type, OLD.owner_peer_id);
        PERFORM public.telesrv_bump_story_hidden_list(OLD.viewer_user_id);
        RETURN OLD;
    END IF;
    IF TG_OP = 'UPDATE' THEN
        IF (OLD.owner_peer_type, OLD.owner_peer_id)
           IS DISTINCT FROM (NEW.owner_peer_type, NEW.owner_peer_id) THEN
            PERFORM public.telesrv_bump_story_peer(OLD.owner_peer_type, OLD.owner_peer_id);
        END IF;
        IF OLD.viewer_user_id IS DISTINCT FROM NEW.viewer_user_id THEN
            PERFORM public.telesrv_bump_story_hidden_list(OLD.viewer_user_id);
        END IF;
    END IF;
    PERFORM public.telesrv_bump_story_peer(NEW.owner_peer_type, NEW.owner_peer_id);
    PERFORM public.telesrv_bump_story_hidden_list(NEW.viewer_user_id);
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS telesrv_stories_story_peer_read_model ON public.stories;
CREATE TRIGGER telesrv_stories_story_peer_read_model
AFTER INSERT OR UPDATE OR DELETE ON public.stories
FOR EACH ROW EXECUTE FUNCTION public.telesrv_notify_story_row_read_models();

DROP TRIGGER IF EXISTS telesrv_story_hidden_peers_story_peer_read_model ON public.story_hidden_peers;
CREATE TRIGGER telesrv_story_hidden_peers_story_peer_read_model
AFTER INSERT OR UPDATE OR DELETE ON public.story_hidden_peers
FOR EACH ROW EXECUTE FUNCTION public.telesrv_notify_story_hidden_row_read_models();

CREATE OR REPLACE FUNCTION public.telesrv_maintain_user_story_projection_versions()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM public.telesrv_bump_story_peer('user', OLD.id);
        PERFORM public.telesrv_bump_story_hidden_list(OLD.id);
        DELETE FROM public.read_model_versions
        WHERE (model = 'story_peer' AND owner_user_id = 0
               AND peer_type = 'user' AND peer_id = OLD.id)
           OR (model = 'story_hidden_list' AND owner_user_id = OLD.id
               AND peer_type = 'user' AND peer_id = OLD.id);
        RETURN OLD;
    END IF;
    PERFORM public.telesrv_bump_story_peer('user', NEW.id);
    PERFORM public.telesrv_bump_story_hidden_list(NEW.id);
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS users_story_projection_versions_changed ON public.users;
CREATE TRIGGER users_story_projection_versions_changed
AFTER INSERT OR DELETE ON public.users
FOR EACH ROW EXECUTE FUNCTION public.telesrv_maintain_user_story_projection_versions();

CREATE OR REPLACE FUNCTION public.telesrv_maintain_channel_story_projection_version()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM public.telesrv_bump_story_peer('channel', OLD.id);
        DELETE FROM public.read_model_versions
        WHERE model = 'story_peer' AND owner_user_id = 0
          AND peer_type = 'channel' AND peer_id = OLD.id;
        RETURN OLD;
    END IF;
    PERFORM public.telesrv_bump_story_peer('channel', NEW.id);
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS channels_story_projection_version_changed ON public.channels;
CREATE TRIGGER channels_story_projection_version_changed
AFTER INSERT OR DELETE ON public.channels
FOR EACH ROW EXECUTE FUNCTION public.telesrv_maintain_channel_story_projection_version();

INSERT INTO public.read_model_versions (
    model, owner_user_id, peer_type, peer_id, version, hash, updated_at
)
SELECT 'story_peer', 0, 'user', u.id, 1,
       public.telesrv_random_read_model_hash(), now()
FROM public.users u
ON CONFLICT (model, owner_user_id, peer_type, peer_id) DO NOTHING;

INSERT INTO public.read_model_versions (
    model, owner_user_id, peer_type, peer_id, version, hash, updated_at
)
SELECT 'story_peer', 0, 'channel', c.id, 1,
       public.telesrv_random_read_model_hash(), now()
FROM public.channels c
ON CONFLICT (model, owner_user_id, peer_type, peer_id) DO NOTHING;

INSERT INTO public.read_model_versions (
    model, owner_user_id, peer_type, peer_id, version, hash, updated_at
)
SELECT 'story_hidden_list', u.id, 'user', u.id, 1,
       public.telesrv_random_read_model_hash(), now()
FROM public.users u
ON CONFLICT (model, owner_user_id, peer_type, peer_id) DO NOTHING;
