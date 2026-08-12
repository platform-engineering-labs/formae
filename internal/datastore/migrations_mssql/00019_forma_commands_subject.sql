-- © 2025 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- Persist the verified subject (and its display-name hint) that triggered a
-- FormaCommand, alongside the existing self-asserted ClientID device
-- attribution. Both columns are nullable: rows written before this migration,
-- and any command created without an authenticated caller (classic mode,
-- internal origins), have no subject to record.
ALTER TABLE forma_commands ADD subject nvarchar(max) NULL, subject_name nvarchar(max) NULL;

-- +goose Down
ALTER TABLE forma_commands DROP COLUMN subject, subject_name;
