-- © 2025 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- The planner sets IsCascade / CascadeSource on ResourceUpdates produced
-- by cascade-delete and cascade-update branches; persist them so the CLI
-- can render the "(cascade) because it depends on X" breadcrumb after
-- the command is loaded back from the database (e.g. after an agent
-- restart, or via `formae status <command-id>`).
ALTER TABLE resource_updates ADD COLUMN is_cascade INTEGER NOT NULL DEFAULT 0;
ALTER TABLE resource_updates ADD COLUMN cascade_source TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite doesn't support DROP COLUMN before 3.35.0; leaving the columns
-- in place on rollback is safe (the application ignores extra columns).
SELECT 1;
