-- © 2025 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- Persist each target's resolved reaping behaviour. reap_kind is the variant
-- ('after' or 'never'); reap_max_unreachable_seconds is the reap-after duration.
-- The NOT NULL DEFAULT clauses backfill existing rows to the global default
-- (after / 86400s).

ALTER TABLE targets ADD reap_kind nvarchar(450) NOT NULL DEFAULT 'after';
ALTER TABLE targets ADD reap_max_unreachable_seconds bigint NOT NULL DEFAULT 86400;

-- +goose Down
-- Columns with inline DEFAULT clauses get system-named default constraints on
-- SQL Server. Those constraints must be dropped before the column can be dropped.
-- The DECLARE/SELECT/EXEC pattern looks up the constraint name dynamically so it
-- works regardless of the system-generated name and is idempotent.
-- +goose StatementBegin
DECLARE @sql nvarchar(max);

SELECT @sql = 'ALTER TABLE targets DROP CONSTRAINT ' + dc.name
FROM sys.default_constraints dc
JOIN sys.columns c ON dc.parent_object_id = c.object_id AND dc.parent_column_id = c.column_id
WHERE dc.parent_object_id = OBJECT_ID('targets') AND c.name = 'reap_max_unreachable_seconds';
IF @sql IS NOT NULL EXEC sp_executesql @sql;
SET @sql = NULL;

SELECT @sql = 'ALTER TABLE targets DROP CONSTRAINT ' + dc.name
FROM sys.default_constraints dc
JOIN sys.columns c ON dc.parent_object_id = c.object_id AND dc.parent_column_id = c.column_id
WHERE dc.parent_object_id = OBJECT_ID('targets') AND c.name = 'reap_kind';
IF @sql IS NOT NULL EXEC sp_executesql @sql;
-- +goose StatementEnd

ALTER TABLE targets DROP COLUMN reap_max_unreachable_seconds;
ALTER TABLE targets DROP COLUMN reap_kind;
