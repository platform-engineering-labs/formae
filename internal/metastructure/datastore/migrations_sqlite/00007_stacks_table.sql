-- +goose Up
CREATE TABLE IF NOT EXISTS stacks (
    id TEXT NOT NULL,
    version TEXT NOT NULL,
    valid_from TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    command_id TEXT,
    operation TEXT NOT NULL,
    label TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (id, version)
);

CREATE INDEX IF NOT EXISTS idx_stacks_label ON stacks (label);
CREATE INDEX IF NOT EXISTS idx_stacks_valid_from ON stacks (valid_from);
CREATE INDEX IF NOT EXISTS idx_stacks_command_id ON stacks (command_id);

-- Seed with existing stack labels from resources (excluding $unmanaged stack)
-- Note: SQLite doesn't support KSUID generation natively. We use a hex-based
-- unique ID prefixed with '0' to ensure it sorts before any real KSUIDs
-- (which start with timestamps). The application generates proper KSUIDs
-- for all new stacks.
INSERT INTO stacks (id, version, operation, label, description)
SELECT 
    '0' || substr(lower(hex(randomblob(13))), 1, 26) AS id,
    '0' || substr(lower(hex(randomblob(13))), 1, 26) AS version,
    'create',
    stack,
    ''
FROM (
    SELECT DISTINCT stack
    FROM resources
    WHERE stack IS NOT NULL
      AND stack != ''
      AND stack != '$unmanaged'
);

-- +goose Down
DROP INDEX IF EXISTS idx_stacks_command_id;
DROP INDEX IF EXISTS idx_stacks_valid_from;
DROP INDEX IF EXISTS idx_stacks_label;
DROP TABLE IF EXISTS stacks;
