CREATE TABLE broadcasts (
    id bigserial PRIMARY KEY,
    message text NOT NULL CHECK (message <> '' AND octet_length(message) <= 4096),
    target_mode varchar(16) NOT NULL CHECK (target_mode IN ('all', 'selected')),
    snapshot_max_user_id bigint NOT NULL DEFAULT 0,
    enumeration_cursor_user_id bigint NOT NULL DEFAULT 0,
    enumeration_done boolean NOT NULL DEFAULT false,
    target_count bigint NOT NULL DEFAULT 0 CHECK (target_count >= 0),
    materialized_count bigint NOT NULL DEFAULT 0 CHECK (materialized_count >= 0),
    sent_count bigint NOT NULL DEFAULT 0 CHECK (sent_count >= 0),
    failed_count bigint NOT NULL DEFAULT 0 CHECK (failed_count >= 0),
    created_by varchar(128) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (enumeration_cursor_user_id >= 0 AND enumeration_cursor_user_id <= snapshot_max_user_id),
    CHECK (sent_count + failed_count <= materialized_count)
);

CREATE TABLE broadcast_recipients (
    id bigserial PRIMARY KEY,
    broadcast_id bigint NOT NULL REFERENCES broadcasts(id) ON DELETE CASCADE,
    user_id bigint NOT NULL,
    status varchar(16) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processing', 'sent', 'failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_token varchar(64) NOT NULL DEFAULT '',
    lease_until timestamptz,
    last_error varchar(500) NOT NULL DEFAULT '',
    private_message_id bigint NOT NULL DEFAULT 0,
    message_box_id integer NOT NULL DEFAULT 0,
    pts integer NOT NULL DEFAULT 0,
    sent_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (broadcast_id, user_id),
    CHECK (
        (status = 'sent' AND private_message_id > 0 AND message_box_id > 0 AND pts > 0 AND sent_at IS NOT NULL)
        OR
        (status <> 'sent' AND private_message_id = 0 AND message_box_id = 0 AND pts = 0 AND sent_at IS NULL)
    ),
    CHECK (
        (status = 'processing' AND lease_token <> '' AND lease_until IS NOT NULL)
        OR
        (status <> 'processing' AND lease_token = '' AND lease_until IS NULL)
    )
);

CREATE INDEX broadcasts_enumeration_idx ON broadcasts (id)
    WHERE target_mode = 'all' AND NOT enumeration_done;
CREATE INDEX broadcast_recipients_pending_idx ON broadcast_recipients (next_attempt_at, id)
    WHERE status = 'pending';
CREATE INDEX broadcast_recipients_processing_idx ON broadcast_recipients (lease_until, id)
    WHERE status = 'processing';
CREATE INDEX broadcast_recipients_broadcast_idx ON broadcast_recipients (broadcast_id, id);
