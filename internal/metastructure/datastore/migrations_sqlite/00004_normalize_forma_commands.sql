-- +goose Up
-- Add new columns to forma_commands for normalized storage
-- These columns replace the JSON blob in the 'data' column
ALTER TABLE forma_commands ADD COLUMN description TEXT;
ALTER TABLE forma_commands ADD COLUMN config TEXT;
ALTER TABLE forma_commands ADD COLUMN target_updates TEXT;
ALTER TABLE forma_commands ADD COLUMN modified_ts TEXT;

-- Migrate existing data from the JSON blob to the new columns
-- Extract Description, Config, and TargetUpdates from the data JSON
UPDATE forma_commands
SET
    description = json_extract(data, '$.Description'),
    config = json_extract(data, '$.Config'),
    target_updates = json_extract(data, '$.TargetUpdates'),
    modified_ts = json_extract(data, '$.ModifiedTs')
WHERE data IS NOT NULL;

-- Migrate ResourceUpdates from the JSON blob to the resource_updates table
-- This uses a recursive CTE to iterate through the JSON array
INSERT OR IGNORE INTO resource_updates (
    command_id, ksuid, operation, state, start_ts, modified_ts,
    retries, remaining, version, stack_label, group_id, source,
    resource, resource_target, existing_resource, existing_target,
    metadata, progress_result, most_recent_progress,
    remaining_resolvables, reference_labels, previous_properties
)
SELECT
    fc.command_id,
    json_extract(ru.value, '$.Resource.Ksuid'),
    json_extract(ru.value, '$.Operation'),
    json_extract(ru.value, '$.State'),
    json_extract(ru.value, '$.StartTs'),
    json_extract(ru.value, '$.ModifiedTs'),
    COALESCE(json_extract(ru.value, '$.Retries'), 0),
    COALESCE(json_extract(ru.value, '$.Remaining'), 0),
    json_extract(ru.value, '$.Version'),
    COALESCE(json_extract(ru.value, '$.StackLabel'), json_extract(ru.value, '$.Resource.Stack')),
    json_extract(ru.value, '$.GroupID'),
    json_extract(ru.value, '$.Source'),
    json_extract(ru.value, '$.Resource'),
    json_extract(ru.value, '$.ResourceTarget'),
    json_extract(ru.value, '$.ExistingResource'),
    json_extract(ru.value, '$.ExistingTarget'),
    json_extract(ru.value, '$.MetaData'),
    json_extract(ru.value, '$.ProgressResult'),
    json_extract(ru.value, '$.MostRecentProgressResult'),
    json_extract(ru.value, '$.RemainingResolvables'),
    json_extract(ru.value, '$.ReferenceLabels'),
    json_extract(ru.value, '$.PreviousProperties')
FROM forma_commands fc, json_each(json_extract(fc.data, '$.ResourceUpdates')) ru
WHERE fc.data IS NOT NULL
  AND json_extract(fc.data, '$.ResourceUpdates') IS NOT NULL
  AND json_array_length(json_extract(fc.data, '$.ResourceUpdates')) > 0;

-- +goose Down
-- Remove the migrated resource_updates (only those that came from migration)
-- Note: This is a best-effort rollback - new data added after migration won't be affected
DELETE FROM resource_updates WHERE command_id IN (
    SELECT command_id FROM forma_commands WHERE data IS NOT NULL
);

ALTER TABLE forma_commands DROP COLUMN modified_ts;
ALTER TABLE forma_commands DROP COLUMN target_updates;
ALTER TABLE forma_commands DROP COLUMN config;
ALTER TABLE forma_commands DROP COLUMN description;
