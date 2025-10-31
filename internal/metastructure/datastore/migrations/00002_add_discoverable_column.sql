-- +goose Up
ALTER TABLE targets ADD COLUMN discoverable INTEGER DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_discoverable ON targets (discoverable);

-- +goose Down
DROP INDEX IF EXISTS idx_discoverable;
-- sqLite doesn't support DROP COLUMN, so we skip
