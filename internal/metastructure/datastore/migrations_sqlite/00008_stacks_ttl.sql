-- +goose Up
ALTER TABLE stacks ADD COLUMN ttl_seconds INTEGER;
ALTER TABLE stacks ADD COLUMN expires_at TIMESTAMP;
ALTER TABLE stacks ADD COLUMN created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP;
ALTER TABLE stacks ADD COLUMN updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP;

CREATE INDEX IF NOT EXISTS idx_stacks_expires_at ON stacks (expires_at);

-- +goose Down
DROP INDEX IF EXISTS idx_stacks_expires_at;
-- SQLite doesn't support DROP COLUMN, so we need to recreate the table
-- For now, we leave the columns in place on rollback
