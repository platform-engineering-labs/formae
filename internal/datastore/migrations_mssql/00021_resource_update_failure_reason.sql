-- © 2026 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- Persist the failure reason recorded for a resource update that fails
-- before any plugin operation runs (e.g. a terminal resolve failure), so the
-- command's error message survives a reload instead of coming back blank.
-- Nullable: rows written before this migration, and successful updates, have
-- no reason to record.
ALTER TABLE resource_updates ADD failure_reason nvarchar(max) NULL;

-- +goose Down
ALTER TABLE resource_updates DROP COLUMN failure_reason;
