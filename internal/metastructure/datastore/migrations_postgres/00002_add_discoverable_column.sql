-- +goose Up
ALTER TABLE targets ADD COLUMN IF NOT EXISTS discoverable BOOLEAN DEFAULT FALSE;
CREATE INDEX IF NOT EXISTS idx_targets_discoverable ON targets (discoverable);

-- +goose Down
DROP INDEX IF EXISTS idx_targets_discoverable;
ALTER TABLE targets DROP COLUMN IF EXISTS discoverable;
