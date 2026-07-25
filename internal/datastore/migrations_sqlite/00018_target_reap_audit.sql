-- © 2025 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- Records one immutable row per completed target reap. incarnation_id is UNIQUE:
-- a second reap of the same target incarnation is a no-op, caught either by the
-- conditional-transition CAS (rowcount 0 once the row is already 'reaped') or,
-- as a backstop, by this unique constraint. reaped_at is the reap instant,
-- accum_seconds the accrued unreachable time at reap, resource_count the number
-- of resources tombstoned in the reap transaction.
CREATE TABLE IF NOT EXISTS target_reap_audit (
    incarnation_id TEXT NOT NULL UNIQUE,
    label          TEXT NOT NULL,
    reaped_at      TEXT NOT NULL,
    accum_seconds  INTEGER NOT NULL,
    resource_count INTEGER NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS target_reap_audit;
