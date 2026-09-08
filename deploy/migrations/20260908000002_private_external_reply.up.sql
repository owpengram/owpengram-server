ALTER TABLE private_messages ADD COLUMN reply_external jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE message_boxes ADD COLUMN reply_external jsonb NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE private_messages ADD CONSTRAINT private_messages_reply_external_object
  CHECK (jsonb_typeof(reply_external) = 'object' AND octet_length(reply_external::text) <= 1048576);
ALTER TABLE message_boxes ADD CONSTRAINT message_boxes_reply_external_object
  CHECK (jsonb_typeof(reply_external) = 'object' AND octet_length(reply_external::text) <= 1048576);
