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
ALTER TABLE generators ADD COLUMN generation_id TEXT NOT NULL DEFAULT '';
ALTER TABLE generators ADD COLUMN generation_spec JSONB NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE generators DROP COLUMN generation_spec;
ALTER TABLE generators DROP COLUMN generation_id;
