-- +goose Up
-- Create the resource_updates table to store ResourceUpdates separately from forma_commands
-- This enables O(1) lookups and updates instead of deserializing the entire JSON blob
CREATE TABLE IF NOT EXISTS resource_updates (
    command_id TEXT NOT NULL,
    ksuid TEXT NOT NULL,
    operation TEXT NOT NULL,
    state TEXT NOT NULL,
    start_ts TIMESTAMP,
    modified_ts TIMESTAMP,
    retries INTEGER DEFAULT 0,
    remaining INTEGER DEFAULT 0,
    version TEXT,
    stack_label TEXT,
    group_id TEXT,
    source TEXT,
    resource TEXT,
    resource_target TEXT,
    existing_resource TEXT,
    existing_target TEXT,
    metadata TEXT,
    progress_result TEXT,
    most_recent_progress TEXT,
    remaining_resolvables TEXT,
    reference_labels TEXT,
    previous_properties TEXT,
    PRIMARY KEY (command_id, ksuid, operation)
);

CREATE INDEX IF NOT EXISTS idx_ru_command_id ON resource_updates (command_id);
CREATE INDEX IF NOT EXISTS idx_ru_ksuid ON resource_updates (ksuid);
CREATE INDEX IF NOT EXISTS idx_ru_state ON resource_updates (state);
CREATE INDEX IF NOT EXISTS idx_ru_stack_label ON resource_updates (stack_label);
CREATE INDEX IF NOT EXISTS idx_ru_operation ON resource_updates (operation);

-- +goose Down
DROP INDEX IF EXISTS idx_ru_operation;
DROP INDEX IF EXISTS idx_ru_stack_label;
DROP INDEX IF EXISTS idx_ru_state;
DROP INDEX IF EXISTS idx_ru_ksuid;
DROP INDEX IF EXISTS idx_ru_command_id;
DROP TABLE IF EXISTS resource_updates;
