-- © 2026 Platform Engineering Labs Inc.
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- Command progress writes are frequent. Ignore the modified timestamp when it
-- is the only changed column, and materialize the command's affected rows
-- before inspecting JSON or resource-update history.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION admission_forma_commands_update() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
IF (to_jsonb(OLD) - 'modified_ts') = (to_jsonb(NEW) - 'modified_ts') THEN
RETURN NEW;
END IF;

WITH command_images AS MATERIALIZED (
    SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source
    UNION ALL
    SELECT NEW.command_id, NEW.command, NEW.source
),
affected_resource_updates AS MATERIALIZED (
    SELECT u.stack_label
    FROM resource_updates u
    WHERE u.command_id IN (OLD.command_id, NEW.command_id)
),
affected_resources AS MATERIALIZED (
    SELECT h.stack
    FROM resources h
    WHERE h.command_id IN (OLD.command_id, NEW.command_id)
),
affected_labels AS MATERIALIZED (
    SELECT cs.stack_label AS label
    FROM command_stacks cs
    JOIN command_images r ON cs.command_id=r.command_id
    WHERE NOT (COALESCE(r.command,'')='sync' AND COALESCE(r.source,'') IN ('synchronizer','discovery'))
    UNION
    SELECT stack_label FROM affected_resource_updates
    UNION
    SELECT stack FROM affected_resources
)
INSERT INTO admission_stack_labels(label)
SELECT DISTINCT label FROM affected_labels WHERE label IS NOT NULL
ON CONFLICT(label) DO NOTHING;

FOR k IN
WITH command_images AS MATERIALIZED (
    SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source,
           OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates,
           OLD.policy_updates AS policy_updates
    UNION ALL
    SELECT NEW.command_id, NEW.command, NEW.source,
           NEW.target_updates, NEW.stack_updates, NEW.policy_updates
),
affected_resource_updates AS MATERIALIZED (
    SELECT u.command_id, u.ksuid, u.stack_label, u.resource, u.existing_resource,
           u.resource_target, u.existing_target, u.reference_labels,
           u.provenance_records, u.resolved_root_digests, u.remaining_resolvables
    FROM resource_updates u
    WHERE u.command_id IN (OLD.command_id, NEW.command_id)
),
affected_resource_update_ids AS MATERIALIZED (
    SELECT DISTINCT ksuid FROM affected_resource_updates WHERE ksuid IS NOT NULL
),
affected_resource_update_history AS MATERIALIZED (
    SELECT h.ksuid, h.resource, h.existing_resource, h.resource_target,
           h.existing_target, h.reference_labels, h.provenance_records,
           h.resolved_root_digests, h.remaining_resolvables
    FROM resource_updates h
    JOIN affected_resource_update_ids a ON h.ksuid=a.ksuid
),
affected_resources AS MATERIALIZED (
    SELECT h.stack, h.data
    FROM resources h
    WHERE h.command_id IN (OLD.command_id, NEW.command_id)
),
affected_labels AS MATERIALIZED (
    SELECT cs.stack_label AS label
    FROM command_stacks cs
    JOIN command_images r ON cs.command_id=r.command_id
    WHERE NOT (COALESCE(r.command,'')='sync' AND COALESCE(r.source,'') IN ('synchronizer','discovery'))
    UNION
    SELECT stack_label FROM affected_resource_updates
    UNION
    SELECT stack FROM affected_resources
),
keys AS (
    SELECT l.guard_key
    FROM admission_stack_labels l
    JOIN affected_labels x ON l.label=x.label
    UNION
    SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$'
                THEN 'admission:stack:' || RTRIM(s.id)
                ELSE 'admission:stack:exceptional' END AS guard_key
    FROM stacks s
    JOIN affected_labels x ON s.label=x.label
    UNION
    SELECT 'admission:targets' AS guard_key
    WHERE EXISTS (SELECT 1 FROM command_images r WHERE COALESCE(r.target_updates,'') NOT IN ('','null','{}','[]'))
    UNION
    SELECT 'admission:topology' AS guard_key
    WHERE EXISTS (SELECT 1 FROM command_images r WHERE COALESCE(r.target_updates,'') NOT IN ('','null','{}','[]'))
    UNION
    SELECT 'admission:stack-mapping' AS guard_key
    WHERE EXISTS (SELECT 1 FROM command_images r WHERE COALESCE(r.stack_updates,'') NOT IN ('','null','{}','[]'))
    UNION
    SELECT 'admission:policies' AS guard_key
    WHERE EXISTS (SELECT 1 FROM command_images r WHERE COALESCE(r.policy_updates,'') NOT IN ('','null','{}','[]'))
    UNION
    SELECT 'admission:topology' AS guard_key
    WHERE EXISTS (
        SELECT 1 FROM affected_resource_updates u
        WHERE (CAST(u.resource AS TEXT) LIKE '%$ref%' OR CAST(u.resource AS TEXT) LIKE '%$gen%' OR CAST(u.resource AS TEXT) LIKE '%' || chr(92) || '%')
           OR (CAST(u.existing_resource AS TEXT) LIKE '%$ref%' OR CAST(u.existing_resource AS TEXT) LIKE '%$gen%' OR CAST(u.existing_resource AS TEXT) LIKE '%' || chr(92) || '%')
           OR (CAST(u.resource_target AS TEXT) LIKE '%$ref%' OR CAST(u.resource_target AS TEXT) LIKE '%$gen%' OR CAST(u.resource_target AS TEXT) LIKE '%' || chr(92) || '%')
           OR (CAST(u.existing_target AS TEXT) LIKE '%$ref%' OR CAST(u.existing_target AS TEXT) LIKE '%$gen%' OR CAST(u.existing_target AS TEXT) LIKE '%' || chr(92) || '%')
           OR COALESCE(u.reference_labels,'') NOT IN ('','null','{}','[]')
           OR COALESCE(u.provenance_records,'') NOT IN ('','null','{}','[]')
           OR COALESCE(u.resolved_root_digests,'') NOT IN ('','null','{}','[]')
           OR COALESCE(u.remaining_resolvables,'') NOT IN ('','null','{}','[]')
           OR EXISTS (
                SELECT 1 FROM affected_resource_update_history h
                WHERE h.ksuid=u.ksuid AND (
                       (CAST(h.resource AS TEXT) LIKE '%$ref%' OR CAST(h.resource AS TEXT) LIKE '%$gen%' OR CAST(h.resource AS TEXT) LIKE '%' || chr(92) || '%')
                    OR (CAST(h.existing_resource AS TEXT) LIKE '%$ref%' OR CAST(h.existing_resource AS TEXT) LIKE '%$gen%' OR CAST(h.existing_resource AS TEXT) LIKE '%' || chr(92) || '%')
                    OR (CAST(h.resource_target AS TEXT) LIKE '%$ref%' OR CAST(h.resource_target AS TEXT) LIKE '%$gen%' OR CAST(h.resource_target AS TEXT) LIKE '%' || chr(92) || '%')
                    OR (CAST(h.existing_target AS TEXT) LIKE '%$ref%' OR CAST(h.existing_target AS TEXT) LIKE '%$gen%' OR CAST(h.existing_target AS TEXT) LIKE '%' || chr(92) || '%')
                    OR COALESCE(h.reference_labels,'') NOT IN ('','null','{}','[]')
                    OR COALESCE(h.provenance_records,'') NOT IN ('','null','{}','[]')
                    OR COALESCE(h.resolved_root_digests,'') NOT IN ('','null','{}','[]')
                    OR COALESCE(h.remaining_resolvables,'') NOT IN ('','null','{}','[]')
                )
           )
    )
    UNION
    SELECT 'admission:topology' AS guard_key
    WHERE EXISTS (
        SELECT 1 FROM affected_resources h
        WHERE CAST(h.data AS TEXT) LIKE '%$ref%'
           OR CAST(h.data AS TEXT) LIKE '%$gen%'
           OR CAST(h.data AS TEXT) LIKE '%' || chr(92) || '%'
    )
)
SELECT guard_key FROM keys ORDER BY guard_key COLLATE "C"
LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1)
ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose Down
-- Restored below to the command UPDATE function shipped by migration 00031.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION admission_forma_commands_update() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT cs.stack_label AS label FROM command_stacks cs JOIN (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates UNION ALL SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r ON cs.command_id=r.command_id WHERE NOT (COALESCE(r.command,'')='sync' AND COALESCE(r.source,'') IN ('synchronizer','discovery')) UNION SELECT ru.stack_label FROM resource_updates ru JOIN (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates UNION ALL SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r ON ru.command_id=r.command_id UNION SELECT h.stack FROM resources h JOIN (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates UNION ALL SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r ON h.command_id=r.command_id) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT cs.stack_label AS label FROM command_stacks cs JOIN (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates UNION ALL SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r ON cs.command_id=r.command_id WHERE NOT (COALESCE(r.command,'')='sync' AND COALESCE(r.source,'') IN ('synchronizer','discovery')) UNION SELECT ru.stack_label FROM resource_updates ru JOIN (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates UNION ALL SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r ON ru.command_id=r.command_id UNION SELECT h.stack FROM resources h JOIN (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates UNION ALL SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r ON h.command_id=r.command_id) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT cs.stack_label AS label FROM command_stacks cs JOIN (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates UNION ALL SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r ON cs.command_id=r.command_id WHERE NOT (COALESCE(r.command,'')='sync' AND COALESCE(r.source,'') IN ('synchronizer','discovery')) UNION SELECT ru.stack_label FROM resource_updates ru JOIN (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates UNION ALL SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r ON ru.command_id=r.command_id UNION SELECT h.stack FROM resources h JOIN (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates UNION ALL SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r ON h.command_id=r.command_id) x ON s.label=x.label
UNION
SELECT 'admission:targets' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates UNION ALL SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r WHERE COALESCE(r.target_updates,'') NOT IN ('','null','{}','[]'))
UNION
SELECT 'admission:topology' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates UNION ALL SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r WHERE COALESCE(r.target_updates,'') NOT IN ('','null','{}','[]'))
UNION
SELECT 'admission:stack-mapping' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates UNION ALL SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r WHERE COALESCE(r.stack_updates,'') NOT IN ('','null','{}','[]'))
UNION
SELECT 'admission:policies' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates UNION ALL SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r WHERE COALESCE(r.policy_updates,'') NOT IN ('','null','{}','[]'))
UNION
SELECT 'admission:topology' AS guard_key WHERE EXISTS (SELECT 1 FROM resource_updates u JOIN (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates UNION ALL SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r ON u.command_id=r.command_id WHERE (CAST(u.resource AS TEXT) LIKE '%$ref%' OR CAST(u.resource AS TEXT) LIKE '%$gen%' OR CAST(u.resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(u.existing_resource AS TEXT) LIKE '%$ref%' OR CAST(u.existing_resource AS TEXT) LIKE '%$gen%' OR CAST(u.existing_resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(u.resource_target AS TEXT) LIKE '%$ref%' OR CAST(u.resource_target AS TEXT) LIKE '%$gen%' OR CAST(u.resource_target AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(u.existing_target AS TEXT) LIKE '%$ref%' OR CAST(u.existing_target AS TEXT) LIKE '%$gen%' OR CAST(u.existing_target AS TEXT) LIKE '%' || chr(92) || '%') OR COALESCE(u.reference_labels,'') NOT IN ('','null','{}','[]') OR COALESCE(u.provenance_records,'') NOT IN ('','null','{}','[]') OR COALESCE(u.resolved_root_digests,'') NOT IN ('','null','{}','[]') OR COALESCE(u.remaining_resolvables,'') NOT IN ('','null','{}','[]') OR EXISTS (SELECT 1 FROM resource_updates h WHERE h.ksuid=u.ksuid AND ((CAST(h.resource AS TEXT) LIKE '%$ref%' OR CAST(h.resource AS TEXT) LIKE '%$gen%' OR CAST(h.resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(h.existing_resource AS TEXT) LIKE '%$ref%' OR CAST(h.existing_resource AS TEXT) LIKE '%$gen%' OR CAST(h.existing_resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(h.resource_target AS TEXT) LIKE '%$ref%' OR CAST(h.resource_target AS TEXT) LIKE '%$gen%' OR CAST(h.resource_target AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(h.existing_target AS TEXT) LIKE '%$ref%' OR CAST(h.existing_target AS TEXT) LIKE '%$gen%' OR CAST(h.existing_target AS TEXT) LIKE '%' || chr(92) || '%') OR COALESCE(h.reference_labels,'') NOT IN ('','null','{}','[]') OR COALESCE(h.provenance_records,'') NOT IN ('','null','{}','[]') OR COALESCE(h.resolved_root_digests,'') NOT IN ('','null','{}','[]') OR COALESCE(h.remaining_resolvables,'') NOT IN ('','null','{}','[]'))))
UNION
SELECT 'admission:topology' AS guard_key WHERE EXISTS (SELECT 1 FROM resources h JOIN (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates UNION ALL SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r ON h.command_id=r.command_id WHERE (CAST(h.data AS TEXT) LIKE '%$ref%' OR CAST(h.data AS TEXT) LIKE '%$gen%' OR CAST(h.data AS TEXT) LIKE '%' || chr(92) || '%'))) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN NEW;
END $$;
-- +goose StatementEnd
