-- +goose Up
CREATE TABLE IF NOT EXISTS forma_commands (
    command_id TEXT NOT NULL,
    timestamp TIMESTAMP NOT NULL,
    command TEXT NOT NULL,
    state TEXT NOT NULL,
    agent_version TEXT,
    client_id TEXT,
    agent_id TEXT,
    data JSONB,
    PRIMARY KEY (command_id)
);

CREATE INDEX IF NOT EXISTS idx_forma_commands_timestamp ON forma_commands (timestamp);
CREATE INDEX IF NOT EXISTS idx_forma_commands_command ON forma_commands (command);
CREATE INDEX IF NOT EXISTS idx_forma_commands_state ON forma_commands (state);
CREATE INDEX IF NOT EXISTS idx_forma_commands_agent_version ON forma_commands (agent_version);
CREATE INDEX IF NOT EXISTS idx_forma_commands_agent_id ON forma_commands (agent_id);
CREATE INDEX IF NOT EXISTS idx_forma_commands_client_id ON forma_commands (client_id);

CREATE TABLE IF NOT EXISTS resources (
    uri TEXT NOT NULL,
    version TEXT NOT NULL,
    valid_from TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    command_id TEXT,
    operation TEXT,
    native_id TEXT,
    stack TEXT,
    type TEXT,
    label TEXT,
    target TEXT,
    data JSONB,
    managed BOOLEAN DEFAULT TRUE,
    ksuid TEXT,
    PRIMARY KEY (uri, version)
);

CREATE INDEX IF NOT EXISTS idx_resources_valid_from ON resources (valid_from);
CREATE INDEX IF NOT EXISTS idx_resources_command_id ON resources (command_id);
CREATE INDEX IF NOT EXISTS idx_resources_operation ON resources (operation);
CREATE INDEX IF NOT EXISTS idx_resources_native_id ON resources (native_id);
CREATE INDEX IF NOT EXISTS idx_resources_stack ON resources (stack);
CREATE INDEX IF NOT EXISTS idx_resources_type ON resources (type);
CREATE INDEX IF NOT EXISTS idx_resources_label ON resources (label);
CREATE INDEX IF NOT EXISTS idx_resources_stack_label_type_version ON resources (stack, label, type, version);
CREATE INDEX IF NOT EXISTS idx_resources_ksuid ON resources (ksuid);

CREATE TABLE IF NOT EXISTS targets (
    label TEXT NOT NULL,
    version INTEGER NOT NULL,
    namespace TEXT NOT NULL,
    config JSONB,
    PRIMARY KEY (label, version)
);

CREATE INDEX IF NOT EXISTS idx_targets_namespace ON targets (namespace);

-- +goose Down
DROP INDEX IF EXISTS idx_targets_namespace;
DROP TABLE IF EXISTS targets;

DROP INDEX IF EXISTS idx_resources_ksuid;
DROP INDEX IF EXISTS idx_resources_stack_label_type_version;
DROP INDEX IF EXISTS idx_resources_label;
DROP INDEX IF EXISTS idx_resources_type;
DROP INDEX IF EXISTS idx_resources_stack;
DROP INDEX IF EXISTS idx_resources_native_id;
DROP INDEX IF EXISTS idx_resources_operation;
DROP INDEX IF EXISTS idx_resources_command_id;
DROP INDEX IF EXISTS idx_resources_valid_from;
DROP TABLE IF EXISTS resources;

DROP INDEX IF EXISTS idx_forma_commands_client_id;
DROP INDEX IF EXISTS idx_forma_commands_agent_id;
DROP INDEX IF EXISTS idx_forma_commands_agent_version;
DROP INDEX IF EXISTS idx_forma_commands_state;
DROP INDEX IF EXISTS idx_forma_commands_command;
DROP INDEX IF EXISTS idx_forma_commands_timestamp;
DROP TABLE IF EXISTS forma_commands;
