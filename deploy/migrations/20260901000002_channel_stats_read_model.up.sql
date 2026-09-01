-- Bounded stats reads: event-time viewers, membership snapshots, and exact
-- public-forward seek pagination by the durable MessageForward JSON shape.
CREATE INDEX channel_message_viewers_stats_date_idx
    ON public.channel_message_viewers (channel_id, viewed_at, viewer_user_id, message_id);

CREATE INDEX channel_members_stats_period_idx
    ON public.channel_members (channel_id, joined_at, left_at, user_id);

CREATE INDEX channel_messages_public_forward_source_seek_idx
    ON public.channel_messages (
        (fwd_from #>> '{From,Type}'),
        (fwd_from #>> '{From,ID}'),
        (fwd_from #>> '{ChannelPost}'),
        message_date DESC,
        channel_id ASC,
        id DESC
    )
    WHERE NOT deleted AND fwd_from <> '{}'::jsonb;
