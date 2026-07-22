-- © 2025 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- Persist the command-level Source field so the agent can distinguish user-
-- initiated commands from internal bookkeeping (auto-reconciler, stack-expirer,
-- synchronizer).  Rows written before this migration get an empty string;
-- the application treats '' the same as 'user' for display purposes.
ALTER TABLE forma_commands ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE forma_commands DROP COLUMN IF EXISTS source;
