-- Complete the dialog_light/channel_base dependency closure for cached dialog
-- top-message projections. These triggers only bump read-model versions; they
-- do not allocate PTS or mutate message/update facts.

CREATE OR REPLACE FUNCTION telesrv_notify_private_top_message_read_model()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM dialogs d
        WHERE d.user_id = NEW.owner_user_id
          AND d.peer_type = NEW.peer_type
          AND d.peer_id = NEW.peer_id
          AND d.top_message_id = NEW.box_id
    ) THEN
        PERFORM telesrv_bump_dialog_light(NEW.owner_user_id, NEW.peer_type, NEW.peer_id);
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER message_boxes_dialog_top_projection_changed
AFTER UPDATE OF
    message_date, body, entities, deleted, edit_date, silent, noforwards,
    reply_to_msg_id, reply_to_peer_type, reply_to_peer_id, reply_to_top_id,
    reply_to_story_id, quote_text, quote_entities, quote_offset,
    fwd_from_peer_type, fwd_from_peer_id, fwd_from_name, fwd_date,
    fwd_saved_from_peer_type, fwd_saved_from_peer_id, fwd_saved_from_msg_id,
    media, media_unread, reaction_unread, ttl_period, expires_at, pinned,
    saved_peer_type, saved_peer_id, reply_markup, via_bot_id, rich_message,
    grouped_id, effect, hide_edited
ON message_boxes
FOR EACH ROW
EXECUTE FUNCTION telesrv_notify_private_top_message_read_model();

CREATE OR REPLACE FUNCTION telesrv_notify_private_top_reactions_read_model()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    changed_sender_id BIGINT;
    changed_message_id BIGINT;
BEGIN
    IF TG_OP = 'DELETE' THEN
        changed_sender_id := OLD.message_sender_id;
        changed_message_id := OLD.private_message_id;
    ELSE
        changed_sender_id := NEW.message_sender_id;
        changed_message_id := NEW.private_message_id;
    END IF;

    PERFORM telesrv_bump_dialog_light(b.owner_user_id, b.peer_type, b.peer_id)
    FROM message_boxes b
    JOIN dialogs d
      ON d.user_id = b.owner_user_id
     AND d.peer_type = b.peer_type
     AND d.peer_id = b.peer_id
     AND d.top_message_id = b.box_id
    WHERE b.message_sender_id = changed_sender_id
      AND b.private_message_id = changed_message_id
      AND NOT b.deleted;
    RETURN NULL;
END;
$$;

CREATE TRIGGER private_message_reactions_dialog_top_projection_changed
AFTER INSERT OR DELETE OR UPDATE
ON private_message_reactions
FOR EACH ROW
EXECUTE FUNCTION telesrv_notify_private_top_reactions_read_model();

CREATE OR REPLACE FUNCTION telesrv_notify_channel_top_message_read_model()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM channels c
        WHERE c.id = NEW.channel_id
          AND c.top_message_id = NEW.id
    ) THEN
        PERFORM telesrv_bump_read_model_version('channel_base', 0, 'channel', NEW.channel_id);
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER channel_messages_dialog_top_projection_changed
AFTER UPDATE OF
    sender_user_id, from_peer_type, from_peer_id, send_as_peer_type, send_as_peer_id,
    message_date, edit_date, post, silent, noforwards, body, entities, reply_to,
    reply_to_msg_id, reply_to_peer_type, reply_to_peer_id, reply_to_top_id, fwd_from,
    discussion_channel_id, discussion_message_id, action, deleted, views_count, media,
    ttl_period, expires_at, post_author, pinned, via_bot_id, reply_markup,
    from_boosts_applied, rich_message
ON channel_messages
FOR EACH ROW
EXECUTE FUNCTION telesrv_notify_channel_top_message_read_model();

CREATE OR REPLACE FUNCTION telesrv_notify_channel_top_reactions_read_model()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    changed_channel_id BIGINT;
    changed_message_id INTEGER;
BEGIN
    IF TG_OP = 'DELETE' THEN
        changed_channel_id := OLD.channel_id;
        changed_message_id := OLD.message_id;
    ELSE
        changed_channel_id := NEW.channel_id;
        changed_message_id := NEW.message_id;
    END IF;

    IF EXISTS (
        SELECT 1
        FROM channels c
        WHERE c.id = changed_channel_id
          AND c.top_message_id = changed_message_id
    ) THEN
        PERFORM telesrv_bump_read_model_version('channel_base', 0, 'channel', changed_channel_id);
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER channel_message_reactions_dialog_top_projection_changed
AFTER INSERT OR DELETE OR UPDATE
ON channel_message_reactions
FOR EACH ROW
EXECUTE FUNCTION telesrv_notify_channel_top_reactions_read_model();
