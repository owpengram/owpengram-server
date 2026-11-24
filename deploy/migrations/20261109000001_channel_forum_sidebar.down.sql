ALTER TABLE channels
    DROP CONSTRAINT IF EXISTS channels_forum_sidebar_check;
ALTER TABLE channels
    DROP COLUMN IF EXISTS forum_sidebar;
