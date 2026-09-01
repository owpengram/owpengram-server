-- Extend the owner-scoped channel_active_memberships generation to every
-- durable predicate used by ListActiveChannelIDsForUser. Ordinary memberships
-- are covered by user_channel_member_index; these triggers cover monoforum
-- subscriber message visibility, manager rights, and parent/mono enablement.

CREATE OR REPLACE FUNCTION public.telesrv_bump_channel_active_membership_owner(p_user_id bigint)
RETURNS void
LANGUAGE plpgsql
AS $$
BEGIN
    IF COALESCE(p_user_id, 0) <= 0 THEN
        RETURN;
    END IF;
    PERFORM public.telesrv_bump_read_model_version(
        'channel_active_memberships', p_user_id, 'user', p_user_id
    );
END;
$$;

CREATE OR REPLACE FUNCTION public.telesrv_notify_monoforum_message_visibility_read_model()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    old_owner bigint := 0;
    new_owner bigint := 0;
BEGIN
    IF TG_OP <> 'INSERT'
       AND OLD.saved_peer_type = 'user'
       AND OLD.saved_peer_id > 0
       AND NOT OLD.deleted
    THEN
        old_owner := OLD.saved_peer_id;
    END IF;
    IF TG_OP <> 'DELETE'
       AND NEW.saved_peer_type = 'user'
       AND NEW.saved_peer_id > 0
       AND NOT NEW.deleted
    THEN
        new_owner := NEW.saved_peer_id;
    END IF;

    IF old_owner > 0 THEN
        PERFORM public.telesrv_bump_channel_active_membership_owner(old_owner);
    END IF;
    IF new_owner > 0 AND new_owner IS DISTINCT FROM old_owner THEN
        PERFORM public.telesrv_bump_channel_active_membership_owner(new_owner);
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER channel_messages_monoforum_active_membership_changed
AFTER INSERT OR DELETE OR UPDATE OF channel_id, saved_peer_type, saved_peer_id, deleted
ON public.channel_messages
FOR EACH ROW EXECUTE FUNCTION public.telesrv_notify_monoforum_message_visibility_read_model();

CREATE OR REPLACE FUNCTION public.telesrv_notify_monoforum_manager_visibility_read_model()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.role IS NOT DISTINCT FROM NEW.role
       AND OLD.admin_rights IS NOT DISTINCT FROM NEW.admin_rights
    THEN
        RETURN NULL;
    END IF;
    IF EXISTS (
        SELECT 1
        FROM public.channels AS parent
        WHERE parent.id = NEW.channel_id
          AND parent.linked_monoforum_id <> 0
          AND parent.broadcast_messages_allowed
          AND NOT parent.deleted
    ) THEN
        PERFORM public.telesrv_bump_channel_active_membership_owner(NEW.user_id);
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER channel_members_monoforum_manager_visibility_changed
AFTER UPDATE OF role, admin_rights ON public.channel_members
FOR EACH ROW EXECUTE FUNCTION public.telesrv_notify_monoforum_manager_visibility_read_model();

CREATE OR REPLACE FUNCTION public.telesrv_bump_monoforum_visibility_owners(
    p_parent_id bigint,
    p_monoforum_id bigint
)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    affected record;
BEGIN
    IF COALESCE(p_parent_id, 0) <= 0 OR COALESCE(p_monoforum_id, 0) <= 0 THEN
        RETURN;
    END IF;
    FOR affected IN
        SELECT DISTINCT owner_id
        FROM (
            SELECT member.user_id AS owner_id
            FROM public.user_channel_member_index AS member
            WHERE member.channel_id = p_parent_id
              AND member.status = 'active'
              AND NOT member.deleted
              AND member.role IN ('creator', 'admin')
            UNION
            SELECT message.saved_peer_id AS owner_id
            FROM public.channel_messages AS message
            WHERE message.channel_id = p_monoforum_id
              AND message.saved_peer_type = 'user'
              AND message.saved_peer_id > 0
              AND NOT message.deleted
        ) AS owners
        WHERE owner_id > 0
        ORDER BY owner_id
    LOOP
        PERFORM public.telesrv_bump_channel_active_membership_owner(affected.owner_id);
    END LOOP;
END;
$$;

CREATE OR REPLACE FUNCTION public.telesrv_notify_monoforum_channel_visibility_read_model()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    old_parent_id bigint := 0;
    old_monoforum_id bigint := 0;
    new_parent_id bigint := 0;
    new_monoforum_id bigint := 0;
BEGIN
    IF (OLD.broadcast_messages_allowed, OLD.linked_monoforum_id, OLD.deleted, OLD.monoforum)
       IS NOT DISTINCT FROM
       (NEW.broadcast_messages_allowed, NEW.linked_monoforum_id, NEW.deleted, NEW.monoforum)
    THEN
        RETURN NULL;
    END IF;

    IF OLD.linked_monoforum_id > 0 THEN
        IF OLD.monoforum THEN
            old_parent_id := OLD.linked_monoforum_id;
            old_monoforum_id := OLD.id;
        ELSE
            old_parent_id := OLD.id;
            old_monoforum_id := OLD.linked_monoforum_id;
        END IF;
    END IF;
    IF NEW.linked_monoforum_id > 0 THEN
        IF NEW.monoforum THEN
            new_parent_id := NEW.linked_monoforum_id;
            new_monoforum_id := NEW.id;
        ELSE
            new_parent_id := NEW.id;
            new_monoforum_id := NEW.linked_monoforum_id;
        END IF;
    END IF;

    IF old_parent_id > 0 THEN
        PERFORM public.telesrv_bump_monoforum_visibility_owners(old_parent_id, old_monoforum_id);
    END IF;
    IF new_parent_id > 0
       AND (new_parent_id, new_monoforum_id)
           IS DISTINCT FROM (old_parent_id, old_monoforum_id)
    THEN
        PERFORM public.telesrv_bump_monoforum_visibility_owners(new_parent_id, new_monoforum_id);
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER channels_monoforum_active_membership_changed
AFTER UPDATE OF broadcast_messages_allowed, linked_monoforum_id, deleted, monoforum
ON public.channels
FOR EACH ROW EXECUTE FUNCTION public.telesrv_notify_monoforum_channel_visibility_read_model();

-- Existing monoforum owners need a fresh generation before any shared page can
-- be populated under the expanded dependency contract.
DO $$
DECLARE
    linked record;
BEGIN
    FOR linked IN
        SELECT parent.id AS parent_id, parent.linked_monoforum_id AS monoforum_id
        FROM public.channels AS parent
        WHERE parent.linked_monoforum_id > 0
    LOOP
        PERFORM public.telesrv_bump_monoforum_visibility_owners(
            linked.parent_id, linked.monoforum_id
        );
    END LOOP;
END;
$$;
