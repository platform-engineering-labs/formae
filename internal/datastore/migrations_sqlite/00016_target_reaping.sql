-- © 2025 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- Persist each target's resolved reaping behaviour. reap_kind is the variant
-- ('after' or 'never'); reap_max_unreachable_seconds is the reap-after duration.
-- Existing rows are backfilled to the global default (after / 86400s) by the
-- NOT NULL DEFAULT clauses, which SQLite applies to every existing row.

ALTER TABLE targets ADD COLUMN reap_kind TEXT NOT NULL DEFAULT 'after';
ALTER TABLE targets ADD COLUMN reap_max_unreachable_seconds INTEGER NOT NULL DEFAULT 86400;

-- +goose Down
-- SQLite doesn't support DROP COLUMN before 3.35.0; leaving the columns
-- in place on rollback is safe (the application ignores extra columns).
SELECT 1;
