-- +goose Up
-- Remove the metadata column from resource_updates table
-- The metadata was redundant as it was always derived from Resource.Properties via GetMetadata()
ALTER TABLE resource_updates DROP COLUMN metadata;

-- +goose Down
-- Restore the metadata column (will be NULL for existing records)
ALTER TABLE resource_updates ADD COLUMN metadata TEXT;
