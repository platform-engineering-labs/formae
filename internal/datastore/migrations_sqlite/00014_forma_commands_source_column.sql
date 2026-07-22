-- © 2025 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- Persist the command-level Source field so the agent can distinguish user-
-- initiated commands from internal bookkeeping (auto-reconciler, stack-expirer,
-- synchronizer).  Rows written before this migration get an empty string;
-- the application treats '' the same as 'user' for display purposes.
ALTER TABLE forma_commands ADD COLUMN source TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite doesn't support DROP COLUMN before 3.35.0; leaving the column
-- in place on rollback is safe (the application ignores extra columns).
SELECT 1;
