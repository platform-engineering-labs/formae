-- © 2025 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
ALTER TABLE targets ADD COLUMN config_schema TEXT;

-- +goose Down
-- SQLite doesn't support DROP COLUMN before 3.35.0, so we recreate
CREATE TABLE targets_backup AS SELECT label, version, namespace, config, discoverable FROM targets;
DROP TABLE targets;
CREATE TABLE targets (
    label TEXT NOT NULL,
    version INTEGER NOT NULL,
    namespace TEXT NOT NULL,
    config TEXT,
    discoverable INTEGER DEFAULT 0,
    PRIMARY KEY (label, version)
);
INSERT INTO targets SELECT * FROM targets_backup;
DROP TABLE targets_backup;
CREATE INDEX IF NOT EXISTS idx_namespace ON targets (namespace);
CREATE INDEX IF NOT EXISTS idx_discoverable ON targets (discoverable);
