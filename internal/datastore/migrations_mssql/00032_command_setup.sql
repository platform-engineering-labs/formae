-- © 2026 Platform Engineering Labs Inc.
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
ALTER TABLE forma_commands ADD setup_metadata NVARCHAR(MAX) NULL;

-- +goose Down
ALTER TABLE forma_commands DROP COLUMN setup_metadata;
