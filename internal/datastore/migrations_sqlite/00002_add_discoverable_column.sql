-- +goose Up
-- No support for ALTER TABLE ADD COLUMN IF NOT EXISTS in sqlite
-- Recreate the table to ensure the column exists
-- This is idempotent: works whether column exists or not
CREATE TABLE targets_temp (
    label TEXT NOT NULL,
    version INTEGER NOT NULL,
    namespace TEXT NOT NULL,
    config TEXT,
    discoverable INTEGER DEFAULT 0,
    PRIMARY KEY (label, version)
);

INSERT INTO targets_temp (label, version, namespace, config)
SELECT label, version, namespace, config
FROM targets;

DROP TABLE targets;
ALTER TABLE targets_temp RENAME TO targets;

-- Recreate indexes
CREATE INDEX IF NOT EXISTS idx_namespace ON targets (namespace);
CREATE INDEX IF NOT EXISTS idx_discoverable ON targets (discoverable);

-- +goose Down
DROP INDEX IF EXISTS idx_discoverable;
-- SQLite doesn't support DROP COLUMN, so we skip it
-- In prod, this requires table recreate
