-- © 2026 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- The suppressed-drift notes column shipped only in 0.89.0 dev builds and
-- was superseded before a stable release: witnessed out-of-band movement is
-- now ordinary drift (rejected or force-reverted), so nothing writes or
-- reads the column. The bundled SQLite (3.46, statically compiled into the
-- agent) supports DROP COLUMN, and the column is plain nullable TEXT with
-- no index, so the drop is safe.
ALTER TABLE forma_commands DROP COLUMN suppressed_drift_notes;

-- +goose Down
ALTER TABLE forma_commands ADD COLUMN suppressed_drift_notes TEXT;
