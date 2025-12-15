-- +goose Up
-- Drop the data column now that all data has been migrated to normalized columns
-- SQLite doesn't support DROP COLUMN directly in older versions, so we need to recreate the table

-- Create a new table without the data column
CREATE TABLE forma_commands_new (
    command_id TEXT NOT NULL,
    timestamp TEXT NOT NULL,
    command TEXT NOT NULL,
    state TEXT NOT NULL,
    agent_version TEXT,
    client_id TEXT,
    agent_id TEXT,
    description TEXT,
    config TEXT,
    target_updates TEXT,
    modified_ts TEXT,
    PRIMARY KEY (command_id)
);

-- Copy data from old table to new table
INSERT INTO forma_commands_new (
    command_id, timestamp, command, state, agent_version, client_id, agent_id,
    description, config, target_updates, modified_ts
)
SELECT
    command_id, timestamp, command, state, agent_version, client_id, agent_id,
    description, config, target_updates, modified_ts
FROM forma_commands;

-- Drop old table
DROP TABLE forma_commands;

-- Rename new table to original name
ALTER TABLE forma_commands_new RENAME TO forma_commands;

-- Recreate indexes
CREATE INDEX IF NOT EXISTS idx_timestamp ON forma_commands (timestamp);
CREATE INDEX IF NOT EXISTS idx_command ON forma_commands (command);
CREATE INDEX IF NOT EXISTS idx_state ON forma_commands (state);
CREATE INDEX IF NOT EXISTS idx_agent_version ON forma_commands (agent_version);
CREATE INDEX IF NOT EXISTS idx_agent_id ON forma_commands (agent_id);
CREATE INDEX IF NOT EXISTS idx_client_id ON forma_commands (client_id);

-- +goose Down
-- Add back the data column
-- Note: This is a destructive operation - we cannot fully restore the original JSON data
-- The down migration creates the column but leaves it NULL

CREATE TABLE forma_commands_old (
    command_id TEXT NOT NULL,
    timestamp TEXT NOT NULL,
    command TEXT NOT NULL,
    state TEXT NOT NULL,
    agent_version TEXT,
    client_id TEXT,
    agent_id TEXT,
    data TEXT,
    description TEXT,
    config TEXT,
    target_updates TEXT,
    modified_ts TEXT,
    PRIMARY KEY (command_id)
);

INSERT INTO forma_commands_old (
    command_id, timestamp, command, state, agent_version, client_id, agent_id,
    description, config, target_updates, modified_ts
)
SELECT
    command_id, timestamp, command, state, agent_version, client_id, agent_id,
    description, config, target_updates, modified_ts
FROM forma_commands;

DROP TABLE forma_commands;

ALTER TABLE forma_commands_old RENAME TO forma_commands;

CREATE INDEX IF NOT EXISTS idx_timestamp ON forma_commands (timestamp);
CREATE INDEX IF NOT EXISTS idx_command ON forma_commands (command);
CREATE INDEX IF NOT EXISTS idx_state ON forma_commands (state);
CREATE INDEX IF NOT EXISTS idx_agent_version ON forma_commands (agent_version);
CREATE INDEX IF NOT EXISTS idx_agent_id ON forma_commands (agent_id);
CREATE INDEX IF NOT EXISTS idx_client_id ON forma_commands (client_id);
