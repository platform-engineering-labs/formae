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
IF EXISTS (
    SELECT 1 FROM sys.columns
    WHERE object_id = OBJECT_ID('generators') AND name = 'generation_id'
)
BEGIN
    ALTER TABLE generators DROP COLUMN generation_id;
END;
-- +goose StatementEnd
