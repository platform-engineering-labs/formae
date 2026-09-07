-- © 2026 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- Resolution provenance for reference occurrences: the immutable per-
-- occurrence records computed at planning, and the source-URI digest map
-- resolution populates (persisted with progress so post-dispatch recovery can
-- stamp without re-resolving). Both nullable: rows written before this
-- migration carry no provenance and classify as unknown.
ALTER TABLE resource_updates ADD COLUMN provenance_records TEXT;
ALTER TABLE resource_updates ADD COLUMN resolved_root_digests TEXT;

-- +goose Down
ALTER TABLE resource_updates DROP COLUMN provenance_records;
ALTER TABLE resource_updates DROP COLUMN resolved_root_digests;
