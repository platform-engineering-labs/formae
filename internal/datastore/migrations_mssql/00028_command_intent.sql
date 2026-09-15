-- © 2026 Platform Engineering Labs Inc.
-- SPDX-License-Identifier: FSL-1.1-ALv2
-- +goose Up
ALTER TABLE forma_commands ADD message NVARCHAR(MAX) NULL;
ALTER TABLE forma_commands ADD input_properties NVARCHAR(MAX) NULL;
CREATE TABLE command_stacks (
    command_id NVARCHAR(450) COLLATE Latin1_General_BIN2 NOT NULL,
    stack_id NVARCHAR(450) COLLATE Latin1_General_BIN2 NOT NULL,
    stack_label NVARCHAR(450) NOT NULL,
    CONSTRAINT pk_command_stacks PRIMARY KEY (command_id, stack_id)
);
CREATE INDEX command_stacks_stack_label_idx ON command_stacks(stack_label, command_id);
-- +goose Down
DROP TABLE command_stacks;
ALTER TABLE forma_commands DROP COLUMN input_properties;
ALTER TABLE forma_commands DROP COLUMN message;
