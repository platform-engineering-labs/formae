-- © 2025 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- Add a stable incarnation id and health-observation columns to the targets
-- table so that health state can be persisted per target row.

ALTER TABLE targets ADD COLUMN target_incarnation_id TEXT NOT NULL DEFAULT '';
ALTER TABLE targets ADD COLUMN health_state TEXT NOT NULL DEFAULT 'unknown';
ALTER TABLE targets ADD COLUMN last_seen_at TEXT;
ALTER TABLE targets ADD COLUMN observed_at TEXT;
ALTER TABLE targets ADD COLUMN first_unreachable_at TEXT;
ALTER TABLE targets ADD COLUMN last_sample_at TEXT;
ALTER TABLE targets ADD COLUMN unreachable_accum_seconds INTEGER NOT NULL DEFAULT 0;
ALTER TABLE targets ADD COLUMN last_error_code TEXT;

-- Backfill each existing target row with a unique incarnation id.
-- Each label group (all versions of one target) shares the same id;
-- only the highest-version row per label needs one — readers always
-- load the latest version, so the earlier rows are never returned.
-- Using a temp table avoids a correlated subquery and stays within
-- SQLite's ALTER TABLE / UPDATE idioms (matching migration 00009).
CREATE TEMP TABLE _target_incarnation_backfill AS
SELECT DISTINCT label,
    '0' || substr(lower(hex(randomblob(13))), 1, 26) AS new_id
FROM targets;

UPDATE targets
SET target_incarnation_id = (
    SELECT new_id FROM _target_incarnation_backfill
    WHERE _target_incarnation_backfill.label = targets.label
);

DROP TABLE _target_incarnation_backfill;

-- +goose Down
-- SQLite doesn't support DROP COLUMN before 3.35.0; leaving the columns
-- in place on rollback is safe (the application ignores extra columns).
SELECT 1;
