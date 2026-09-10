-- © 2026 Platform Engineering Labs Inc.
-- SPDX-License-Identifier: FSL-1.1-ALv2
-- +goose Up
ALTER TABLE forma_commands ADD COLUMN message TEXT;
ALTER TABLE forma_commands ADD COLUMN input_properties JSONB;
CREATE TABLE command_stacks (
    command_id TEXT NOT NULL,
    stack_id TEXT NOT NULL,
    stack_label TEXT NOT NULL,
    PRIMARY KEY (command_id, stack_id)
);
CREATE INDEX command_stacks_stack_label_idx ON command_stacks(stack_label, command_id);
-- +goose Down
DROP TABLE command_stacks;
ALTER TABLE forma_commands DROP COLUMN input_properties;
ALTER TABLE forma_commands DROP COLUMN message;
