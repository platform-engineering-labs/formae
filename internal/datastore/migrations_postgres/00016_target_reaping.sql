-- © 2025 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- Persist each target's resolved reaping behaviour. reap_kind is the variant
-- ('after' or 'never'); reap_max_unreachable_seconds is the reap-after duration.
-- The NOT NULL DEFAULT clauses backfill existing rows to the global default
-- (after / 86400s).

ALTER TABLE targets ADD COLUMN IF NOT EXISTS reap_kind TEXT NOT NULL DEFAULT 'after';
ALTER TABLE targets ADD COLUMN IF NOT EXISTS reap_max_unreachable_seconds BIGINT NOT NULL DEFAULT 86400;

-- +goose Down
ALTER TABLE targets DROP COLUMN IF EXISTS reap_max_unreachable_seconds;
ALTER TABLE targets DROP COLUMN IF EXISTS reap_kind;
