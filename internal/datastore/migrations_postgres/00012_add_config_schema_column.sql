-- © 2025 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
ALTER TABLE targets ADD COLUMN IF NOT EXISTS config_schema JSONB;

-- +goose Down
ALTER TABLE targets DROP COLUMN IF EXISTS config_schema;
