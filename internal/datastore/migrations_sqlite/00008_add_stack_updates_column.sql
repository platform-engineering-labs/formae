-- +goose Up
-- Add stack_updates column to forma_commands table (mirrors target_updates pattern)
ALTER TABLE forma_commands ADD COLUMN stack_updates TEXT;

-- +goose Down
ALTER TABLE forma_commands DROP COLUMN stack_updates;
