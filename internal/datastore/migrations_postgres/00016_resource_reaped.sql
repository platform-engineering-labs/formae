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
-- `target_incarnation_id` stamps each resource-version row with the target
-- incarnation under which it was written. The resource-write guard compares an
-- expected incarnation against the current row's value to reject stale writes
-- from a superseded incarnation. Existing rows backfill to '' (no incarnation),
-- which the guard treats as "no incarnation check".
ALTER TABLE resources ADD COLUMN target_incarnation_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE resources DROP COLUMN target_incarnation_id;
