package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"telesrv/internal/domain"
)

func channelMembersForUpdateBatchTx(ctx context.Context, tx pgx.Tx, channelID int64, userIDs []int64) (map[int64]domain.ChannelMember, error) {
	rows, err := tx.Query(ctx, `
SELECT channel_id, user_id, inviter_user_id, role, status, joined_at, left_at,
       admin_rights::text, banned_rights::text, rank, available_min_id, available_min_pts,
       history_clear_anchor_id, history_clear_anchor_date,
       read_inbox_max_id, read_outbox_max_id, unread_mark, slowmode_last_send_date
FROM channel_members
WHERE channel_id = $1 AND user_id = ANY($2::bigint[])
ORDER BY user_id
FOR UPDATE`, channelID, userIDs)
	if err != nil {
		return nil, fmt.Errorf("lock channel invite members: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]domain.ChannelMember, len(userIDs))
	for rows.Next() {
		member, err := scanChannelMember(rows)
		if err != nil {
			return nil, err
		}
		out[member.UserID] = member
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("lock channel invite members: %w", err)
	}
	return out, nil
}

func enableChannelMembershipBatchTx(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('telesrv.membership_batch_mode', 'on', true)`); err != nil {
		return fmt.Errorf("enable channel membership batch invalidation: %w", err)
	}
	return nil
}

func upsertChannelMembersBatchTx(ctx context.Context, tx pgx.Tx, channel domain.Channel, members []domain.ChannelMember) error {
	if len(members) == 0 {
		return nil
	}
	userIDs := make([]int64, len(members))
	inviterIDs := make([]int64, len(members))
	joinedAt := make([]int32, len(members))
	availableMinIDs := make([]int32, len(members))
	availableMinPts := make([]int32, len(members))
	readInboxMaxIDs := make([]int32, len(members))
	for i, member := range members {
		userIDs[i] = member.UserID
		inviterIDs[i] = member.InviterUserID
		joinedAt[i] = int32(member.JoinedAt)
		availableMinIDs[i] = int32(member.AvailableMinID)
		availableMinPts[i] = int32(member.AvailableMinPts)
		readInboxMaxIDs[i] = int32(member.ReadInboxMaxID)
	}
	if _, err := tx.Exec(ctx, `
WITH input AS MATERIALIZED (
    SELECT user_id, inviter_user_id, joined_at, available_min_id, available_min_pts, read_inbox_max_id
    FROM unnest(
        $2::bigint[], $3::bigint[], $4::integer[], $5::integer[], $6::integer[], $7::integer[]
    ) AS value(user_id, inviter_user_id, joined_at, available_min_id, available_min_pts, read_inbox_max_id)
)
INSERT INTO channel_members (
    channel_id, user_id, inviter_user_id, role, status, joined_at, left_at,
    admin_rights, banned_rights, rank, available_min_id, available_min_pts,
    read_inbox_max_id, read_outbox_max_id, unread_mark, slowmode_last_send_date
)
SELECT $1, user_id, inviter_user_id, 'member', 'active', joined_at, 0,
       '{}'::jsonb, '{}'::jsonb, '', available_min_id, available_min_pts,
       read_inbox_max_id, 0, false, 0
FROM input
ORDER BY user_id
ON CONFLICT (channel_id, user_id) DO UPDATE SET
    inviter_user_id = EXCLUDED.inviter_user_id,
    role = EXCLUDED.role,
    status = EXCLUDED.status,
    joined_at = EXCLUDED.joined_at,
    left_at = EXCLUDED.left_at,
    admin_rights = EXCLUDED.admin_rights,
    banned_rights = EXCLUDED.banned_rights,
    rank = EXCLUDED.rank,
    available_min_id = GREATEST(channel_members.available_min_id, EXCLUDED.available_min_id),
    available_min_pts = GREATEST(channel_members.available_min_pts, EXCLUDED.available_min_pts),
    read_inbox_max_id = GREATEST(channel_members.read_inbox_max_id, EXCLUDED.read_inbox_max_id),
    updated_at = now()`, channel.ID, userIDs, inviterIDs, joinedAt, availableMinIDs, availableMinPts, readInboxMaxIDs); err != nil {
		return fmt.Errorf("batch upsert channel members: %w", err)
	}

	if _, err := tx.Exec(ctx, `
WITH input AS MATERIALIZED (
    SELECT user_id
    FROM unnest($2::bigint[]) AS value(user_id)
)
INSERT INTO user_channel_member_index (
    user_id, channel_id, status, megagroup, broadcast, deleted,
    role, left_at, forum, public_username, can_pin_messages
)
SELECT user_id, $1, 'active', $3, $4, $5, 'member', 0, $6, $7, false
FROM input
ORDER BY user_id
ON CONFLICT (user_id, channel_id) DO UPDATE SET
    status = EXCLUDED.status,
    megagroup = EXCLUDED.megagroup,
    broadcast = EXCLUDED.broadcast,
    deleted = EXCLUDED.deleted,
    role = EXCLUDED.role,
    left_at = EXCLUDED.left_at,
    forum = EXCLUDED.forum,
    public_username = EXCLUDED.public_username,
    can_pin_messages = EXCLUDED.can_pin_messages,
    updated_at = now()`, channel.ID, userIDs, channel.Megagroup, channel.Broadcast, channel.Deleted, channel.Forum, channel.Username != ""); err != nil {
		return fmt.Errorf("batch upsert user channel member index: %w", err)
	}
	return nil
}

func insertChannelInviteAdminLogsBatchTx(ctx context.Context, tx pgx.Tx, channelID, inviterUserID int64, date int, members []domain.ChannelMember) error {
	if len(members) == 0 {
		return nil
	}
	type row struct {
		Ordinal     int                  `json:"ordinal"`
		Participant domain.ChannelMember `json:"participant"`
	}
	input := make([]row, len(members))
	for i, member := range members {
		input[i] = row{Ordinal: i + 1, Participant: member}
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("marshal channel invite admin logs: %w", err)
	}
	if _, err := tx.Exec(ctx, `
WITH input AS MATERIALIZED (
    SELECT ordinal, participant
    FROM jsonb_to_recordset($5::jsonb) AS value(ordinal integer, participant jsonb)
), allocated AS MATERIALIZED (
    UPDATE channels
    SET admin_log_seq = admin_log_seq + $4, updated_at = now()
    WHERE id = $1
    RETURNING admin_log_seq
)
INSERT INTO channel_admin_log_events (
    channel_id, id, actor_user_id, event_date, event_type, participant, query
)
SELECT $1, allocated.admin_log_seq - $4 + input.ordinal, $2, $3,
       'participant_invite', input.participant, ''
FROM input
CROSS JOIN allocated
ORDER BY input.ordinal`, channelID, inviterUserID, date, len(members), string(payload)); err != nil {
		return fmt.Errorf("batch insert channel invite admin logs: %w", err)
	}
	return nil
}

func upsertChannelDialogsBatchTx(ctx context.Context, tx pgx.Tx, channel domain.Channel, top domain.ChannelMessage, members []domain.ChannelMember) error {
	if len(members) == 0 {
		return nil
	}
	topDate := top.Date
	if topDate == 0 {
		topDate = channel.Date
	}
	userIDs := make([]int64, len(members))
	readInboxMaxIDs := make([]int32, len(members))
	readOutboxMaxIDs := make([]int32, len(members))
	for i, member := range members {
		userIDs[i] = member.UserID
		readInboxMaxIDs[i] = int32(member.ReadInboxMaxID)
		readOutboxMaxIDs[i] = int32(member.ReadOutboxMaxID)
	}
	if _, err := tx.Exec(ctx, `
WITH input AS MATERIALIZED (
    SELECT user_id, read_inbox_max_id, read_outbox_max_id
    FROM unnest($4::bigint[], $5::integer[], $6::integer[])
         AS value(user_id, read_inbox_max_id, read_outbox_max_id)
)
INSERT INTO channel_dialogs (
    user_id, channel_id, top_message_id, top_message_date,
    read_inbox_max_id, read_outbox_max_id, unread_count, unread_mark
)
SELECT user_id, $1, $2, $3, read_inbox_max_id, read_outbox_max_id, 0, false
FROM input
ORDER BY user_id
ON CONFLICT (user_id, channel_id) DO UPDATE SET
    top_message_id = GREATEST(channel_dialogs.top_message_id, EXCLUDED.top_message_id),
    top_message_date = GREATEST(channel_dialogs.top_message_date, EXCLUDED.top_message_date),
    read_inbox_max_id = GREATEST(channel_dialogs.read_inbox_max_id, EXCLUDED.read_inbox_max_id),
    read_outbox_max_id = GREATEST(channel_dialogs.read_outbox_max_id, EXCLUDED.read_outbox_max_id),
    unread_mark = false,
    updated_at = now()`, channel.ID, channel.TopMessageID, topDate, userIDs, readInboxMaxIDs, readOutboxMaxIDs); err != nil {
		return fmt.Errorf("batch upsert channel dialogs: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE channel_dialogs AS dialog
SET unread_count = (
        SELECT COUNT(*)::int
        FROM (
            SELECT 1
            FROM channel_messages AS message
            WHERE message.channel_id = dialog.channel_id
              AND message.id > dialog.read_inbox_max_id
              AND message.id <= dialog.top_message_id
              AND message.sender_user_id <> dialog.user_id
              AND NOT message.deleted
            LIMIT $3
        ) AS capped
    ),
    updated_at = now()
WHERE dialog.channel_id = $1
  AND dialog.user_id = ANY($2::bigint[])`, channel.ID, userIDs, domain.MaxDialogUnreadCount); err != nil {
		return fmt.Errorf("batch refresh channel dialog unread count: %w", err)
	}
	return nil
}

func refreshChannelUnreadReactionsCountsBatchTx(ctx context.Context, tx pgx.Tx, channelID int64, userIDs []int64) error {
	if len(userIDs) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
WITH input AS MATERIALIZED (
    SELECT user_id FROM unnest($2::bigint[]) AS value(user_id)
), counts AS MATERIALIZED (
    SELECT input.user_id,
           (
               SELECT COUNT(DISTINCT reaction.message_id)::int
               FROM channel_message_reactions AS reaction
               JOIN channel_messages AS message
                 ON message.channel_id = reaction.channel_id AND message.id = reaction.message_id
               JOIN channel_members AS member
                 ON member.channel_id = reaction.channel_id AND member.user_id = input.user_id
               WHERE reaction.sender_user_id = input.user_id
                 AND reaction.channel_id = $1
                 AND reaction.unread
                 AND reaction.reacted_user_id <> input.user_id
                 AND message.id > member.available_min_id
                 AND NOT message.deleted
                 AND member.status = 'active'
                 AND NOT COALESCE((member.banned_rights->>'ViewMessages')::boolean, false)
           ) AS count
    FROM input
)
INSERT INTO channel_dialogs (user_id, channel_id, unread_reactions_count)
SELECT user_id, $1, count
FROM counts
ORDER BY user_id
ON CONFLICT (user_id, channel_id) DO UPDATE SET
    unread_reactions_count = EXCLUDED.unread_reactions_count,
    updated_at = now()`, channelID, userIDs); err != nil {
		return fmt.Errorf("batch refresh channel unread reactions count: %w", err)
	}
	return nil
}

func bumpChannelMembershipReadModelsBatchTx(ctx context.Context, tx pgx.Tx, channelID int64, userIDs []int64) error {
	if len(userIDs) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `SELECT public.telesrv_bump_channel_membership_read_models($1, $2::bigint[])`, channelID, userIDs); err != nil {
		return fmt.Errorf("batch bump channel membership read models: %w", err)
	}
	return nil
}
