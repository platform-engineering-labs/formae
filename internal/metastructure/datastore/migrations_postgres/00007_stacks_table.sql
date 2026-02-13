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
-- Note: id is a UUID (just needs uniqueness), version uses '0' prefix to sort
-- before real KSUIDs (which start with timestamps).
-- This approach is compatible with both regular PostgreSQL and Aurora Data API.
INSERT INTO stacks (id, version, operation, label, description)
SELECT
    gen_random_uuid()::text AS id,
    '0' || substring(replace(gen_random_uuid()::text, '-', ''), 1, 26) AS version,
    'create',
    stack,
    ''
FROM (
    SELECT DISTINCT stack
    FROM resources
    WHERE stack IS NOT NULL
      AND stack != ''
      AND stack != '$unmanaged'
) AS distinct_stacks;

-- +goose Down
DROP INDEX IF EXISTS idx_stacks_command_id;
DROP INDEX IF EXISTS idx_stacks_valid_from;
DROP INDEX IF EXISTS idx_stacks_label;
DROP TABLE IF EXISTS stacks;
