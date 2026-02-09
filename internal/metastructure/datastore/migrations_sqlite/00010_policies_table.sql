-- +goose Up
CREATE TABLE IF NOT EXISTS policies (
    id TEXT NOT NULL,
    version TEXT NOT NULL,
    valid_from TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    command_id TEXT,
    operation TEXT NOT NULL,
    label TEXT NOT NULL,
    policy_type TEXT NOT NULL,
    stack_id TEXT,  -- NULL = standalone, non-NULL = inline
    policy_data TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (id, version)
);

CREATE INDEX IF NOT EXISTS idx_policies_stack_id ON policies(stack_id);
CREATE INDEX IF NOT EXISTS idx_policies_policy_type ON policies(policy_type);
CREATE INDEX IF NOT EXISTS idx_policies_label ON policies(label);

-- +goose Down
DROP INDEX IF EXISTS idx_policies_label;
DROP INDEX IF EXISTS idx_policies_policy_type;
DROP INDEX IF EXISTS idx_policies_stack_id;
DROP TABLE IF EXISTS policies;
