-- +goose Up
CREATE TABLE IF NOT EXISTS forma_commands (
    command_id TEXT NOT NULL,
    timestamp TEXT NOT NULL,
    command TEXT NOT NULL,
    state TEXT NOT NULL,
    agent_version TEXT,
    client_id TEXT,
    agent_id TEXT,
    data TEXT,
    PRIMARY KEY (command_id)
);

CREATE INDEX IF NOT EXISTS idx_timestamp ON forma_commands (timestamp);
CREATE INDEX IF NOT EXISTS idx_command ON forma_commands (command);
CREATE INDEX IF NOT EXISTS idx_state ON forma_commands (state);
CREATE INDEX IF NOT EXISTS idx_agent_version ON forma_commands (agent_version);
CREATE INDEX IF NOT EXISTS idx_agent_id ON forma_commands (agent_id);
CREATE INDEX IF NOT EXISTS idx_client_id ON forma_commands (client_id);

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
    data TEXT,
    managed INTEGER DEFAULT 1,
    ksuid TEXT,
    PRIMARY KEY (uri, version)
);

CREATE INDEX IF NOT EXISTS idx_valid_from ON resources (valid_from);
CREATE INDEX IF NOT EXISTS idx_command_id ON resources (command_id);
CREATE INDEX IF NOT EXISTS idx_operation ON resources (operation);
CREATE INDEX IF NOT EXISTS idx_native_id ON resources (native_id);
CREATE INDEX IF NOT EXISTS idx_stack ON resources (stack);
CREATE INDEX IF NOT EXISTS idx_type ON resources (type);
CREATE INDEX IF NOT EXISTS idx_label ON resources (label);
CREATE INDEX IF NOT EXISTS idx_resources_stack_label_type_version ON resources (stack, label, type, version);
CREATE INDEX IF NOT EXISTS idx_ksuid ON resources (ksuid);

CREATE TABLE IF NOT EXISTS targets (
    label TEXT NOT NULL,
    version INTEGER NOT NULL,
    namespace TEXT NOT NULL,
    config TEXT,
    PRIMARY KEY (label, version)
);

CREATE INDEX IF NOT EXISTS idx_namespace ON targets (namespace);

-- +goose Down
DROP INDEX IF EXISTS idx_namespace;
DROP TABLE IF EXISTS targets;

DROP INDEX IF EXISTS idx_ksuid;
DROP INDEX IF EXISTS idx_resources_stack_label_type_version;
DROP INDEX IF EXISTS idx_label;
DROP INDEX IF EXISTS idx_type;
DROP INDEX IF EXISTS idx_stack;
DROP INDEX IF EXISTS idx_native_id;
DROP INDEX IF EXISTS idx_operation;
DROP INDEX IF EXISTS idx_command_id;
DROP INDEX IF EXISTS idx_valid_from;
DROP TABLE IF EXISTS resources;

DROP INDEX IF EXISTS idx_client_id;
DROP INDEX IF EXISTS idx_agent_id;
DROP INDEX IF EXISTS idx_agent_version;
DROP INDEX IF EXISTS idx_state;
DROP INDEX IF EXISTS idx_command;
DROP INDEX IF EXISTS idx_timestamp;
DROP TABLE IF EXISTS forma_commands;
