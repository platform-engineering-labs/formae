-- © 2025 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- Persist the verified subject (and its display-name hint) that triggered a
-- FormaCommand, alongside the existing self-asserted ClientID device
-- attribution. Both columns are nullable: rows written before this migration,
-- and any command created without an authenticated caller (classic mode,
-- internal origins), have no subject to record.
ALTER TABLE forma_commands ADD COLUMN IF NOT EXISTS subject TEXT;
ALTER TABLE forma_commands ADD COLUMN IF NOT EXISTS subject_name TEXT;

-- +goose Down
ALTER TABLE forma_commands DROP COLUMN IF EXISTS subject_name;
ALTER TABLE forma_commands DROP COLUMN IF EXISTS subject;
