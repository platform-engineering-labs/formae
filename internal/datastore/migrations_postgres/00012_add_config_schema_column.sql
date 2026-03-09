-- +goose Up
ALTER TABLE targets ADD COLUMN IF NOT EXISTS config_schema TEXT;

-- +goose Down
ALTER TABLE targets DROP COLUMN IF EXISTS config_schema;
