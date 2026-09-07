-- © 2026 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- Persist the suppressed-drift notes computed at submission: the record of
-- out-of-band movement on provider-default fields the command's plan could
-- not see, whose completion advances the drift window past that movement.
-- Nullable: commands without suppressed movement store nothing.
ALTER TABLE forma_commands ADD suppressed_drift_notes nvarchar(max) NULL;

-- +goose Down
ALTER TABLE forma_commands DROP COLUMN suppressed_drift_notes;
