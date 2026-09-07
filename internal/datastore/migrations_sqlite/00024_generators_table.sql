-- © 2026 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- Generators produce values (e.g. random passwords) that secrets will later
-- reference. Unlike policies, a generator has no standalone form: it is
-- always owned by exactly one stack, so stack_id is NOT NULL and there is no
-- stack_generators junction table and no attach/detach.
--
-- Identity is the KSUID in id, not the label: a label is unique only within
-- its stack, and generator cadence will later be derived per generator id, so
-- a rename must not read as a delete plus a fresh generator.
CREATE TABLE IF NOT EXISTS generators (
    id TEXT NOT NULL,
    version TEXT NOT NULL,
    valid_from TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    command_id TEXT,
    operation TEXT NOT NULL,
    label TEXT NOT NULL,
    generator_type TEXT NOT NULL,
    stack_id TEXT NOT NULL,
    generator_data TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (id, version)
);

CREATE INDEX IF NOT EXISTS idx_generators_stack_id ON generators(stack_id);
CREATE INDEX IF NOT EXISTS idx_generators_generator_type ON generators(generator_type);

-- +goose Down
DROP INDEX IF EXISTS idx_generators_generator_type;
DROP INDEX IF EXISTS idx_generators_stack_id;
DROP TABLE IF EXISTS generators;
