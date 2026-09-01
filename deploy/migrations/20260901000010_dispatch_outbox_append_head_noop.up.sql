-- Producers hold a shared per-lane fence until commit. A consumer that may
-- remove the last head takes the exclusive form in a preceding statement;
-- after that fence is acquired, its DELETE sees every earlier append or every
-- later append observes the missing marker and installs a new one.
CREATE FUNCTION dispatch_outbox_lane_advisory_key(target_user_id bigint)
RETURNS bigint
LANGUAGE sql
IMMUTABLE
STRICT
PARALLEL SAFE
AS $$
    SELECT hashtextextended('telesrv:dispatch-outbox-lane:' || target_user_id::text, 0)
$$;

DROP TRIGGER dispatch_outbox_insert_user_head ON dispatch_outbox;

CREATE FUNCTION dispatch_outbox_insert_user_heads()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM pg_advisory_xact_lock_shared(
        dispatch_outbox_lane_advisory_key(streams.target_user_id)
    )
    FROM (
        SELECT DISTINCT target_user_id
        FROM new_rows
        ORDER BY target_user_id
    ) AS streams;

    -- The anti-join avoids touching a committed mutable head. ON CONFLICT is
    -- retained only for concurrent producers racing to create an empty lane.
    WITH candidates AS MATERIALIZED (
        SELECT DISTINCT ON (target_user_id)
            target_user_id, id AS head_id, pts AS head_pts,
            status, next_attempt_at, updated_at
        FROM new_rows
        ORDER BY target_user_id, pts, id
    )
    INSERT INTO dispatch_outbox_user_heads (
        target_user_id, head_id, head_pts, status, next_attempt_at, updated_at
    )
    SELECT
        c.target_user_id, c.head_id, c.head_pts,
        c.status, c.next_attempt_at, c.updated_at
    FROM candidates c
    WHERE NOT EXISTS (
        SELECT 1
        FROM dispatch_outbox_user_heads existing
        WHERE existing.target_user_id = c.target_user_id
    )
    ON CONFLICT (target_user_id) DO NOTHING;
    RETURN NULL;
END;
$$;

CREATE OR REPLACE FUNCTION dispatch_outbox_maintain_user_head()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    removed_head bigint;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        UPDATE dispatch_outbox_user_heads
        SET status = NEW.status,
            next_attempt_at = NEW.next_attempt_at,
            updated_at = NEW.updated_at
        WHERE target_user_id = NEW.target_user_id
          AND head_id = NEW.id;
        RETURN NULL;
    END IF;

    DELETE FROM dispatch_outbox_user_heads
    WHERE target_user_id = OLD.target_user_id
      AND head_id = OLD.id
    RETURNING head_id INTO removed_head;

    IF removed_head IS NOT NULL THEN
        INSERT INTO dispatch_outbox_user_heads (
            target_user_id, head_id, head_pts, status, next_attempt_at, updated_at
        )
        SELECT target_user_id, id, pts, status, next_attempt_at, updated_at
        FROM dispatch_outbox
        WHERE target_user_id = OLD.target_user_id
        ORDER BY pts ASC, id ASC
        LIMIT 1
        ON CONFLICT (target_user_id) DO UPDATE
        SET head_id = EXCLUDED.head_id,
            head_pts = EXCLUDED.head_pts,
            status = EXCLUDED.status,
            next_attempt_at = EXCLUDED.next_attempt_at,
            updated_at = EXCLUDED.updated_at
        WHERE (EXCLUDED.head_pts, EXCLUDED.head_id) <
              (dispatch_outbox_user_heads.head_pts, dispatch_outbox_user_heads.head_id);
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER dispatch_outbox_insert_user_head
AFTER INSERT ON dispatch_outbox
REFERENCING NEW TABLE AS new_rows
FOR EACH STATEMENT
EXECUTE FUNCTION dispatch_outbox_insert_user_heads();

-- Repair any marker that may be absent when upgrading from an interrupted or
-- older deployment. Startup migration runs before the dispatcher starts.
INSERT INTO dispatch_outbox_user_heads (
    target_user_id, head_id, head_pts, status, next_attempt_at, updated_at
)
SELECT DISTINCT ON (d.target_user_id)
    d.target_user_id, d.id, d.pts, d.status, d.next_attempt_at, d.updated_at
FROM dispatch_outbox d
LEFT JOIN dispatch_outbox_user_heads h USING (target_user_id)
WHERE h.target_user_id IS NULL
ORDER BY d.target_user_id, d.pts, d.id
ON CONFLICT (target_user_id) DO NOTHING;
