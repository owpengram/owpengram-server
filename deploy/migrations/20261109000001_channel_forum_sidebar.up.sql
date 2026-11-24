-- Layer 233: forums can show their topics in a sidebar (channel.forum_sidebar,
-- set by channels.toggleForum sidebar:Bool). Like forum_tabs, it only means
-- something while the channel is a forum.
ALTER TABLE channels
    ADD COLUMN IF NOT EXISTS forum_sidebar boolean DEFAULT false NOT NULL;

ALTER TABLE channels
    DROP CONSTRAINT IF EXISTS channels_forum_sidebar_check;
ALTER TABLE channels
    ADD CONSTRAINT channels_forum_sidebar_check CHECK ((NOT forum_sidebar) OR forum);
