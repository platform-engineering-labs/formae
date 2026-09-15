-- © 2026 Platform Engineering Labs Inc.
-- SPDX-License-Identifier: FSL-1.1-ALv2
-- +goose Up
-- Persistent label identities intentionally use the same collation as stacks.label.
-- Never delete these identities or reset revisions during ordinary writes.
-- Stack ID grammar matches AdmissionStackGuardKey; unusual IDs share a bucket.
-- URI identities match resources.uri collation and serialize logical-version
-- mutations before discovering historical row/JSON stack scopes. Their guard
-- rows are internal writer mutexes; planner guard requirements are unchanged.
-- PostgreSQL resource functions are VOLATILE for fresh post-lock query snapshots;
-- SQL Server uses committed locking history reads after acquiring URI mutexes.
-- SQLite includes replaced rows by their actual PK without recursive_triggers.
-- Reference/escape-bearing history conservatively invalidates topology; unrelated
-- reference-free resource URIs remain independent.
CREATE TABLE admission_stack_labels (label TEXT NOT NULL PRIMARY KEY, guard_key TEXT COLLATE "C" NOT NULL UNIQUE DEFAULT ('admission:label:' || gen_random_uuid()::text));
CREATE TABLE admission_resource_uris (uri TEXT NOT NULL PRIMARY KEY, guard_key TEXT COLLATE "C" NOT NULL UNIQUE DEFAULT ('admission:uri:' || gen_random_uuid()::text));
-- +goose StatementBegin
CREATE FUNCTION admission_resources_insert() RETURNS trigger LANGUAGE plpgsql VOLATILE AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_resource_uris(uri) SELECT DISTINCT uri FROM (SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) affected ON CONFLICT(uri) DO NOTHING;
FOR k IN SELECT u.guard_key FROM admission_resource_uris u JOIN (SELECT DISTINCT uri FROM (SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) affected) a ON a.uri=u.uri ORDER BY u.guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT r.stack AS label FROM (SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r UNION SELECT (r.data::jsonb ->> 'Stack') FROM (SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r UNION SELECT h.stack FROM resources h JOIN (SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r ON h.uri=r.uri UNION SELECT (h.data::jsonb ->> 'Stack') FROM resources h JOIN (SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r ON h.uri=r.uri) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT r.stack AS label FROM (SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r UNION SELECT (r.data::jsonb ->> 'Stack') FROM (SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r UNION SELECT h.stack FROM resources h JOIN (SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r ON h.uri=r.uri UNION SELECT (h.data::jsonb ->> 'Stack') FROM resources h JOIN (SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r ON h.uri=r.uri) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT r.stack AS label FROM (SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r UNION SELECT (r.data::jsonb ->> 'Stack') FROM (SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r UNION SELECT h.stack FROM resources h JOIN (SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r ON h.uri=r.uri UNION SELECT (h.data::jsonb ->> 'Stack') FROM resources h JOIN (SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r ON h.uri=r.uri) x ON s.label=x.label
UNION
SELECT 'admission:topology' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r WHERE (CAST(r.data AS TEXT) LIKE '%$ref%' OR CAST(r.data AS TEXT) LIKE '%$gen%' OR CAST(r.data AS TEXT) LIKE '%' || chr(92) || '%') OR cardinality(r.refs)>0 OR EXISTS (SELECT 1 FROM resources h WHERE h.uri=r.uri AND ((CAST(h.data AS TEXT) LIKE '%$ref%' OR CAST(h.data AS TEXT) LIKE '%$gen%' OR CAST(h.data AS TEXT) LIKE '%' || chr(92) || '%') OR cardinality(h.refs)>0)))) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_resources_insert BEFORE INSERT ON resources FOR EACH ROW EXECUTE FUNCTION admission_resources_insert();
-- +goose StatementBegin
CREATE FUNCTION admission_resources_update() RETURNS trigger LANGUAGE plpgsql VOLATILE AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_resource_uris(uri) SELECT DISTINCT uri FROM (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs UNION ALL SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) affected ON CONFLICT(uri) DO NOTHING;
FOR k IN SELECT u.guard_key FROM admission_resource_uris u JOIN (SELECT DISTINCT uri FROM (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs UNION ALL SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) affected) a ON a.uri=u.uri ORDER BY u.guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT r.stack AS label FROM (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs UNION ALL SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r UNION SELECT (r.data::jsonb ->> 'Stack') FROM (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs UNION ALL SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r UNION SELECT h.stack FROM resources h JOIN (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs UNION ALL SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r ON h.uri=r.uri UNION SELECT (h.data::jsonb ->> 'Stack') FROM resources h JOIN (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs UNION ALL SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r ON h.uri=r.uri) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT r.stack AS label FROM (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs UNION ALL SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r UNION SELECT (r.data::jsonb ->> 'Stack') FROM (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs UNION ALL SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r UNION SELECT h.stack FROM resources h JOIN (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs UNION ALL SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r ON h.uri=r.uri UNION SELECT (h.data::jsonb ->> 'Stack') FROM resources h JOIN (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs UNION ALL SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r ON h.uri=r.uri) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT r.stack AS label FROM (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs UNION ALL SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r UNION SELECT (r.data::jsonb ->> 'Stack') FROM (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs UNION ALL SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r UNION SELECT h.stack FROM resources h JOIN (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs UNION ALL SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r ON h.uri=r.uri UNION SELECT (h.data::jsonb ->> 'Stack') FROM resources h JOIN (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs UNION ALL SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r ON h.uri=r.uri) x ON s.label=x.label
UNION
SELECT 'admission:topology' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs UNION ALL SELECT NEW.stack AS stack, NEW.data AS data, NEW.uri AS uri, NEW.refs AS refs) r WHERE (CAST(r.data AS TEXT) LIKE '%$ref%' OR CAST(r.data AS TEXT) LIKE '%$gen%' OR CAST(r.data AS TEXT) LIKE '%' || chr(92) || '%') OR cardinality(r.refs)>0 OR EXISTS (SELECT 1 FROM resources h WHERE h.uri=r.uri AND ((CAST(h.data AS TEXT) LIKE '%$ref%' OR CAST(h.data AS TEXT) LIKE '%$gen%' OR CAST(h.data AS TEXT) LIKE '%' || chr(92) || '%') OR cardinality(h.refs)>0)))) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_resources_update BEFORE UPDATE ON resources FOR EACH ROW EXECUTE FUNCTION admission_resources_update();
-- +goose StatementBegin
CREATE FUNCTION admission_resources_delete() RETURNS trigger LANGUAGE plpgsql VOLATILE AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_resource_uris(uri) SELECT DISTINCT uri FROM (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs) affected ON CONFLICT(uri) DO NOTHING;
FOR k IN SELECT u.guard_key FROM admission_resource_uris u JOIN (SELECT DISTINCT uri FROM (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs) affected) a ON a.uri=u.uri ORDER BY u.guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT r.stack AS label FROM (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs) r UNION SELECT (r.data::jsonb ->> 'Stack') FROM (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs) r UNION SELECT h.stack FROM resources h JOIN (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs) r ON h.uri=r.uri UNION SELECT (h.data::jsonb ->> 'Stack') FROM resources h JOIN (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs) r ON h.uri=r.uri) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT r.stack AS label FROM (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs) r UNION SELECT (r.data::jsonb ->> 'Stack') FROM (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs) r UNION SELECT h.stack FROM resources h JOIN (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs) r ON h.uri=r.uri UNION SELECT (h.data::jsonb ->> 'Stack') FROM resources h JOIN (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs) r ON h.uri=r.uri) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT r.stack AS label FROM (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs) r UNION SELECT (r.data::jsonb ->> 'Stack') FROM (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs) r UNION SELECT h.stack FROM resources h JOIN (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs) r ON h.uri=r.uri UNION SELECT (h.data::jsonb ->> 'Stack') FROM resources h JOIN (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs) r ON h.uri=r.uri) x ON s.label=x.label
UNION
SELECT 'admission:topology' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT OLD.stack AS stack, OLD.data AS data, OLD.uri AS uri, OLD.refs AS refs) r WHERE (CAST(r.data AS TEXT) LIKE '%$ref%' OR CAST(r.data AS TEXT) LIKE '%$gen%' OR CAST(r.data AS TEXT) LIKE '%' || chr(92) || '%') OR cardinality(r.refs)>0 OR EXISTS (SELECT 1 FROM resources h WHERE h.uri=r.uri AND ((CAST(h.data AS TEXT) LIKE '%$ref%' OR CAST(h.data AS TEXT) LIKE '%$gen%' OR CAST(h.data AS TEXT) LIKE '%' || chr(92) || '%') OR cardinality(h.refs)>0)))) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN OLD;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_resources_delete BEFORE DELETE ON resources FOR EACH ROW EXECUTE FUNCTION admission_resources_delete();
-- +goose StatementBegin
CREATE FUNCTION admission_stacks_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT r.label FROM (SELECT NEW.id AS id, NEW.label AS label) r) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT r.label FROM (SELECT NEW.id AS id, NEW.label AS label) r) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT r.label FROM (SELECT NEW.id AS id, NEW.label AS label) r) x ON s.label=x.label
UNION
SELECT 'admission:stack-mapping' AS guard_key
UNION
SELECT CASE WHEN RTRIM(r.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(r.id) ELSE 'admission:stack:exceptional' END FROM (SELECT NEW.id AS id, NEW.label AS label) r) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_stacks_insert BEFORE INSERT ON stacks FOR EACH ROW EXECUTE FUNCTION admission_stacks_insert();
-- +goose StatementBegin
CREATE FUNCTION admission_stacks_update() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT r.label FROM (SELECT OLD.id AS id, OLD.label AS label UNION ALL SELECT NEW.id AS id, NEW.label AS label) r) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT r.label FROM (SELECT OLD.id AS id, OLD.label AS label UNION ALL SELECT NEW.id AS id, NEW.label AS label) r) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT r.label FROM (SELECT OLD.id AS id, OLD.label AS label UNION ALL SELECT NEW.id AS id, NEW.label AS label) r) x ON s.label=x.label
UNION
SELECT 'admission:stack-mapping' AS guard_key
UNION
SELECT CASE WHEN RTRIM(r.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(r.id) ELSE 'admission:stack:exceptional' END FROM (SELECT OLD.id AS id, OLD.label AS label UNION ALL SELECT NEW.id AS id, NEW.label AS label) r) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_stacks_update BEFORE UPDATE ON stacks FOR EACH ROW EXECUTE FUNCTION admission_stacks_update();
-- +goose StatementBegin
CREATE FUNCTION admission_stacks_delete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT r.label FROM (SELECT OLD.id AS id, OLD.label AS label) r) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT r.label FROM (SELECT OLD.id AS id, OLD.label AS label) r) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT r.label FROM (SELECT OLD.id AS id, OLD.label AS label) r) x ON s.label=x.label
UNION
SELECT 'admission:stack-mapping' AS guard_key
UNION
SELECT CASE WHEN RTRIM(r.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(r.id) ELSE 'admission:stack:exceptional' END FROM (SELECT OLD.id AS id, OLD.label AS label) r) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN OLD;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_stacks_delete BEFORE DELETE ON stacks FOR EACH ROW EXECUTE FUNCTION admission_stacks_delete();
-- +goose StatementBegin
CREATE FUNCTION admission_targets_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT NULL AS label WHERE 1=0) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT NULL AS label WHERE 1=0) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT NULL AS label WHERE 1=0) x ON s.label=x.label
UNION
SELECT 'admission:targets' AS guard_key
UNION
SELECT 'admission:topology' AS guard_key) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_targets_insert BEFORE INSERT ON targets FOR EACH ROW EXECUTE FUNCTION admission_targets_insert();
-- +goose StatementBegin
CREATE FUNCTION admission_targets_update() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
IF OLD.label IS NOT DISTINCT FROM NEW.label AND OLD.version IS NOT DISTINCT FROM NEW.version AND OLD.namespace IS NOT DISTINCT FROM NEW.namespace AND OLD.config IS NOT DISTINCT FROM NEW.config AND OLD.config_schema IS NOT DISTINCT FROM NEW.config_schema AND OLD.discoverable IS NOT DISTINCT FROM NEW.discoverable AND OLD.target_incarnation_id IS NOT DISTINCT FROM NEW.target_incarnation_id AND OLD.reap_kind IS NOT DISTINCT FROM NEW.reap_kind AND OLD.reap_max_unreachable_seconds IS NOT DISTINCT FROM NEW.reap_max_unreachable_seconds AND (OLD.health_state='reaped') IS NOT DISTINCT FROM (NEW.health_state='reaped') THEN RETURN NEW; END IF;
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT NULL AS label WHERE 1=0) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT NULL AS label WHERE 1=0) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT NULL AS label WHERE 1=0) x ON s.label=x.label
UNION
SELECT 'admission:targets' AS guard_key
UNION
SELECT 'admission:topology' AS guard_key) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_targets_update BEFORE UPDATE ON targets FOR EACH ROW EXECUTE FUNCTION admission_targets_update();
-- +goose StatementBegin
CREATE FUNCTION admission_targets_delete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT NULL AS label WHERE 1=0) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT NULL AS label WHERE 1=0) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT NULL AS label WHERE 1=0) x ON s.label=x.label
UNION
SELECT 'admission:targets' AS guard_key
UNION
SELECT 'admission:topology' AS guard_key) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN OLD;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_targets_delete BEFORE DELETE ON targets FOR EACH ROW EXECUTE FUNCTION admission_targets_delete();
-- +goose StatementBegin
CREATE FUNCTION admission_policies_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT s.label FROM stacks s JOIN (SELECT NEW.stack_id AS stack_id) r ON s.id=r.stack_id) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT s.label FROM stacks s JOIN (SELECT NEW.stack_id AS stack_id) r ON s.id=r.stack_id) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT s.label FROM stacks s JOIN (SELECT NEW.stack_id AS stack_id) r ON s.id=r.stack_id) x ON s.label=x.label
UNION
SELECT CASE WHEN RTRIM(r.stack_id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(r.stack_id) ELSE 'admission:stack:exceptional' END FROM (SELECT NEW.stack_id AS stack_id) r WHERE r.stack_id IS NOT NULL
UNION
SELECT 'admission:policies' AS guard_key) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_policies_insert BEFORE INSERT ON policies FOR EACH ROW EXECUTE FUNCTION admission_policies_insert();
-- +goose StatementBegin
CREATE FUNCTION admission_policies_update() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT s.label FROM stacks s JOIN (SELECT OLD.stack_id AS stack_id UNION ALL SELECT NEW.stack_id AS stack_id) r ON s.id=r.stack_id) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT s.label FROM stacks s JOIN (SELECT OLD.stack_id AS stack_id UNION ALL SELECT NEW.stack_id AS stack_id) r ON s.id=r.stack_id) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT s.label FROM stacks s JOIN (SELECT OLD.stack_id AS stack_id UNION ALL SELECT NEW.stack_id AS stack_id) r ON s.id=r.stack_id) x ON s.label=x.label
UNION
SELECT CASE WHEN RTRIM(r.stack_id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(r.stack_id) ELSE 'admission:stack:exceptional' END FROM (SELECT OLD.stack_id AS stack_id UNION ALL SELECT NEW.stack_id AS stack_id) r WHERE r.stack_id IS NOT NULL
UNION
SELECT 'admission:policies' AS guard_key) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_policies_update BEFORE UPDATE ON policies FOR EACH ROW EXECUTE FUNCTION admission_policies_update();
-- +goose StatementBegin
CREATE FUNCTION admission_policies_delete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT s.label FROM stacks s JOIN (SELECT OLD.stack_id AS stack_id) r ON s.id=r.stack_id) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT s.label FROM stacks s JOIN (SELECT OLD.stack_id AS stack_id) r ON s.id=r.stack_id) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT s.label FROM stacks s JOIN (SELECT OLD.stack_id AS stack_id) r ON s.id=r.stack_id) x ON s.label=x.label
UNION
SELECT CASE WHEN RTRIM(r.stack_id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(r.stack_id) ELSE 'admission:stack:exceptional' END FROM (SELECT OLD.stack_id AS stack_id) r WHERE r.stack_id IS NOT NULL
UNION
SELECT 'admission:policies' AS guard_key) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN OLD;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_policies_delete BEFORE DELETE ON policies FOR EACH ROW EXECUTE FUNCTION admission_policies_delete();
-- +goose StatementBegin
CREATE FUNCTION admission_stack_policies_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT s.label FROM stacks s JOIN (SELECT NEW.stack_id AS stack_id) r ON s.id=r.stack_id) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT s.label FROM stacks s JOIN (SELECT NEW.stack_id AS stack_id) r ON s.id=r.stack_id) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT s.label FROM stacks s JOIN (SELECT NEW.stack_id AS stack_id) r ON s.id=r.stack_id) x ON s.label=x.label
UNION
SELECT CASE WHEN RTRIM(r.stack_id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(r.stack_id) ELSE 'admission:stack:exceptional' END FROM (SELECT NEW.stack_id AS stack_id) r WHERE r.stack_id IS NOT NULL
UNION
SELECT 'admission:policies' AS guard_key) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_stack_policies_insert BEFORE INSERT ON stack_policies FOR EACH ROW EXECUTE FUNCTION admission_stack_policies_insert();
-- +goose StatementBegin
CREATE FUNCTION admission_stack_policies_update() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT s.label FROM stacks s JOIN (SELECT OLD.stack_id AS stack_id UNION ALL SELECT NEW.stack_id AS stack_id) r ON s.id=r.stack_id) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT s.label FROM stacks s JOIN (SELECT OLD.stack_id AS stack_id UNION ALL SELECT NEW.stack_id AS stack_id) r ON s.id=r.stack_id) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT s.label FROM stacks s JOIN (SELECT OLD.stack_id AS stack_id UNION ALL SELECT NEW.stack_id AS stack_id) r ON s.id=r.stack_id) x ON s.label=x.label
UNION
SELECT CASE WHEN RTRIM(r.stack_id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(r.stack_id) ELSE 'admission:stack:exceptional' END FROM (SELECT OLD.stack_id AS stack_id UNION ALL SELECT NEW.stack_id AS stack_id) r WHERE r.stack_id IS NOT NULL
UNION
SELECT 'admission:policies' AS guard_key) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_stack_policies_update BEFORE UPDATE ON stack_policies FOR EACH ROW EXECUTE FUNCTION admission_stack_policies_update();
-- +goose StatementBegin
CREATE FUNCTION admission_stack_policies_delete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT s.label FROM stacks s JOIN (SELECT OLD.stack_id AS stack_id) r ON s.id=r.stack_id) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT s.label FROM stacks s JOIN (SELECT OLD.stack_id AS stack_id) r ON s.id=r.stack_id) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT s.label FROM stacks s JOIN (SELECT OLD.stack_id AS stack_id) r ON s.id=r.stack_id) x ON s.label=x.label
UNION
SELECT CASE WHEN RTRIM(r.stack_id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(r.stack_id) ELSE 'admission:stack:exceptional' END FROM (SELECT OLD.stack_id AS stack_id) r WHERE r.stack_id IS NOT NULL
UNION
SELECT 'admission:policies' AS guard_key) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN OLD;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_stack_policies_delete BEFORE DELETE ON stack_policies FOR EACH ROW EXECUTE FUNCTION admission_stack_policies_delete();
-- +goose StatementBegin
CREATE FUNCTION admission_generators_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT s.label FROM stacks s JOIN (SELECT NEW.stack_id AS stack_id) r ON s.id=r.stack_id) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT s.label FROM stacks s JOIN (SELECT NEW.stack_id AS stack_id) r ON s.id=r.stack_id) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT s.label FROM stacks s JOIN (SELECT NEW.stack_id AS stack_id) r ON s.id=r.stack_id) x ON s.label=x.label
UNION
SELECT CASE WHEN RTRIM(r.stack_id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(r.stack_id) ELSE 'admission:stack:exceptional' END FROM (SELECT NEW.stack_id AS stack_id) r WHERE r.stack_id IS NOT NULL
UNION
SELECT 'admission:generators' AS guard_key
UNION
SELECT 'admission:topology' AS guard_key) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_generators_insert BEFORE INSERT ON generators FOR EACH ROW EXECUTE FUNCTION admission_generators_insert();
-- +goose StatementBegin
CREATE FUNCTION admission_generators_update() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT s.label FROM stacks s JOIN (SELECT OLD.stack_id AS stack_id UNION ALL SELECT NEW.stack_id AS stack_id) r ON s.id=r.stack_id) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT s.label FROM stacks s JOIN (SELECT OLD.stack_id AS stack_id UNION ALL SELECT NEW.stack_id AS stack_id) r ON s.id=r.stack_id) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT s.label FROM stacks s JOIN (SELECT OLD.stack_id AS stack_id UNION ALL SELECT NEW.stack_id AS stack_id) r ON s.id=r.stack_id) x ON s.label=x.label
UNION
SELECT CASE WHEN RTRIM(r.stack_id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(r.stack_id) ELSE 'admission:stack:exceptional' END FROM (SELECT OLD.stack_id AS stack_id UNION ALL SELECT NEW.stack_id AS stack_id) r WHERE r.stack_id IS NOT NULL
UNION
SELECT 'admission:generators' AS guard_key
UNION
SELECT 'admission:topology' AS guard_key) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_generators_update BEFORE UPDATE ON generators FOR EACH ROW EXECUTE FUNCTION admission_generators_update();
-- +goose StatementBegin
CREATE FUNCTION admission_generators_delete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT s.label FROM stacks s JOIN (SELECT OLD.stack_id AS stack_id) r ON s.id=r.stack_id) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT s.label FROM stacks s JOIN (SELECT OLD.stack_id AS stack_id) r ON s.id=r.stack_id) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT s.label FROM stacks s JOIN (SELECT OLD.stack_id AS stack_id) r ON s.id=r.stack_id) x ON s.label=x.label
UNION
SELECT CASE WHEN RTRIM(r.stack_id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(r.stack_id) ELSE 'admission:stack:exceptional' END FROM (SELECT OLD.stack_id AS stack_id) r WHERE r.stack_id IS NOT NULL
UNION
SELECT 'admission:generators' AS guard_key
UNION
SELECT 'admission:topology' AS guard_key) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN OLD;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_generators_delete BEFORE DELETE ON generators FOR EACH ROW EXECUTE FUNCTION admission_generators_delete();
-- +goose StatementBegin
CREATE FUNCTION admission_resource_updates_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT r.stack_label AS label FROM (SELECT NEW.stack_label AS stack_label, NEW.ksuid AS ksuid, NEW.resource AS resource, NEW.existing_resource AS existing_resource, NEW.resource_target AS resource_target, NEW.existing_target AS existing_target, NEW.reference_labels AS reference_labels, NEW.provenance_records AS provenance_records, NEW.resolved_root_digests AS resolved_root_digests, NEW.remaining_resolvables AS remaining_resolvables) r) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT r.stack_label AS label FROM (SELECT NEW.stack_label AS stack_label, NEW.ksuid AS ksuid, NEW.resource AS resource, NEW.existing_resource AS existing_resource, NEW.resource_target AS resource_target, NEW.existing_target AS existing_target, NEW.reference_labels AS reference_labels, NEW.provenance_records AS provenance_records, NEW.resolved_root_digests AS resolved_root_digests, NEW.remaining_resolvables AS remaining_resolvables) r) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT r.stack_label AS label FROM (SELECT NEW.stack_label AS stack_label, NEW.ksuid AS ksuid, NEW.resource AS resource, NEW.existing_resource AS existing_resource, NEW.resource_target AS resource_target, NEW.existing_target AS existing_target, NEW.reference_labels AS reference_labels, NEW.provenance_records AS provenance_records, NEW.resolved_root_digests AS resolved_root_digests, NEW.remaining_resolvables AS remaining_resolvables) r) x ON s.label=x.label
UNION
SELECT 'admission:topology' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT NEW.stack_label AS stack_label, NEW.ksuid AS ksuid, NEW.resource AS resource, NEW.existing_resource AS existing_resource, NEW.resource_target AS resource_target, NEW.existing_target AS existing_target, NEW.reference_labels AS reference_labels, NEW.provenance_records AS provenance_records, NEW.resolved_root_digests AS resolved_root_digests, NEW.remaining_resolvables AS remaining_resolvables) r WHERE (CAST(r.resource AS TEXT) LIKE '%$ref%' OR CAST(r.resource AS TEXT) LIKE '%$gen%' OR CAST(r.resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(r.existing_resource AS TEXT) LIKE '%$ref%' OR CAST(r.existing_resource AS TEXT) LIKE '%$gen%' OR CAST(r.existing_resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(r.resource_target AS TEXT) LIKE '%$ref%' OR CAST(r.resource_target AS TEXT) LIKE '%$gen%' OR CAST(r.resource_target AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(r.existing_target AS TEXT) LIKE '%$ref%' OR CAST(r.existing_target AS TEXT) LIKE '%$gen%' OR CAST(r.existing_target AS TEXT) LIKE '%' || chr(92) || '%') OR COALESCE(r.reference_labels,'') NOT IN ('','null','{}','[]') OR COALESCE(r.provenance_records,'') NOT IN ('','null','{}','[]') OR COALESCE(r.resolved_root_digests,'') NOT IN ('','null','{}','[]') OR COALESCE(r.remaining_resolvables,'') NOT IN ('','null','{}','[]') OR EXISTS (SELECT 1 FROM resource_updates h WHERE h.ksuid=r.ksuid AND ((CAST(h.resource AS TEXT) LIKE '%$ref%' OR CAST(h.resource AS TEXT) LIKE '%$gen%' OR CAST(h.resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(h.existing_resource AS TEXT) LIKE '%$ref%' OR CAST(h.existing_resource AS TEXT) LIKE '%$gen%' OR CAST(h.existing_resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(h.resource_target AS TEXT) LIKE '%$ref%' OR CAST(h.resource_target AS TEXT) LIKE '%$gen%' OR CAST(h.resource_target AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(h.existing_target AS TEXT) LIKE '%$ref%' OR CAST(h.existing_target AS TEXT) LIKE '%$gen%' OR CAST(h.existing_target AS TEXT) LIKE '%' || chr(92) || '%') OR COALESCE(h.reference_labels,'') NOT IN ('','null','{}','[]') OR COALESCE(h.provenance_records,'') NOT IN ('','null','{}','[]') OR COALESCE(h.resolved_root_digests,'') NOT IN ('','null','{}','[]') OR COALESCE(h.remaining_resolvables,'') NOT IN ('','null','{}','[]'))))) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_resource_updates_insert BEFORE INSERT ON resource_updates FOR EACH ROW EXECUTE FUNCTION admission_resource_updates_insert();
-- +goose StatementBegin
CREATE FUNCTION admission_resource_updates_update() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT r.stack_label AS label FROM (SELECT OLD.stack_label AS stack_label, OLD.ksuid AS ksuid, OLD.resource AS resource, OLD.existing_resource AS existing_resource, OLD.resource_target AS resource_target, OLD.existing_target AS existing_target, OLD.reference_labels AS reference_labels, OLD.provenance_records AS provenance_records, OLD.resolved_root_digests AS resolved_root_digests, OLD.remaining_resolvables AS remaining_resolvables UNION ALL SELECT NEW.stack_label AS stack_label, NEW.ksuid AS ksuid, NEW.resource AS resource, NEW.existing_resource AS existing_resource, NEW.resource_target AS resource_target, NEW.existing_target AS existing_target, NEW.reference_labels AS reference_labels, NEW.provenance_records AS provenance_records, NEW.resolved_root_digests AS resolved_root_digests, NEW.remaining_resolvables AS remaining_resolvables) r) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT r.stack_label AS label FROM (SELECT OLD.stack_label AS stack_label, OLD.ksuid AS ksuid, OLD.resource AS resource, OLD.existing_resource AS existing_resource, OLD.resource_target AS resource_target, OLD.existing_target AS existing_target, OLD.reference_labels AS reference_labels, OLD.provenance_records AS provenance_records, OLD.resolved_root_digests AS resolved_root_digests, OLD.remaining_resolvables AS remaining_resolvables UNION ALL SELECT NEW.stack_label AS stack_label, NEW.ksuid AS ksuid, NEW.resource AS resource, NEW.existing_resource AS existing_resource, NEW.resource_target AS resource_target, NEW.existing_target AS existing_target, NEW.reference_labels AS reference_labels, NEW.provenance_records AS provenance_records, NEW.resolved_root_digests AS resolved_root_digests, NEW.remaining_resolvables AS remaining_resolvables) r) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT r.stack_label AS label FROM (SELECT OLD.stack_label AS stack_label, OLD.ksuid AS ksuid, OLD.resource AS resource, OLD.existing_resource AS existing_resource, OLD.resource_target AS resource_target, OLD.existing_target AS existing_target, OLD.reference_labels AS reference_labels, OLD.provenance_records AS provenance_records, OLD.resolved_root_digests AS resolved_root_digests, OLD.remaining_resolvables AS remaining_resolvables UNION ALL SELECT NEW.stack_label AS stack_label, NEW.ksuid AS ksuid, NEW.resource AS resource, NEW.existing_resource AS existing_resource, NEW.resource_target AS resource_target, NEW.existing_target AS existing_target, NEW.reference_labels AS reference_labels, NEW.provenance_records AS provenance_records, NEW.resolved_root_digests AS resolved_root_digests, NEW.remaining_resolvables AS remaining_resolvables) r) x ON s.label=x.label
UNION
SELECT 'admission:topology' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT OLD.stack_label AS stack_label, OLD.ksuid AS ksuid, OLD.resource AS resource, OLD.existing_resource AS existing_resource, OLD.resource_target AS resource_target, OLD.existing_target AS existing_target, OLD.reference_labels AS reference_labels, OLD.provenance_records AS provenance_records, OLD.resolved_root_digests AS resolved_root_digests, OLD.remaining_resolvables AS remaining_resolvables UNION ALL SELECT NEW.stack_label AS stack_label, NEW.ksuid AS ksuid, NEW.resource AS resource, NEW.existing_resource AS existing_resource, NEW.resource_target AS resource_target, NEW.existing_target AS existing_target, NEW.reference_labels AS reference_labels, NEW.provenance_records AS provenance_records, NEW.resolved_root_digests AS resolved_root_digests, NEW.remaining_resolvables AS remaining_resolvables) r WHERE (CAST(r.resource AS TEXT) LIKE '%$ref%' OR CAST(r.resource AS TEXT) LIKE '%$gen%' OR CAST(r.resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(r.existing_resource AS TEXT) LIKE '%$ref%' OR CAST(r.existing_resource AS TEXT) LIKE '%$gen%' OR CAST(r.existing_resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(r.resource_target AS TEXT) LIKE '%$ref%' OR CAST(r.resource_target AS TEXT) LIKE '%$gen%' OR CAST(r.resource_target AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(r.existing_target AS TEXT) LIKE '%$ref%' OR CAST(r.existing_target AS TEXT) LIKE '%$gen%' OR CAST(r.existing_target AS TEXT) LIKE '%' || chr(92) || '%') OR COALESCE(r.reference_labels,'') NOT IN ('','null','{}','[]') OR COALESCE(r.provenance_records,'') NOT IN ('','null','{}','[]') OR COALESCE(r.resolved_root_digests,'') NOT IN ('','null','{}','[]') OR COALESCE(r.remaining_resolvables,'') NOT IN ('','null','{}','[]') OR EXISTS (SELECT 1 FROM resource_updates h WHERE h.ksuid=r.ksuid AND ((CAST(h.resource AS TEXT) LIKE '%$ref%' OR CAST(h.resource AS TEXT) LIKE '%$gen%' OR CAST(h.resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(h.existing_resource AS TEXT) LIKE '%$ref%' OR CAST(h.existing_resource AS TEXT) LIKE '%$gen%' OR CAST(h.existing_resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(h.resource_target AS TEXT) LIKE '%$ref%' OR CAST(h.resource_target AS TEXT) LIKE '%$gen%' OR CAST(h.resource_target AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(h.existing_target AS TEXT) LIKE '%$ref%' OR CAST(h.existing_target AS TEXT) LIKE '%$gen%' OR CAST(h.existing_target AS TEXT) LIKE '%' || chr(92) || '%') OR COALESCE(h.reference_labels,'') NOT IN ('','null','{}','[]') OR COALESCE(h.provenance_records,'') NOT IN ('','null','{}','[]') OR COALESCE(h.resolved_root_digests,'') NOT IN ('','null','{}','[]') OR COALESCE(h.remaining_resolvables,'') NOT IN ('','null','{}','[]'))))) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_resource_updates_update BEFORE UPDATE ON resource_updates FOR EACH ROW EXECUTE FUNCTION admission_resource_updates_update();
-- +goose StatementBegin
CREATE FUNCTION admission_resource_updates_delete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT r.stack_label AS label FROM (SELECT OLD.stack_label AS stack_label, OLD.ksuid AS ksuid, OLD.resource AS resource, OLD.existing_resource AS existing_resource, OLD.resource_target AS resource_target, OLD.existing_target AS existing_target, OLD.reference_labels AS reference_labels, OLD.provenance_records AS provenance_records, OLD.resolved_root_digests AS resolved_root_digests, OLD.remaining_resolvables AS remaining_resolvables) r) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT r.stack_label AS label FROM (SELECT OLD.stack_label AS stack_label, OLD.ksuid AS ksuid, OLD.resource AS resource, OLD.existing_resource AS existing_resource, OLD.resource_target AS resource_target, OLD.existing_target AS existing_target, OLD.reference_labels AS reference_labels, OLD.provenance_records AS provenance_records, OLD.resolved_root_digests AS resolved_root_digests, OLD.remaining_resolvables AS remaining_resolvables) r) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT r.stack_label AS label FROM (SELECT OLD.stack_label AS stack_label, OLD.ksuid AS ksuid, OLD.resource AS resource, OLD.existing_resource AS existing_resource, OLD.resource_target AS resource_target, OLD.existing_target AS existing_target, OLD.reference_labels AS reference_labels, OLD.provenance_records AS provenance_records, OLD.resolved_root_digests AS resolved_root_digests, OLD.remaining_resolvables AS remaining_resolvables) r) x ON s.label=x.label
UNION
SELECT 'admission:topology' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT OLD.stack_label AS stack_label, OLD.ksuid AS ksuid, OLD.resource AS resource, OLD.existing_resource AS existing_resource, OLD.resource_target AS resource_target, OLD.existing_target AS existing_target, OLD.reference_labels AS reference_labels, OLD.provenance_records AS provenance_records, OLD.resolved_root_digests AS resolved_root_digests, OLD.remaining_resolvables AS remaining_resolvables) r WHERE (CAST(r.resource AS TEXT) LIKE '%$ref%' OR CAST(r.resource AS TEXT) LIKE '%$gen%' OR CAST(r.resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(r.existing_resource AS TEXT) LIKE '%$ref%' OR CAST(r.existing_resource AS TEXT) LIKE '%$gen%' OR CAST(r.existing_resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(r.resource_target AS TEXT) LIKE '%$ref%' OR CAST(r.resource_target AS TEXT) LIKE '%$gen%' OR CAST(r.resource_target AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(r.existing_target AS TEXT) LIKE '%$ref%' OR CAST(r.existing_target AS TEXT) LIKE '%$gen%' OR CAST(r.existing_target AS TEXT) LIKE '%' || chr(92) || '%') OR COALESCE(r.reference_labels,'') NOT IN ('','null','{}','[]') OR COALESCE(r.provenance_records,'') NOT IN ('','null','{}','[]') OR COALESCE(r.resolved_root_digests,'') NOT IN ('','null','{}','[]') OR COALESCE(r.remaining_resolvables,'') NOT IN ('','null','{}','[]') OR EXISTS (SELECT 1 FROM resource_updates h WHERE h.ksuid=r.ksuid AND ((CAST(h.resource AS TEXT) LIKE '%$ref%' OR CAST(h.resource AS TEXT) LIKE '%$gen%' OR CAST(h.resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(h.existing_resource AS TEXT) LIKE '%$ref%' OR CAST(h.existing_resource AS TEXT) LIKE '%$gen%' OR CAST(h.existing_resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(h.resource_target AS TEXT) LIKE '%$ref%' OR CAST(h.resource_target AS TEXT) LIKE '%$gen%' OR CAST(h.resource_target AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(h.existing_target AS TEXT) LIKE '%$ref%' OR CAST(h.existing_target AS TEXT) LIKE '%$gen%' OR CAST(h.existing_target AS TEXT) LIKE '%' || chr(92) || '%') OR COALESCE(h.reference_labels,'') NOT IN ('','null','{}','[]') OR COALESCE(h.provenance_records,'') NOT IN ('','null','{}','[]') OR COALESCE(h.resolved_root_digests,'') NOT IN ('','null','{}','[]') OR COALESCE(h.remaining_resolvables,'') NOT IN ('','null','{}','[]'))))) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN OLD;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_resource_updates_delete BEFORE DELETE ON resource_updates FOR EACH ROW EXECUTE FUNCTION admission_resource_updates_delete();
-- +goose StatementBegin
-- Read-only membership is not planning input. Classify both command row images;
-- retain normalized intent, observed history and setup contributions below.
CREATE FUNCTION admission_forma_commands_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT cs.stack_label AS label FROM command_stacks cs JOIN (SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r ON cs.command_id=r.command_id WHERE NOT (COALESCE(r.command,'')='sync' AND COALESCE(r.source,'') IN ('synchronizer','discovery')) UNION SELECT ru.stack_label FROM resource_updates ru JOIN (SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r ON ru.command_id=r.command_id UNION SELECT h.stack FROM resources h JOIN (SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r ON h.command_id=r.command_id) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT cs.stack_label AS label FROM command_stacks cs JOIN (SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r ON cs.command_id=r.command_id WHERE NOT (COALESCE(r.command,'')='sync' AND COALESCE(r.source,'') IN ('synchronizer','discovery')) UNION SELECT ru.stack_label FROM resource_updates ru JOIN (SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r ON ru.command_id=r.command_id UNION SELECT h.stack FROM resources h JOIN (SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r ON h.command_id=r.command_id) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT cs.stack_label AS label FROM command_stacks cs JOIN (SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r ON cs.command_id=r.command_id WHERE NOT (COALESCE(r.command,'')='sync' AND COALESCE(r.source,'') IN ('synchronizer','discovery')) UNION SELECT ru.stack_label FROM resource_updates ru JOIN (SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r ON ru.command_id=r.command_id UNION SELECT h.stack FROM resources h JOIN (SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r ON h.command_id=r.command_id) x ON s.label=x.label
UNION
SELECT 'admission:targets' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r WHERE COALESCE(r.target_updates,'') NOT IN ('','null','{}','[]'))
UNION
SELECT 'admission:topology' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r WHERE COALESCE(r.target_updates,'') NOT IN ('','null','{}','[]'))
UNION
SELECT 'admission:stack-mapping' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r WHERE COALESCE(r.stack_updates,'') NOT IN ('','null','{}','[]'))
UNION
SELECT 'admission:policies' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r WHERE COALESCE(r.policy_updates,'') NOT IN ('','null','{}','[]'))
UNION
SELECT 'admission:topology' AS guard_key WHERE EXISTS (SELECT 1 FROM resource_updates u JOIN (SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r ON u.command_id=r.command_id WHERE (CAST(u.resource AS TEXT) LIKE '%$ref%' OR CAST(u.resource AS TEXT) LIKE '%$gen%' OR CAST(u.resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(u.existing_resource AS TEXT) LIKE '%$ref%' OR CAST(u.existing_resource AS TEXT) LIKE '%$gen%' OR CAST(u.existing_resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(u.resource_target AS TEXT) LIKE '%$ref%' OR CAST(u.resource_target AS TEXT) LIKE '%$gen%' OR CAST(u.resource_target AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(u.existing_target AS TEXT) LIKE '%$ref%' OR CAST(u.existing_target AS TEXT) LIKE '%$gen%' OR CAST(u.existing_target AS TEXT) LIKE '%' || chr(92) || '%') OR COALESCE(u.reference_labels,'') NOT IN ('','null','{}','[]') OR COALESCE(u.provenance_records,'') NOT IN ('','null','{}','[]') OR COALESCE(u.resolved_root_digests,'') NOT IN ('','null','{}','[]') OR COALESCE(u.remaining_resolvables,'') NOT IN ('','null','{}','[]') OR EXISTS (SELECT 1 FROM resource_updates h WHERE h.ksuid=u.ksuid AND ((CAST(h.resource AS TEXT) LIKE '%$ref%' OR CAST(h.resource AS TEXT) LIKE '%$gen%' OR CAST(h.resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(h.existing_resource AS TEXT) LIKE '%$ref%' OR CAST(h.existing_resource AS TEXT) LIKE '%$gen%' OR CAST(h.existing_resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(h.resource_target AS TEXT) LIKE '%$ref%' OR CAST(h.resource_target AS TEXT) LIKE '%$gen%' OR CAST(h.resource_target AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(h.existing_target AS TEXT) LIKE '%$ref%' OR CAST(h.existing_target AS TEXT) LIKE '%$gen%' OR CAST(h.existing_target AS TEXT) LIKE '%' || chr(92) || '%') OR COALESCE(h.reference_labels,'') NOT IN ('','null','{}','[]') OR COALESCE(h.provenance_records,'') NOT IN ('','null','{}','[]') OR COALESCE(h.resolved_root_digests,'') NOT IN ('','null','{}','[]') OR COALESCE(h.remaining_resolvables,'') NOT IN ('','null','{}','[]'))))
UNION
SELECT 'admission:topology' AS guard_key WHERE EXISTS (SELECT 1 FROM resources h JOIN (SELECT NEW.command_id AS command_id, NEW.command AS command, NEW.source AS source, NEW.target_updates AS target_updates, NEW.stack_updates AS stack_updates, NEW.policy_updates AS policy_updates) r ON h.command_id=r.command_id WHERE (CAST(h.data AS TEXT) LIKE '%$ref%' OR CAST(h.data AS TEXT) LIKE '%$gen%' OR CAST(h.data AS TEXT) LIKE '%' || chr(92) || '%'))) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_forma_commands_insert BEFORE INSERT ON forma_commands FOR EACH ROW EXECUTE FUNCTION admission_forma_commands_insert();
-- +goose StatementBegin
CREATE FUNCTION admission_forma_commands_update() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER admission_forma_commands_update BEFORE UPDATE ON forma_commands FOR EACH ROW EXECUTE FUNCTION admission_forma_commands_update();
-- +goose StatementBegin
CREATE FUNCTION admission_forma_commands_delete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT cs.stack_label AS label FROM command_stacks cs JOIN (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates) r ON cs.command_id=r.command_id WHERE NOT (COALESCE(r.command,'')='sync' AND COALESCE(r.source,'') IN ('synchronizer','discovery')) UNION SELECT ru.stack_label FROM resource_updates ru JOIN (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates) r ON ru.command_id=r.command_id UNION SELECT h.stack FROM resources h JOIN (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates) r ON h.command_id=r.command_id) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT cs.stack_label AS label FROM command_stacks cs JOIN (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates) r ON cs.command_id=r.command_id WHERE NOT (COALESCE(r.command,'')='sync' AND COALESCE(r.source,'') IN ('synchronizer','discovery')) UNION SELECT ru.stack_label FROM resource_updates ru JOIN (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates) r ON ru.command_id=r.command_id UNION SELECT h.stack FROM resources h JOIN (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates) r ON h.command_id=r.command_id) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT cs.stack_label AS label FROM command_stacks cs JOIN (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates) r ON cs.command_id=r.command_id WHERE NOT (COALESCE(r.command,'')='sync' AND COALESCE(r.source,'') IN ('synchronizer','discovery')) UNION SELECT ru.stack_label FROM resource_updates ru JOIN (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates) r ON ru.command_id=r.command_id UNION SELECT h.stack FROM resources h JOIN (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates) r ON h.command_id=r.command_id) x ON s.label=x.label
UNION
SELECT 'admission:targets' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates) r WHERE COALESCE(r.target_updates,'') NOT IN ('','null','{}','[]'))
UNION
SELECT 'admission:topology' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates) r WHERE COALESCE(r.target_updates,'') NOT IN ('','null','{}','[]'))
UNION
SELECT 'admission:stack-mapping' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates) r WHERE COALESCE(r.stack_updates,'') NOT IN ('','null','{}','[]'))
UNION
SELECT 'admission:policies' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates) r WHERE COALESCE(r.policy_updates,'') NOT IN ('','null','{}','[]'))
UNION
SELECT 'admission:topology' AS guard_key WHERE EXISTS (SELECT 1 FROM resource_updates u JOIN (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates) r ON u.command_id=r.command_id WHERE (CAST(u.resource AS TEXT) LIKE '%$ref%' OR CAST(u.resource AS TEXT) LIKE '%$gen%' OR CAST(u.resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(u.existing_resource AS TEXT) LIKE '%$ref%' OR CAST(u.existing_resource AS TEXT) LIKE '%$gen%' OR CAST(u.existing_resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(u.resource_target AS TEXT) LIKE '%$ref%' OR CAST(u.resource_target AS TEXT) LIKE '%$gen%' OR CAST(u.resource_target AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(u.existing_target AS TEXT) LIKE '%$ref%' OR CAST(u.existing_target AS TEXT) LIKE '%$gen%' OR CAST(u.existing_target AS TEXT) LIKE '%' || chr(92) || '%') OR COALESCE(u.reference_labels,'') NOT IN ('','null','{}','[]') OR COALESCE(u.provenance_records,'') NOT IN ('','null','{}','[]') OR COALESCE(u.resolved_root_digests,'') NOT IN ('','null','{}','[]') OR COALESCE(u.remaining_resolvables,'') NOT IN ('','null','{}','[]') OR EXISTS (SELECT 1 FROM resource_updates h WHERE h.ksuid=u.ksuid AND ((CAST(h.resource AS TEXT) LIKE '%$ref%' OR CAST(h.resource AS TEXT) LIKE '%$gen%' OR CAST(h.resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(h.existing_resource AS TEXT) LIKE '%$ref%' OR CAST(h.existing_resource AS TEXT) LIKE '%$gen%' OR CAST(h.existing_resource AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(h.resource_target AS TEXT) LIKE '%$ref%' OR CAST(h.resource_target AS TEXT) LIKE '%$gen%' OR CAST(h.resource_target AS TEXT) LIKE '%' || chr(92) || '%') OR (CAST(h.existing_target AS TEXT) LIKE '%$ref%' OR CAST(h.existing_target AS TEXT) LIKE '%$gen%' OR CAST(h.existing_target AS TEXT) LIKE '%' || chr(92) || '%') OR COALESCE(h.reference_labels,'') NOT IN ('','null','{}','[]') OR COALESCE(h.provenance_records,'') NOT IN ('','null','{}','[]') OR COALESCE(h.resolved_root_digests,'') NOT IN ('','null','{}','[]') OR COALESCE(h.remaining_resolvables,'') NOT IN ('','null','{}','[]'))))
UNION
SELECT 'admission:topology' AS guard_key WHERE EXISTS (SELECT 1 FROM resources h JOIN (SELECT OLD.command_id AS command_id, OLD.command AS command, OLD.source AS source, OLD.target_updates AS target_updates, OLD.stack_updates AS stack_updates, OLD.policy_updates AS policy_updates) r ON h.command_id=r.command_id WHERE (CAST(h.data AS TEXT) LIKE '%$ref%' OR CAST(h.data AS TEXT) LIKE '%$gen%' OR CAST(h.data AS TEXT) LIKE '%' || chr(92) || '%'))) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN OLD;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_forma_commands_delete BEFORE DELETE ON forma_commands FOR EACH ROW EXECUTE FUNCTION admission_forma_commands_delete();
-- +goose StatementBegin
CREATE FUNCTION admission_command_stacks_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT r.stack_label AS label FROM (SELECT NEW.stack_label AS stack_label, NEW.stack_id AS stack_id, NEW.command_id AS command_id) r WHERE NOT EXISTS (SELECT 1 FROM forma_commands c WHERE c.command_id=r.command_id AND c.command='sync' AND c.source IN ('synchronizer','discovery'))) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT r.stack_label AS label FROM (SELECT NEW.stack_label AS stack_label, NEW.stack_id AS stack_id, NEW.command_id AS command_id) r WHERE NOT EXISTS (SELECT 1 FROM forma_commands c WHERE c.command_id=r.command_id AND c.command='sync' AND c.source IN ('synchronizer','discovery'))) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT r.stack_label AS label FROM (SELECT NEW.stack_label AS stack_label, NEW.stack_id AS stack_id, NEW.command_id AS command_id) r WHERE NOT EXISTS (SELECT 1 FROM forma_commands c WHERE c.command_id=r.command_id AND c.command='sync' AND c.source IN ('synchronizer','discovery'))) x ON s.label=x.label
UNION
SELECT CASE WHEN RTRIM(r.stack_id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(r.stack_id) ELSE 'admission:stack:exceptional' END FROM (SELECT NEW.stack_label AS stack_label, NEW.stack_id AS stack_id, NEW.command_id AS command_id) r WHERE r.stack_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM forma_commands c WHERE c.command_id=r.command_id AND c.command='sync' AND c.source IN ('synchronizer','discovery'))) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_command_stacks_insert BEFORE INSERT ON command_stacks FOR EACH ROW EXECUTE FUNCTION admission_command_stacks_insert();
-- +goose StatementBegin
CREATE FUNCTION admission_command_stacks_update() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT r.stack_label AS label FROM (SELECT OLD.stack_label AS stack_label, OLD.stack_id AS stack_id, OLD.command_id AS command_id UNION ALL SELECT NEW.stack_label AS stack_label, NEW.stack_id AS stack_id, NEW.command_id AS command_id) r WHERE NOT EXISTS (SELECT 1 FROM forma_commands c WHERE c.command_id=r.command_id AND c.command='sync' AND c.source IN ('synchronizer','discovery'))) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT r.stack_label AS label FROM (SELECT OLD.stack_label AS stack_label, OLD.stack_id AS stack_id, OLD.command_id AS command_id UNION ALL SELECT NEW.stack_label AS stack_label, NEW.stack_id AS stack_id, NEW.command_id AS command_id) r WHERE NOT EXISTS (SELECT 1 FROM forma_commands c WHERE c.command_id=r.command_id AND c.command='sync' AND c.source IN ('synchronizer','discovery'))) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT r.stack_label AS label FROM (SELECT OLD.stack_label AS stack_label, OLD.stack_id AS stack_id, OLD.command_id AS command_id UNION ALL SELECT NEW.stack_label AS stack_label, NEW.stack_id AS stack_id, NEW.command_id AS command_id) r WHERE NOT EXISTS (SELECT 1 FROM forma_commands c WHERE c.command_id=r.command_id AND c.command='sync' AND c.source IN ('synchronizer','discovery'))) x ON s.label=x.label
UNION
SELECT CASE WHEN RTRIM(r.stack_id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(r.stack_id) ELSE 'admission:stack:exceptional' END FROM (SELECT OLD.stack_label AS stack_label, OLD.stack_id AS stack_id, OLD.command_id AS command_id UNION ALL SELECT NEW.stack_label AS stack_label, NEW.stack_id AS stack_id, NEW.command_id AS command_id) r WHERE r.stack_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM forma_commands c WHERE c.command_id=r.command_id AND c.command='sync' AND c.source IN ('synchronizer','discovery'))) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_command_stacks_update BEFORE UPDATE ON command_stacks FOR EACH ROW EXECUTE FUNCTION admission_command_stacks_update();
-- +goose StatementBegin
CREATE FUNCTION admission_command_stacks_delete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
INSERT INTO admission_stack_labels(label) SELECT DISTINCT label FROM (SELECT r.stack_label AS label FROM (SELECT OLD.stack_label AS stack_label, OLD.stack_id AS stack_id, OLD.command_id AS command_id) r WHERE NOT EXISTS (SELECT 1 FROM forma_commands c WHERE c.command_id=r.command_id AND c.command='sync' AND c.source IN ('synchronizer','discovery'))) labels WHERE label IS NOT NULL ON CONFLICT(label) DO NOTHING;
FOR k IN SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT r.stack_label AS label FROM (SELECT OLD.stack_label AS stack_label, OLD.stack_id AS stack_id, OLD.command_id AS command_id) r WHERE NOT EXISTS (SELECT 1 FROM forma_commands c WHERE c.command_id=r.command_id AND c.command='sync' AND c.source IN ('synchronizer','discovery'))) x ON l.label=x.label
UNION
SELECT CASE WHEN RTRIM(s.id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s JOIN (SELECT r.stack_label AS label FROM (SELECT OLD.stack_label AS stack_label, OLD.stack_id AS stack_id, OLD.command_id AS command_id) r WHERE NOT EXISTS (SELECT 1 FROM forma_commands c WHERE c.command_id=r.command_id AND c.command='sync' AND c.source IN ('synchronizer','discovery'))) x ON s.label=x.label
UNION
SELECT CASE WHEN RTRIM(r.stack_id) ~ '^[A-Za-z0-9_-]{1,128}$' THEN 'admission:stack:' || RTRIM(r.stack_id) ELSE 'admission:stack:exceptional' END FROM (SELECT OLD.stack_label AS stack_label, OLD.stack_id AS stack_id, OLD.command_id AS command_id) r WHERE r.stack_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM forma_commands c WHERE c.command_id=r.command_id AND c.command='sync' AND c.source IN ('synchronizer','discovery'))) keys ORDER BY guard_key COLLATE "C" LOOP
INSERT INTO admission_revisions(guard_key,revision) VALUES (k,1) ON CONFLICT(guard_key) DO UPDATE SET revision=admission_revisions.revision+1;
END LOOP;
RETURN OLD;
END $$;
-- +goose StatementEnd
CREATE TRIGGER admission_command_stacks_delete BEFORE DELETE ON command_stacks FOR EACH ROW EXECUTE FUNCTION admission_command_stacks_delete();
-- +goose Down
DROP TRIGGER admission_resources_insert ON resources;
DROP FUNCTION admission_resources_insert();
DROP TRIGGER admission_resources_update ON resources;
DROP FUNCTION admission_resources_update();
DROP TRIGGER admission_resources_delete ON resources;
DROP FUNCTION admission_resources_delete();
DROP TRIGGER admission_stacks_insert ON stacks;
DROP FUNCTION admission_stacks_insert();
DROP TRIGGER admission_stacks_update ON stacks;
DROP FUNCTION admission_stacks_update();
DROP TRIGGER admission_stacks_delete ON stacks;
DROP FUNCTION admission_stacks_delete();
DROP TRIGGER admission_targets_insert ON targets;
DROP FUNCTION admission_targets_insert();
DROP TRIGGER admission_targets_update ON targets;
DROP FUNCTION admission_targets_update();
DROP TRIGGER admission_targets_delete ON targets;
DROP FUNCTION admission_targets_delete();
DROP TRIGGER admission_policies_insert ON policies;
DROP FUNCTION admission_policies_insert();
DROP TRIGGER admission_policies_update ON policies;
DROP FUNCTION admission_policies_update();
DROP TRIGGER admission_policies_delete ON policies;
DROP FUNCTION admission_policies_delete();
DROP TRIGGER admission_stack_policies_insert ON stack_policies;
DROP FUNCTION admission_stack_policies_insert();
DROP TRIGGER admission_stack_policies_update ON stack_policies;
DROP FUNCTION admission_stack_policies_update();
DROP TRIGGER admission_stack_policies_delete ON stack_policies;
DROP FUNCTION admission_stack_policies_delete();
DROP TRIGGER admission_generators_insert ON generators;
DROP FUNCTION admission_generators_insert();
DROP TRIGGER admission_generators_update ON generators;
DROP FUNCTION admission_generators_update();
DROP TRIGGER admission_generators_delete ON generators;
DROP FUNCTION admission_generators_delete();
DROP TRIGGER admission_resource_updates_insert ON resource_updates;
DROP FUNCTION admission_resource_updates_insert();
DROP TRIGGER admission_resource_updates_update ON resource_updates;
DROP FUNCTION admission_resource_updates_update();
DROP TRIGGER admission_resource_updates_delete ON resource_updates;
DROP FUNCTION admission_resource_updates_delete();
DROP TRIGGER admission_forma_commands_insert ON forma_commands;
DROP FUNCTION admission_forma_commands_insert();
DROP TRIGGER admission_forma_commands_update ON forma_commands;
DROP FUNCTION admission_forma_commands_update();
DROP TRIGGER admission_forma_commands_delete ON forma_commands;
DROP FUNCTION admission_forma_commands_delete();
DROP TRIGGER admission_command_stacks_insert ON command_stacks;
DROP FUNCTION admission_command_stacks_insert();
DROP TRIGGER admission_command_stacks_update ON command_stacks;
DROP FUNCTION admission_command_stacks_update();
DROP TRIGGER admission_command_stacks_delete ON command_stacks;
DROP FUNCTION admission_command_stacks_delete();
DROP TABLE admission_resource_uris;
DROP TABLE admission_stack_labels;
