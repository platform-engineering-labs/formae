-- +goose Up
-- Add config_schema column to targets table for storing per-field mutability hints.
-- SQLite doesn't support ALTER TABLE ADD COLUMN IF NOT EXISTS reliably,
-- so we recreate the table.
CREATE TABLE targets_temp (
    label TEXT NOT NULL,
    version INTEGER NOT NULL,
    namespace TEXT NOT NULL,
    config TEXT,
    discoverable INTEGER DEFAULT 0,
    config_schema TEXT,
    PRIMARY KEY (label, version)
);

INSERT INTO targets_temp (label, version, namespace, config, discoverable)
SELECT label, version, namespace, config, discoverable
FROM targets;

DROP TABLE targets;
ALTER TABLE targets_temp RENAME TO targets;

-- Recreate indexes
CREATE INDEX IF NOT EXISTS idx_namespace ON targets (namespace);
CREATE INDEX IF NOT EXISTS idx_discoverable ON targets (discoverable);

-- +goose Down
-- SQLite doesn't support DROP COLUMN, so we skip it
