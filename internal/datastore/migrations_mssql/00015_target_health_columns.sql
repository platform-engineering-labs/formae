-- © 2025 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- Add a stable incarnation id and health-observation columns to the targets
-- table so that health state can be persisted per target row.

ALTER TABLE targets ADD target_incarnation_id nvarchar(450) NOT NULL DEFAULT '';
ALTER TABLE targets ADD health_state nvarchar(450) NOT NULL DEFAULT 'unknown';
ALTER TABLE targets ADD last_seen_at datetime2;
ALTER TABLE targets ADD observed_at datetime2;
ALTER TABLE targets ADD first_unreachable_at datetime2;
ALTER TABLE targets ADD last_sample_at datetime2;
ALTER TABLE targets ADD unreachable_accum_seconds bigint NOT NULL DEFAULT 0;
ALTER TABLE targets ADD last_error_code nvarchar(max);

-- Backfill each existing label group with a unique incarnation id.
-- MSSQL NEWID() generates a per-row UUID; we use it as a compact unique string.
-- All version rows for a label share the same id.
WITH label_ids AS (
    SELECT DISTINCT label,
        LOWER(REPLACE(CONVERT(nvarchar(36), NEWID()), '-', '')) AS new_id
    FROM targets
)
UPDATE t
SET t.target_incarnation_id = li.new_id
FROM targets t
INNER JOIN label_ids li ON t.label = li.label;

-- +goose Down
-- Columns with inline DEFAULT clauses get system-named default constraints on
-- SQL Server. Those constraints must be dropped before the column can be dropped.
-- The DECLARE/SELECT/EXEC pattern looks up the constraint name dynamically so it
-- works regardless of the system-generated name and is idempotent (no-ops when the
-- constraint or column does not exist).
-- +goose StatementBegin
DECLARE @sql nvarchar(max);

SELECT @sql = 'ALTER TABLE targets DROP CONSTRAINT ' + dc.name
FROM sys.default_constraints dc
JOIN sys.columns c ON dc.parent_object_id = c.object_id AND dc.parent_column_id = c.column_id
WHERE dc.parent_object_id = OBJECT_ID('targets') AND c.name = 'unreachable_accum_seconds';
IF @sql IS NOT NULL EXEC sp_executesql @sql;
SET @sql = NULL;

SELECT @sql = 'ALTER TABLE targets DROP CONSTRAINT ' + dc.name
FROM sys.default_constraints dc
JOIN sys.columns c ON dc.parent_object_id = c.object_id AND dc.parent_column_id = c.column_id
WHERE dc.parent_object_id = OBJECT_ID('targets') AND c.name = 'health_state';
IF @sql IS NOT NULL EXEC sp_executesql @sql;
SET @sql = NULL;

SELECT @sql = 'ALTER TABLE targets DROP CONSTRAINT ' + dc.name
FROM sys.default_constraints dc
JOIN sys.columns c ON dc.parent_object_id = c.object_id AND dc.parent_column_id = c.column_id
WHERE dc.parent_object_id = OBJECT_ID('targets') AND c.name = 'target_incarnation_id';
IF @sql IS NOT NULL EXEC sp_executesql @sql;
-- +goose StatementEnd

ALTER TABLE targets DROP COLUMN last_error_code;
ALTER TABLE targets DROP COLUMN unreachable_accum_seconds;
ALTER TABLE targets DROP COLUMN last_sample_at;
ALTER TABLE targets DROP COLUMN first_unreachable_at;
ALTER TABLE targets DROP COLUMN observed_at;
ALTER TABLE targets DROP COLUMN last_seen_at;
ALTER TABLE targets DROP COLUMN health_state;
ALTER TABLE targets DROP COLUMN target_incarnation_id;
