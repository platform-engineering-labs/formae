-- +goose Up
-- Convert existing UUID-like stack ids to KSUID-like format with '0' prefix
-- This ensures seeded ids sort before newly generated KSUIDs (which start with timestamp)

-- Create a temp table mapping old ids to new KSUID-like ids
-- Format: '0' + 26 random hex chars = 27 chars (same length as KSUID)
CREATE TEMP TABLE stack_id_mapping AS
SELECT DISTINCT id as old_id,
    '0' || substr(encode(gen_random_bytes(13), 'hex'), 1, 26) as new_id
FROM stacks;

-- Update all rows to use the new ids
UPDATE stacks
SET id = (SELECT new_id FROM stack_id_mapping WHERE old_id = stacks.id);

DROP TABLE stack_id_mapping;

-- +goose Down
-- Cannot reliably restore original UUIDs, but this is backward compatible
-- since the id column is TEXT and only needs uniqueness
