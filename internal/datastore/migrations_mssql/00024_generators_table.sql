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
-- +goose StatementBegin
IF NOT EXISTS (SELECT 1 FROM sys.tables WHERE name = 'generators')
BEGIN
    CREATE TABLE generators (
        id             nvarchar(450) COLLATE Latin1_General_BIN2 NOT NULL,
        version        nvarchar(450) COLLATE Latin1_General_BIN2 NOT NULL,
        valid_from     datetime2     DEFAULT SYSUTCDATETIME(),
        command_id     nvarchar(450) COLLATE Latin1_General_BIN2,
        operation      nvarchar(450) NOT NULL,
        label          nvarchar(450) NOT NULL,
        generator_type nvarchar(450) NOT NULL,
        stack_id       nvarchar(450) COLLATE Latin1_General_BIN2 NOT NULL,
        generator_data nvarchar(max) NOT NULL DEFAULT '{}',
        PRIMARY KEY (id, version)
    );
    CREATE INDEX idx_generators_stack_id       ON generators (stack_id);
    CREATE INDEX idx_generators_generator_type ON generators (generator_type);
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
IF EXISTS (SELECT 1 FROM sys.tables WHERE name = 'generators')
BEGIN
    DROP TABLE generators;
END;
-- +goose StatementEnd
