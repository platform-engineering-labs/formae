-- © 2025 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- Append-only record of agent starts. One row per process start, carrying the
-- build the agent is running. Nothing in the agent reads it back: it exists so
-- an out-of-process reader can answer "which version is this agent, and is it
-- alive" for an installation that has not yet run any command, which the
-- command history cannot answer because there are no commands.
-- +goose StatementBegin
IF NOT EXISTS (SELECT 1 FROM sys.tables WHERE name = 'agent_boots')
BEGIN
    CREATE TABLE agent_boots (
        boot_id   nvarchar(450) NOT NULL PRIMARY KEY,
        version   nvarchar(max) NOT NULL,
        booted_at datetime2 NOT NULL
    );
    CREATE INDEX idx_agent_boots_booted_at ON agent_boots (booted_at, boot_id);
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
IF EXISTS (SELECT 1 FROM sys.tables WHERE name = 'agent_boots')
BEGIN
    DROP TABLE agent_boots;
END;
-- +goose StatementEnd
