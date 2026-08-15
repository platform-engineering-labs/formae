-- © 2025 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- Append-only record of agent starts. One row per process start, carrying the
-- build the agent is running. Nothing in the agent reads it back: it exists so
-- an out-of-process reader can answer "which version is this agent, and is it
-- alive" for an installation that has not yet run any command, which the
-- command history cannot answer because there are no commands.
CREATE TABLE IF NOT EXISTS agent_boots (
    boot_id   TEXT PRIMARY KEY,
    version   TEXT NOT NULL,
    booted_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_agent_boots_booted_at ON agent_boots (booted_at, boot_id);

-- +goose Down
DROP INDEX IF EXISTS idx_agent_boots_booted_at;
DROP TABLE IF EXISTS agent_boots;
