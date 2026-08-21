-- © 2025 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- Persist the verified subject (and its display-name hint) that triggered a
-- FormaCommand, alongside the existing self-asserted ClientID device
-- attribution. Both columns are nullable: rows written before this migration,
-- and any command created without an authenticated caller (classic mode,
-- internal origins), have no subject to record.
ALTER TABLE forma_commands ADD COLUMN subject TEXT;
ALTER TABLE forma_commands ADD COLUMN subject_name TEXT;

-- +goose Down
-- SQLite doesn't support DROP COLUMN before 3.35.0; leaving the columns in
-- place on rollback is safe (the application ignores extra columns).
SELECT 1;
