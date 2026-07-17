-- © 2025 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- Persist the command-level Source field so the agent can distinguish user-
-- initiated commands from internal bookkeeping (auto-reconciler, stack-expirer,
-- synchronizer).  Rows written before this migration get an empty string;
-- the application treats '' the same as 'user' for display purposes.
IF NOT EXISTS (
    SELECT 1 FROM sys.columns
    WHERE object_id = OBJECT_ID('forma_commands') AND name = 'source'
)
BEGIN
    ALTER TABLE forma_commands ADD source nvarchar(450) NOT NULL DEFAULT '';
END;

-- +goose Down
IF EXISTS (
    SELECT 1 FROM sys.columns
    WHERE object_id = OBJECT_ID('forma_commands') AND name = 'source'
)
BEGIN
    ALTER TABLE forma_commands DROP COLUMN source;
END;
