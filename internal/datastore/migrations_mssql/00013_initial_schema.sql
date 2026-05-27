-- © 2025 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- Consolidated schema at v13. Numbering aligns with the sqlite/postgres
-- 1..13 migration history so future schema changes (v14+) stay in lockstep
-- across all three backends.

-- +goose Up
CREATE TABLE forma_commands (
    command_id          nvarchar(450) NOT NULL,
    timestamp           datetime2     NOT NULL,
    command             nvarchar(450) NOT NULL,
    state               nvarchar(450) NOT NULL,
    agent_version       nvarchar(450),
    client_id           nvarchar(450),
    agent_id            nvarchar(450),
    description_text    nvarchar(max),
    description_confirm bit           DEFAULT 0,
    config_mode         nvarchar(450) DEFAULT 'reconcile',
    config_force        bit           DEFAULT 0,
    config_simulate     bit           DEFAULT 0,
    target_updates      nvarchar(max),
    stack_updates       nvarchar(max),
    policy_updates      nvarchar(max),
    modified_ts         datetime2,
    PRIMARY KEY (command_id)
);

CREATE INDEX idx_forma_commands_timestamp     ON forma_commands (timestamp);
CREATE INDEX idx_forma_commands_command       ON forma_commands (command);
CREATE INDEX idx_forma_commands_state         ON forma_commands (state);
CREATE INDEX idx_forma_commands_agent_version ON forma_commands (agent_version);
CREATE INDEX idx_forma_commands_agent_id      ON forma_commands (agent_id);
CREATE INDEX idx_forma_commands_client_id     ON forma_commands (client_id);
CREATE INDEX idx_config_mode                  ON forma_commands (config_mode);

CREATE TABLE resources (
    uri        nvarchar(450) NOT NULL,
    version    nvarchar(450) NOT NULL,
    valid_from datetime2     DEFAULT SYSUTCDATETIME(),
    command_id nvarchar(450),
    operation  nvarchar(450),
    native_id  nvarchar(450),
    stack      nvarchar(450),
    type       nvarchar(450),
    label      nvarchar(450),
    target     nvarchar(450),
    data       nvarchar(max),
    managed    bit           DEFAULT 1,
    ksuid      nvarchar(450),
    PRIMARY KEY (uri, version)
);

CREATE INDEX idx_resources_valid_from               ON resources (valid_from);
CREATE INDEX idx_resources_command_id               ON resources (command_id);
CREATE INDEX idx_resources_operation                ON resources (operation);
CREATE INDEX idx_resources_native_id                ON resources (native_id);
CREATE INDEX idx_resources_stack                    ON resources (stack);
CREATE INDEX idx_resources_type                     ON resources (type);
CREATE INDEX idx_resources_label                    ON resources (label);
CREATE INDEX idx_resources_stack_label_type_version ON resources (stack, label, type, version);
CREATE INDEX idx_resources_ksuid                    ON resources (ksuid);

CREATE TABLE targets (
    label         nvarchar(450) NOT NULL,
    version       int           NOT NULL,
    namespace     nvarchar(450) NOT NULL,
    config        nvarchar(max),
    discoverable  bit           DEFAULT 0,
    config_schema nvarchar(max),
    PRIMARY KEY (label, version)
);

CREATE INDEX idx_targets_namespace    ON targets (namespace);
CREATE INDEX idx_targets_discoverable ON targets (discoverable);

CREATE TABLE resource_updates (
    command_id            nvarchar(450) NOT NULL,
    ksuid                 nvarchar(450) NOT NULL,
    operation             nvarchar(450) NOT NULL,
    state                 nvarchar(450) NOT NULL,
    start_ts              datetime2,
    modified_ts           datetime2,
    retries               int           DEFAULT 0,
    remaining             int           DEFAULT 0,
    version               nvarchar(max),
    stack_label           nvarchar(450),
    group_id              nvarchar(max),
    source                nvarchar(max),
    resource              nvarchar(max),
    resource_target       nvarchar(max),
    existing_resource     nvarchar(max),
    existing_target       nvarchar(max),
    progress_result       nvarchar(max),
    most_recent_progress  nvarchar(max),
    remaining_resolvables nvarchar(max),
    reference_labels      nvarchar(max),
    previous_properties   nvarchar(max),
    is_cascade            bit           NOT NULL DEFAULT 0,
    cascade_source        nvarchar(max) NOT NULL DEFAULT '',
    PRIMARY KEY (command_id, ksuid, operation)
);

CREATE INDEX idx_ru_command_id  ON resource_updates (command_id);
CREATE INDEX idx_ru_ksuid       ON resource_updates (ksuid);
CREATE INDEX idx_ru_state       ON resource_updates (state);
CREATE INDEX idx_ru_stack_label ON resource_updates (stack_label);
CREATE INDEX idx_ru_operation   ON resource_updates (operation);

CREATE TABLE stacks (
    id          nvarchar(450) NOT NULL,
    version     nvarchar(450) NOT NULL,
    valid_from  datetime2     DEFAULT SYSUTCDATETIME(),
    command_id  nvarchar(450),
    operation   nvarchar(450) NOT NULL,
    label       nvarchar(450) NOT NULL,
    description nvarchar(max) NOT NULL DEFAULT '',
    PRIMARY KEY (id, version)
);

CREATE INDEX idx_stacks_label      ON stacks (label);
CREATE INDEX idx_stacks_valid_from ON stacks (valid_from);
CREATE INDEX idx_stacks_command_id ON stacks (command_id);

CREATE TABLE policies (
    id          nvarchar(450) NOT NULL,
    version     nvarchar(450) NOT NULL,
    valid_from  datetime2     DEFAULT SYSUTCDATETIME(),
    command_id  nvarchar(450),
    operation   nvarchar(450) NOT NULL,
    label       nvarchar(450) NOT NULL,
    policy_type nvarchar(450) NOT NULL,
    stack_id    nvarchar(450),  -- NULL/empty = standalone, set = inline to that stack
    policy_data nvarchar(max) NOT NULL DEFAULT '{}',
    PRIMARY KEY (id, version)
);

CREATE INDEX idx_policies_stack_id    ON policies (stack_id);
CREATE INDEX idx_policies_policy_type ON policies (policy_type);
CREATE INDEX idx_policies_label       ON policies (label);

-- Junction table for the many-to-many between stacks and standalone policies.
-- Inline policies use the policies.stack_id column directly.
CREATE TABLE stack_policies (
    stack_id   nvarchar(450) NOT NULL,
    policy_id  nvarchar(450) NOT NULL,
    created_at datetime2     DEFAULT SYSUTCDATETIME(),
    PRIMARY KEY (stack_id, policy_id)
);

CREATE INDEX idx_stack_policies_stack_id  ON stack_policies (stack_id);
CREATE INDEX idx_stack_policies_policy_id ON stack_policies (policy_id);
