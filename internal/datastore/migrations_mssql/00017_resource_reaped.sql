-- © 2025 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- Support the 'reaped' resource tombstone and the incarnation-carrying
-- resource-write guard.
--
-- The 'reaped' marker is a distinct value of the existing free-text `operation`
-- column (alongside 'create'/'update'/'delete'); no schema change is needed to
-- store it. A reaped row records that the resource's target was reaped (the
-- provider was never asked to delete it) and is kept in the table but hidden
-- from every live-resource query.
--
-- target_incarnation_id stamps each resource-version row with the target
-- incarnation under which it was written. The resource-write guard compares an
-- expected incarnation against the current row's value to reject stale writes
-- from a superseded incarnation. Existing rows backfill to '' (no incarnation),
-- which the guard treats as "no incarnation check".
ALTER TABLE resources ADD target_incarnation_id nvarchar(450) NOT NULL DEFAULT '';

-- +goose Down
-- The inline DEFAULT clause gets a system-named default constraint on SQL
-- Server that must be dropped before the column can be dropped. The
-- DECLARE/SELECT/EXEC pattern looks up the constraint name dynamically so it
-- works regardless of the system-generated name and is idempotent.
-- +goose StatementBegin
DECLARE @sql nvarchar(max);

SELECT @sql = 'ALTER TABLE resources DROP CONSTRAINT ' + dc.name
FROM sys.default_constraints dc
JOIN sys.columns c ON dc.parent_object_id = c.object_id AND dc.parent_column_id = c.column_id
WHERE dc.parent_object_id = OBJECT_ID('resources') AND c.name = 'target_incarnation_id';
IF @sql IS NOT NULL EXEC sp_executesql @sql;
-- +goose StatementEnd

ALTER TABLE resources DROP COLUMN target_incarnation_id;
