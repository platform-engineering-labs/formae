-- +goose Up
-- Add new columns to forma_commands for normalized storage
-- These columns replace the JSON blob in the 'data' column

-- Normalize description: was JSON {Text, Confirm}, now separate columns
ALTER TABLE forma_commands ADD COLUMN IF NOT EXISTS description_text TEXT;
ALTER TABLE forma_commands ADD COLUMN IF NOT EXISTS description_confirm BOOLEAN DEFAULT FALSE;

-- Normalize config: was JSON {Mode, Force, Simulate}, now separate columns
ALTER TABLE forma_commands ADD COLUMN IF NOT EXISTS config_mode TEXT DEFAULT 'reconcile';
ALTER TABLE forma_commands ADD COLUMN IF NOT EXISTS config_force BOOLEAN DEFAULT FALSE;
ALTER TABLE forma_commands ADD COLUMN IF NOT EXISTS config_simulate BOOLEAN DEFAULT FALSE;

-- Keep target_updates as JSON (array type, genuinely dynamic)
ALTER TABLE forma_commands ADD COLUMN IF NOT EXISTS target_updates TEXT;
ALTER TABLE forma_commands ADD COLUMN IF NOT EXISTS modified_ts TEXT;

-- Migrate existing data from the JSON blob to the new columns
UPDATE forma_commands
SET
    description_text = (data::jsonb->'Description')->>'Text',
    description_confirm = (data::jsonb->'Description')->>'Confirm' = 'true',
    config_mode = COALESCE((data::jsonb->'Config')->>'Mode', 'reconcile'),
    config_force = (data::jsonb->'Config')->>'Force' = 'true',
    config_simulate = (data::jsonb->'Config')->>'Simulate' = 'true',
    target_updates = (data::jsonb->'TargetUpdates')::text,
    modified_ts = data::jsonb->>'ModifiedTs'
WHERE data IS NOT NULL;

-- Migrate ResourceUpdates from the JSON blob to the resource_updates table
-- Uses jsonb_array_elements to iterate through the JSON array
INSERT INTO resource_updates (
    command_id, ksuid, operation, state, start_ts, modified_ts,
    retries, remaining, version, stack_label, group_id, source,
    resource, resource_target, existing_resource, existing_target,
    metadata, progress_result, most_recent_progress,
    remaining_resolvables, reference_labels, previous_properties
)
SELECT
    fc.command_id,
    (ru->'Resource')->>'Ksuid',
    ru->>'Operation',
    ru->>'State',
    ru->>'StartTs',
    ru->>'ModifiedTs',
    COALESCE((ru->>'Retries')::integer, 0),
    COALESCE((ru->>'Remaining')::integer, 0),
    ru->>'Version',
    COALESCE(ru->>'StackLabel', (ru->'Resource')->>'Stack'),
    ru->>'GroupID',
    ru->>'Source',
    (ru->'Resource')::text,
    (ru->'ResourceTarget')::text,
    (ru->'ExistingResource')::text,
    (ru->'ExistingTarget')::text,
    (ru->'MetaData')::text,
    (ru->'ProgressResult')::text,
    (ru->'MostRecentProgressResult')::text,
    (ru->'RemainingResolvables')::text,
    (ru->'ReferenceLabels')::text,
    (ru->'PreviousProperties')::text
FROM forma_commands fc,
     jsonb_array_elements(fc.data::jsonb->'ResourceUpdates') ru
WHERE fc.data IS NOT NULL
  AND fc.data::jsonb->'ResourceUpdates' IS NOT NULL
  AND jsonb_array_length(fc.data::jsonb->'ResourceUpdates') > 0
ON CONFLICT (command_id, ksuid, operation) DO NOTHING;

-- +goose Down
-- Remove the migrated resource_updates (only those that came from migration)
-- Note: This is a best-effort rollback - new data added after migration won't be affected
DELETE FROM resource_updates WHERE command_id IN (
    SELECT command_id FROM forma_commands WHERE data IS NOT NULL
);

ALTER TABLE forma_commands DROP COLUMN IF EXISTS modified_ts;
ALTER TABLE forma_commands DROP COLUMN IF EXISTS target_updates;
ALTER TABLE forma_commands DROP COLUMN IF EXISTS config_simulate;
ALTER TABLE forma_commands DROP COLUMN IF EXISTS config_force;
ALTER TABLE forma_commands DROP COLUMN IF EXISTS config_mode;
ALTER TABLE forma_commands DROP COLUMN IF EXISTS description_confirm;
ALTER TABLE forma_commands DROP COLUMN IF EXISTS description_text;
