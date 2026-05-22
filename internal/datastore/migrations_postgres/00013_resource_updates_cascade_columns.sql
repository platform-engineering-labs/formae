-- © 2025 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- The planner sets IsCascade / CascadeSource on ResourceUpdates produced
-- by cascade-delete and cascade-update branches; persist them so the CLI
-- can render the "(cascade) because it depends on X" breadcrumb after
-- the command is loaded back from the database (e.g. after an agent
-- restart, or via `formae status <command-id>`).
ALTER TABLE resource_updates ADD COLUMN IF NOT EXISTS is_cascade BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE resource_updates ADD COLUMN IF NOT EXISTS cascade_source TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE resource_updates DROP COLUMN IF EXISTS is_cascade;
ALTER TABLE resource_updates DROP COLUMN IF EXISTS cascade_source;
