-- +goose Up
-- Drop the data column now that all data has been migrated to normalized columns
-- PostgreSQL supports DROP COLUMN directly
ALTER TABLE forma_commands DROP COLUMN IF EXISTS data;

-- Add index for the normalized config_mode column (used in queries)
CREATE INDEX IF NOT EXISTS idx_config_mode ON forma_commands (config_mode);

-- +goose Down
-- Add back the data column
-- Note: This is a destructive operation - we cannot fully restore the original JSON data
-- The down migration creates the column but leaves it NULL
ALTER TABLE forma_commands ADD COLUMN IF NOT EXISTS data TEXT;
