-- © 2025 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- Add a stable incarnation id and health-observation columns to the targets
-- table so that health state can be persisted per target row.

ALTER TABLE targets ADD COLUMN IF NOT EXISTS target_incarnation_id TEXT NOT NULL DEFAULT '';
ALTER TABLE targets ADD COLUMN IF NOT EXISTS health_state TEXT NOT NULL DEFAULT 'unknown';
ALTER TABLE targets ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ;
ALTER TABLE targets ADD COLUMN IF NOT EXISTS observed_at TIMESTAMPTZ;
ALTER TABLE targets ADD COLUMN IF NOT EXISTS first_unreachable_at TIMESTAMPTZ;
ALTER TABLE targets ADD COLUMN IF NOT EXISTS last_sample_at TIMESTAMPTZ;
ALTER TABLE targets ADD COLUMN IF NOT EXISTS unreachable_accum_seconds BIGINT NOT NULL DEFAULT 0;
ALTER TABLE targets ADD COLUMN IF NOT EXISTS last_error_code TEXT;

-- Backfill each existing label group with a unique incarnation id.
-- All version rows for a label share one id (matching migration 00009 idiom).
CREATE TEMP TABLE _target_incarnation_backfill AS
SELECT DISTINCT label,
    '0' || substr(replace(gen_random_uuid()::text, '-', ''), 1, 26) AS new_id
FROM targets;

UPDATE targets
SET target_incarnation_id = b.new_id
FROM _target_incarnation_backfill b
WHERE targets.label = b.label;

DROP TABLE _target_incarnation_backfill;

-- +goose Down
ALTER TABLE targets DROP COLUMN IF EXISTS last_error_code;
ALTER TABLE targets DROP COLUMN IF EXISTS unreachable_accum_seconds;
ALTER TABLE targets DROP COLUMN IF EXISTS last_sample_at;
ALTER TABLE targets DROP COLUMN IF EXISTS first_unreachable_at;
ALTER TABLE targets DROP COLUMN IF EXISTS observed_at;
ALTER TABLE targets DROP COLUMN IF EXISTS last_seen_at;
ALTER TABLE targets DROP COLUMN IF EXISTS health_state;
ALTER TABLE targets DROP COLUMN IF EXISTS target_incarnation_id;
