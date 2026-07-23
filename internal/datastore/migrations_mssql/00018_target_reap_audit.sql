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
CREATE TABLE target_reap_audit (
    incarnation_id nvarchar(450) NOT NULL UNIQUE,
    label          nvarchar(max) NOT NULL,
    reaped_at      datetime2 NOT NULL,
    accum_seconds  bigint NOT NULL,
    resource_count bigint NOT NULL
);

-- +goose Down
-- Wrapped in StatementBegin/End so goose sends the guarded DROP as a single
-- batch to the SQL Server driver.
-- +goose StatementBegin
IF OBJECT_ID('target_reap_audit', 'U') IS NOT NULL
    DROP TABLE target_reap_audit;
-- +goose StatementEnd
