-- © 2026 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- The generation a generator currently holds, and the spec it was drawn under.
--
-- Controller state, deliberately NOT inside generator_data: generator_data is
-- the declared desired spec, and generation identity must never participate in
-- desired-config equality. This mirrors how last-reconcile-at is kept out of
-- policy desired config.
--
-- generation_id is empty until a value has actually been drawn; a destination
-- bound to a generator with no generation has nothing to resolve against and
-- must be planned. It is advanced ONLY by a rotation, never by a spec edit or
-- an alias rename, both of which write a new version row.
-- +goose StatementBegin
IF NOT EXISTS (
    SELECT 1 FROM sys.columns
    WHERE object_id = OBJECT_ID('generators') AND name = 'generation_id'
)
BEGIN
    ALTER TABLE generators ADD generation_id nvarchar(450) NOT NULL DEFAULT '';
END;
-- +goose StatementEnd
-- +goose StatementBegin
IF NOT EXISTS (
    SELECT 1 FROM sys.columns
    WHERE object_id = OBJECT_ID('generators') AND name = 'generation_spec'
)
BEGIN
    ALTER TABLE generators ADD generation_spec nvarchar(max) NOT NULL DEFAULT '';
END;
-- +goose StatementEnd

-- +goose Down
-- Columns with inline DEFAULT clauses get system-named default constraints on
-- SQL Server. Those constraints must be dropped before the column can be
-- dropped. The DECLARE/SELECT/EXEC pattern looks up the constraint name
-- dynamically so it works regardless of the system-generated name; it is
-- idempotent because @sql stays NULL when the column or constraint is absent.
-- +goose StatementBegin
DECLARE @sql nvarchar(max);

SELECT @sql = 'ALTER TABLE generators DROP CONSTRAINT ' + dc.name
FROM sys.default_constraints dc
JOIN sys.columns c ON dc.parent_object_id = c.object_id AND dc.parent_column_id = c.column_id
WHERE dc.parent_object_id = OBJECT_ID('generators') AND c.name = 'generation_spec';
IF @sql IS NOT NULL EXEC sp_executesql @sql;
-- +goose StatementEnd
-- +goose StatementBegin
IF EXISTS (
    SELECT 1 FROM sys.columns
    WHERE object_id = OBJECT_ID('generators') AND name = 'generation_spec'
)
BEGIN
    ALTER TABLE generators DROP COLUMN generation_spec;
END;
-- +goose StatementEnd
-- +goose StatementBegin
DECLARE @sql nvarchar(max);

SELECT @sql = 'ALTER TABLE generators DROP CONSTRAINT ' + dc.name
FROM sys.default_constraints dc
JOIN sys.columns c ON dc.parent_object_id = c.object_id AND dc.parent_column_id = c.column_id
WHERE dc.parent_object_id = OBJECT_ID('generators') AND c.name = 'generation_id';
IF @sql IS NOT NULL EXEC sp_executesql @sql;
-- +goose StatementEnd
-- +goose StatementBegin
IF EXISTS (
    SELECT 1 FROM sys.columns
    WHERE object_id = OBJECT_ID('generators') AND name = 'generation_id'
)
BEGIN
    ALTER TABLE generators DROP COLUMN generation_id;
END;
-- +goose StatementEnd
