-- +goose Up
-- Junction table for many-to-many relationship between stacks and standalone policies.
-- This table tracks which standalone policies are attached to which stacks.
-- Inline policies use the stack_id column in the policies table directly.
CREATE TABLE IF NOT EXISTS stack_policies (
    stack_id TEXT NOT NULL,
    policy_id TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (stack_id, policy_id)
);

CREATE INDEX IF NOT EXISTS idx_stack_policies_stack_id ON stack_policies(stack_id);
CREATE INDEX IF NOT EXISTS idx_stack_policies_policy_id ON stack_policies(policy_id);

-- +goose Down
DROP INDEX IF EXISTS idx_stack_policies_policy_id;
DROP INDEX IF EXISTS idx_stack_policies_stack_id;
DROP TABLE IF EXISTS stack_policies;
