ALTER TABLE broadcasts
    DROP CONSTRAINT IF EXISTS broadcasts_entities_array_check,
    DROP COLUMN IF EXISTS entities;
