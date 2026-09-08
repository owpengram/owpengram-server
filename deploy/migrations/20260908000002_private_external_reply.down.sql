DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM private_messages WHERE reply_external <> '{}'::jsonb)
     OR EXISTS (SELECT 1 FROM message_boxes WHERE reply_external <> '{}'::jsonb) THEN
    RAISE EXCEPTION 'cannot discard durable external reply snapshots';
  END IF;
END $$;
ALTER TABLE message_boxes DROP COLUMN reply_external;
ALTER TABLE private_messages DROP COLUMN reply_external;
