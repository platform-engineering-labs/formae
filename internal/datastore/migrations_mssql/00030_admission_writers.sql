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
CREATE TABLE admission_stack_labels (label NVARCHAR(450) NOT NULL PRIMARY KEY, guard_key NVARCHAR(450) COLLATE Latin1_General_BIN2 NOT NULL UNIQUE DEFAULT ('admission:label:'+CONVERT(VARCHAR(36),NEWID())));
CREATE TABLE admission_resource_uris (uri NVARCHAR(450) NOT NULL PRIMARY KEY, guard_key NVARCHAR(450) COLLATE Latin1_General_BIN2 NOT NULL UNIQUE DEFAULT ('admission:uri:'+CONVERT(VARCHAR(36),NEWID())));
-- +goose StatementBegin
CREATE TRIGGER admission_resources_all ON resources AFTER INSERT, UPDATE, DELETE AS
BEGIN
SET NOCOUNT ON;
IF NOT EXISTS (SELECT 1 FROM inserted) AND NOT EXISTS (SELECT 1 FROM deleted) RETURN;
DECLARE @uri NVARCHAR(450), @uri_guard NVARCHAR(450);
DECLARE uris CURSOR LOCAL FAST_FORWARD FOR SELECT DISTINCT uri FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) affected ORDER BY uri;
OPEN uris; FETCH NEXT FROM uris INTO @uri;
WHILE @@FETCH_STATUS=0 BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_resource_uris WHERE uri=@uri) BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_resource_uris WITH (UPDLOCK,HOLDLOCK) WHERE uri=@uri) INSERT INTO admission_resource_uris(uri) VALUES (@uri); END;
FETCH NEXT FROM uris INTO @uri; END; CLOSE uris; DEALLOCATE uris;
DECLARE uri_keys CURSOR LOCAL FAST_FORWARD FOR SELECT u.guard_key FROM admission_resource_uris u WITH (READCOMMITTEDLOCK) JOIN (SELECT DISTINCT uri FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) affected) a ON a.uri=u.uri ORDER BY u.guard_key;
OPEN uri_keys; FETCH NEXT FROM uri_keys INTO @uri_guard;
WHILE @@FETCH_STATUS=0 BEGIN
UPDATE admission_revisions WITH (UPDLOCK,HOLDLOCK) SET revision=revision+1 WHERE guard_key=@uri_guard;
IF @@ROWCOUNT=0 INSERT INTO admission_revisions(guard_key,revision) VALUES (@uri_guard,1);
FETCH NEXT FROM uri_keys INTO @uri_guard; END; CLOSE uri_keys; DEALLOCATE uri_keys;
DECLARE @label NVARCHAR(450), @key NVARCHAR(450);
DECLARE labels CURSOR LOCAL FAST_FORWARD FOR SELECT DISTINCT label FROM (SELECT r.stack AS label FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r UNION SELECT JSON_VALUE(CASE WHEN ISJSON(r.data)=1 THEN r.data ELSE '{}' END, '$.Stack') FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r UNION SELECT h.stack FROM resources h WITH (READCOMMITTEDLOCK) JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON h.uri=r.uri UNION SELECT JSON_VALUE(CASE WHEN ISJSON(h.data)=1 THEN h.data ELSE '{}' END, '$.Stack') FROM resources h WITH (READCOMMITTEDLOCK) JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON h.uri=r.uri) labels WHERE label IS NOT NULL ORDER BY label;
OPEN labels; FETCH NEXT FROM labels INTO @label;
WHILE @@FETCH_STATUS=0 BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_stack_labels WHERE label=@label) BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_stack_labels WITH (UPDLOCK,HOLDLOCK) WHERE label=@label) INSERT INTO admission_stack_labels(label) VALUES (@label); END;
FETCH NEXT FROM labels INTO @label; END; CLOSE labels; DEALLOCATE labels;
DECLARE keys_cursor CURSOR LOCAL FAST_FORWARD FOR SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l WITH (READCOMMITTEDLOCK) JOIN (SELECT r.stack AS label FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r UNION SELECT JSON_VALUE(CASE WHEN ISJSON(r.data)=1 THEN r.data ELSE '{}' END, '$.Stack') FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r UNION SELECT h.stack FROM resources h WITH (READCOMMITTEDLOCK) JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON h.uri=r.uri UNION SELECT JSON_VALUE(CASE WHEN ISJSON(h.data)=1 THEN h.data ELSE '{}' END, '$.Stack') FROM resources h WITH (READCOMMITTEDLOCK) JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON h.uri=r.uri) x ON l.label=x.label
UNION
SELECT CASE WHEN DATALENGTH(RTRIM(s.id)) BETWEEN 2 AND 256 AND RTRIM(s.id) COLLATE Latin1_General_BIN2 NOT LIKE '%[^A-Za-z0-9_-]%' THEN 'admission:stack:' + RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s WITH (INDEX(idx_stacks_label), FORCESEEK) JOIN (SELECT r.stack AS label FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r UNION SELECT JSON_VALUE(CASE WHEN ISJSON(r.data)=1 THEN r.data ELSE '{}' END, '$.Stack') FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r UNION SELECT h.stack FROM resources h WITH (READCOMMITTEDLOCK) JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON h.uri=r.uri UNION SELECT JSON_VALUE(CASE WHEN ISJSON(h.data)=1 THEN h.data ELSE '{}' END, '$.Stack') FROM resources h WITH (READCOMMITTEDLOCK) JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON h.uri=r.uri) x ON s.label=x.label
UNION
SELECT 'admission:topology' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r WHERE (CAST(r.data AS NVARCHAR(MAX)) LIKE '%$ref%' OR CAST(r.data AS NVARCHAR(MAX)) LIKE '%$gen%' OR CAST(r.data AS NVARCHAR(MAX)) LIKE '%' + CHAR(92) + '%') OR EXISTS (SELECT 1 FROM resources h WITH (READCOMMITTEDLOCK) WHERE h.uri=r.uri AND ((CAST(h.data AS NVARCHAR(MAX)) LIKE '%$ref%' OR CAST(h.data AS NVARCHAR(MAX)) LIKE '%$gen%' OR CAST(h.data AS NVARCHAR(MAX)) LIKE '%' + CHAR(92) + '%'))))) keys ORDER BY guard_key;
OPEN keys_cursor; FETCH NEXT FROM keys_cursor INTO @key;
WHILE @@FETCH_STATUS=0 BEGIN
UPDATE admission_revisions WITH (UPDLOCK,HOLDLOCK) SET revision=revision+1 WHERE guard_key=@key;
IF @@ROWCOUNT=0 INSERT INTO admission_revisions(guard_key,revision) VALUES (@key,1);
FETCH NEXT FROM keys_cursor INTO @key; END; CLOSE keys_cursor; DEALLOCATE keys_cursor;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER admission_stacks_all ON stacks AFTER INSERT, UPDATE, DELETE AS
BEGIN
SET NOCOUNT ON;
IF NOT EXISTS (SELECT 1 FROM inserted) AND NOT EXISTS (SELECT 1 FROM deleted) RETURN;
DECLARE @label NVARCHAR(450), @key NVARCHAR(450);
DECLARE labels CURSOR LOCAL FAST_FORWARD FOR SELECT DISTINCT label FROM (SELECT r.label FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r) labels WHERE label IS NOT NULL ORDER BY label;
OPEN labels; FETCH NEXT FROM labels INTO @label;
WHILE @@FETCH_STATUS=0 BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_stack_labels WHERE label=@label) BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_stack_labels WITH (UPDLOCK,HOLDLOCK) WHERE label=@label) INSERT INTO admission_stack_labels(label) VALUES (@label); END;
FETCH NEXT FROM labels INTO @label; END; CLOSE labels; DEALLOCATE labels;
DECLARE keys_cursor CURSOR LOCAL FAST_FORWARD FOR SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT r.label FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r) x ON l.label=x.label
UNION
SELECT CASE WHEN DATALENGTH(RTRIM(s.id)) BETWEEN 2 AND 256 AND RTRIM(s.id) COLLATE Latin1_General_BIN2 NOT LIKE '%[^A-Za-z0-9_-]%' THEN 'admission:stack:' + RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s WITH (INDEX(idx_stacks_label), FORCESEEK) JOIN (SELECT r.label FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r) x ON s.label=x.label
UNION
SELECT 'admission:stack-mapping' AS guard_key
UNION
SELECT CASE WHEN DATALENGTH(RTRIM(r.id)) BETWEEN 2 AND 256 AND RTRIM(r.id) COLLATE Latin1_General_BIN2 NOT LIKE '%[^A-Za-z0-9_-]%' THEN 'admission:stack:' + RTRIM(r.id) ELSE 'admission:stack:exceptional' END FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r) keys ORDER BY guard_key;
OPEN keys_cursor; FETCH NEXT FROM keys_cursor INTO @key;
WHILE @@FETCH_STATUS=0 BEGIN
UPDATE admission_revisions WITH (UPDLOCK,HOLDLOCK) SET revision=revision+1 WHERE guard_key=@key;
IF @@ROWCOUNT=0 INSERT INTO admission_revisions(guard_key,revision) VALUES (@key,1);
FETCH NEXT FROM keys_cursor INTO @key; END; CLOSE keys_cursor; DEALLOCATE keys_cursor;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER admission_targets_all ON targets AFTER INSERT, UPDATE, DELETE AS
BEGIN
SET NOCOUNT ON;
IF NOT EXISTS (SELECT 1 FROM inserted) AND NOT EXISTS (SELECT 1 FROM deleted) RETURN;
IF NOT EXISTS (SELECT label COLLATE Latin1_General_BIN2,DATALENGTH(label),version,namespace COLLATE Latin1_General_BIN2,DATALENGTH(namespace),config COLLATE Latin1_General_BIN2,DATALENGTH(config),config_schema COLLATE Latin1_General_BIN2,DATALENGTH(config_schema),discoverable,target_incarnation_id COLLATE Latin1_General_BIN2,DATALENGTH(target_incarnation_id),reap_kind COLLATE Latin1_General_BIN2,DATALENGTH(reap_kind),reap_max_unreachable_seconds,CASE WHEN health_state='reaped' THEN 1 ELSE 0 END FROM inserted EXCEPT SELECT label COLLATE Latin1_General_BIN2,DATALENGTH(label),version,namespace COLLATE Latin1_General_BIN2,DATALENGTH(namespace),config COLLATE Latin1_General_BIN2,DATALENGTH(config),config_schema COLLATE Latin1_General_BIN2,DATALENGTH(config_schema),discoverable,target_incarnation_id COLLATE Latin1_General_BIN2,DATALENGTH(target_incarnation_id),reap_kind COLLATE Latin1_General_BIN2,DATALENGTH(reap_kind),reap_max_unreachable_seconds,CASE WHEN health_state='reaped' THEN 1 ELSE 0 END FROM deleted) AND NOT EXISTS (SELECT label COLLATE Latin1_General_BIN2,DATALENGTH(label),version,namespace COLLATE Latin1_General_BIN2,DATALENGTH(namespace),config COLLATE Latin1_General_BIN2,DATALENGTH(config),config_schema COLLATE Latin1_General_BIN2,DATALENGTH(config_schema),discoverable,target_incarnation_id COLLATE Latin1_General_BIN2,DATALENGTH(target_incarnation_id),reap_kind COLLATE Latin1_General_BIN2,DATALENGTH(reap_kind),reap_max_unreachable_seconds,CASE WHEN health_state='reaped' THEN 1 ELSE 0 END FROM deleted EXCEPT SELECT label COLLATE Latin1_General_BIN2,DATALENGTH(label),version,namespace COLLATE Latin1_General_BIN2,DATALENGTH(namespace),config COLLATE Latin1_General_BIN2,DATALENGTH(config),config_schema COLLATE Latin1_General_BIN2,DATALENGTH(config_schema),discoverable,target_incarnation_id COLLATE Latin1_General_BIN2,DATALENGTH(target_incarnation_id),reap_kind COLLATE Latin1_General_BIN2,DATALENGTH(reap_kind),reap_max_unreachable_seconds,CASE WHEN health_state='reaped' THEN 1 ELSE 0 END FROM inserted) RETURN;
DECLARE @label NVARCHAR(450), @key NVARCHAR(450);
DECLARE labels CURSOR LOCAL FAST_FORWARD FOR SELECT DISTINCT label FROM (SELECT NULL AS label WHERE 1=0) labels WHERE label IS NOT NULL ORDER BY label;
OPEN labels; FETCH NEXT FROM labels INTO @label;
WHILE @@FETCH_STATUS=0 BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_stack_labels WHERE label=@label) BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_stack_labels WITH (UPDLOCK,HOLDLOCK) WHERE label=@label) INSERT INTO admission_stack_labels(label) VALUES (@label); END;
FETCH NEXT FROM labels INTO @label; END; CLOSE labels; DEALLOCATE labels;
DECLARE keys_cursor CURSOR LOCAL FAST_FORWARD FOR SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT NULL AS label WHERE 1=0) x ON l.label=x.label
UNION
SELECT CASE WHEN DATALENGTH(RTRIM(s.id)) BETWEEN 2 AND 256 AND RTRIM(s.id) COLLATE Latin1_General_BIN2 NOT LIKE '%[^A-Za-z0-9_-]%' THEN 'admission:stack:' + RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s WITH (INDEX(idx_stacks_label), FORCESEEK) JOIN (SELECT NULL AS label WHERE 1=0) x ON s.label=x.label
UNION
SELECT 'admission:targets' AS guard_key
UNION
SELECT 'admission:topology' AS guard_key) keys ORDER BY guard_key;
OPEN keys_cursor; FETCH NEXT FROM keys_cursor INTO @key;
WHILE @@FETCH_STATUS=0 BEGIN
UPDATE admission_revisions WITH (UPDLOCK,HOLDLOCK) SET revision=revision+1 WHERE guard_key=@key;
IF @@ROWCOUNT=0 INSERT INTO admission_revisions(guard_key,revision) VALUES (@key,1);
FETCH NEXT FROM keys_cursor INTO @key; END; CLOSE keys_cursor; DEALLOCATE keys_cursor;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER admission_policies_all ON policies AFTER INSERT, UPDATE, DELETE AS
BEGIN
SET NOCOUNT ON;
IF NOT EXISTS (SELECT 1 FROM inserted) AND NOT EXISTS (SELECT 1 FROM deleted) RETURN;
DECLARE @label NVARCHAR(450), @key NVARCHAR(450);
DECLARE labels CURSOR LOCAL FAST_FORWARD FOR SELECT DISTINCT label FROM (SELECT s.label FROM stacks s JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON s.id=r.stack_id) labels WHERE label IS NOT NULL ORDER BY label;
OPEN labels; FETCH NEXT FROM labels INTO @label;
WHILE @@FETCH_STATUS=0 BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_stack_labels WHERE label=@label) BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_stack_labels WITH (UPDLOCK,HOLDLOCK) WHERE label=@label) INSERT INTO admission_stack_labels(label) VALUES (@label); END;
FETCH NEXT FROM labels INTO @label; END; CLOSE labels; DEALLOCATE labels;
DECLARE keys_cursor CURSOR LOCAL FAST_FORWARD FOR SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT s.label FROM stacks s JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON s.id=r.stack_id) x ON l.label=x.label
UNION
SELECT CASE WHEN DATALENGTH(RTRIM(s.id)) BETWEEN 2 AND 256 AND RTRIM(s.id) COLLATE Latin1_General_BIN2 NOT LIKE '%[^A-Za-z0-9_-]%' THEN 'admission:stack:' + RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s WITH (INDEX(idx_stacks_label), FORCESEEK) JOIN (SELECT s.label FROM stacks s JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON s.id=r.stack_id) x ON s.label=x.label
UNION
SELECT CASE WHEN DATALENGTH(RTRIM(r.stack_id)) BETWEEN 2 AND 256 AND RTRIM(r.stack_id) COLLATE Latin1_General_BIN2 NOT LIKE '%[^A-Za-z0-9_-]%' THEN 'admission:stack:' + RTRIM(r.stack_id) ELSE 'admission:stack:exceptional' END FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r WHERE r.stack_id IS NOT NULL
UNION
SELECT 'admission:policies' AS guard_key) keys ORDER BY guard_key;
OPEN keys_cursor; FETCH NEXT FROM keys_cursor INTO @key;
WHILE @@FETCH_STATUS=0 BEGIN
UPDATE admission_revisions WITH (UPDLOCK,HOLDLOCK) SET revision=revision+1 WHERE guard_key=@key;
IF @@ROWCOUNT=0 INSERT INTO admission_revisions(guard_key,revision) VALUES (@key,1);
FETCH NEXT FROM keys_cursor INTO @key; END; CLOSE keys_cursor; DEALLOCATE keys_cursor;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER admission_stack_policies_all ON stack_policies AFTER INSERT, UPDATE, DELETE AS
BEGIN
SET NOCOUNT ON;
IF NOT EXISTS (SELECT 1 FROM inserted) AND NOT EXISTS (SELECT 1 FROM deleted) RETURN;
DECLARE @label NVARCHAR(450), @key NVARCHAR(450);
DECLARE labels CURSOR LOCAL FAST_FORWARD FOR SELECT DISTINCT label FROM (SELECT s.label FROM stacks s JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON s.id=r.stack_id) labels WHERE label IS NOT NULL ORDER BY label;
OPEN labels; FETCH NEXT FROM labels INTO @label;
WHILE @@FETCH_STATUS=0 BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_stack_labels WHERE label=@label) BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_stack_labels WITH (UPDLOCK,HOLDLOCK) WHERE label=@label) INSERT INTO admission_stack_labels(label) VALUES (@label); END;
FETCH NEXT FROM labels INTO @label; END; CLOSE labels; DEALLOCATE labels;
DECLARE keys_cursor CURSOR LOCAL FAST_FORWARD FOR SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT s.label FROM stacks s JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON s.id=r.stack_id) x ON l.label=x.label
UNION
SELECT CASE WHEN DATALENGTH(RTRIM(s.id)) BETWEEN 2 AND 256 AND RTRIM(s.id) COLLATE Latin1_General_BIN2 NOT LIKE '%[^A-Za-z0-9_-]%' THEN 'admission:stack:' + RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s WITH (INDEX(idx_stacks_label), FORCESEEK) JOIN (SELECT s.label FROM stacks s JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON s.id=r.stack_id) x ON s.label=x.label
UNION
SELECT CASE WHEN DATALENGTH(RTRIM(r.stack_id)) BETWEEN 2 AND 256 AND RTRIM(r.stack_id) COLLATE Latin1_General_BIN2 NOT LIKE '%[^A-Za-z0-9_-]%' THEN 'admission:stack:' + RTRIM(r.stack_id) ELSE 'admission:stack:exceptional' END FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r WHERE r.stack_id IS NOT NULL
UNION
SELECT 'admission:policies' AS guard_key) keys ORDER BY guard_key;
OPEN keys_cursor; FETCH NEXT FROM keys_cursor INTO @key;
WHILE @@FETCH_STATUS=0 BEGIN
UPDATE admission_revisions WITH (UPDLOCK,HOLDLOCK) SET revision=revision+1 WHERE guard_key=@key;
IF @@ROWCOUNT=0 INSERT INTO admission_revisions(guard_key,revision) VALUES (@key,1);
FETCH NEXT FROM keys_cursor INTO @key; END; CLOSE keys_cursor; DEALLOCATE keys_cursor;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER admission_generators_all ON generators AFTER INSERT, UPDATE, DELETE AS
BEGIN
SET NOCOUNT ON;
IF NOT EXISTS (SELECT 1 FROM inserted) AND NOT EXISTS (SELECT 1 FROM deleted) RETURN;
DECLARE @label NVARCHAR(450), @key NVARCHAR(450);
DECLARE labels CURSOR LOCAL FAST_FORWARD FOR SELECT DISTINCT label FROM (SELECT s.label FROM stacks s JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON s.id=r.stack_id) labels WHERE label IS NOT NULL ORDER BY label;
OPEN labels; FETCH NEXT FROM labels INTO @label;
WHILE @@FETCH_STATUS=0 BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_stack_labels WHERE label=@label) BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_stack_labels WITH (UPDLOCK,HOLDLOCK) WHERE label=@label) INSERT INTO admission_stack_labels(label) VALUES (@label); END;
FETCH NEXT FROM labels INTO @label; END; CLOSE labels; DEALLOCATE labels;
DECLARE keys_cursor CURSOR LOCAL FAST_FORWARD FOR SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT s.label FROM stacks s JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON s.id=r.stack_id) x ON l.label=x.label
UNION
SELECT CASE WHEN DATALENGTH(RTRIM(s.id)) BETWEEN 2 AND 256 AND RTRIM(s.id) COLLATE Latin1_General_BIN2 NOT LIKE '%[^A-Za-z0-9_-]%' THEN 'admission:stack:' + RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s WITH (INDEX(idx_stacks_label), FORCESEEK) JOIN (SELECT s.label FROM stacks s JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON s.id=r.stack_id) x ON s.label=x.label
UNION
SELECT CASE WHEN DATALENGTH(RTRIM(r.stack_id)) BETWEEN 2 AND 256 AND RTRIM(r.stack_id) COLLATE Latin1_General_BIN2 NOT LIKE '%[^A-Za-z0-9_-]%' THEN 'admission:stack:' + RTRIM(r.stack_id) ELSE 'admission:stack:exceptional' END FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r WHERE r.stack_id IS NOT NULL
UNION
SELECT 'admission:generators' AS guard_key
UNION
SELECT 'admission:topology' AS guard_key) keys ORDER BY guard_key;
OPEN keys_cursor; FETCH NEXT FROM keys_cursor INTO @key;
WHILE @@FETCH_STATUS=0 BEGIN
UPDATE admission_revisions WITH (UPDLOCK,HOLDLOCK) SET revision=revision+1 WHERE guard_key=@key;
IF @@ROWCOUNT=0 INSERT INTO admission_revisions(guard_key,revision) VALUES (@key,1);
FETCH NEXT FROM keys_cursor INTO @key; END; CLOSE keys_cursor; DEALLOCATE keys_cursor;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER admission_resource_updates_all ON resource_updates AFTER INSERT, UPDATE, DELETE AS
BEGIN
SET NOCOUNT ON;
IF NOT EXISTS (SELECT 1 FROM inserted) AND NOT EXISTS (SELECT 1 FROM deleted) RETURN;
DECLARE @label NVARCHAR(450), @key NVARCHAR(450);
DECLARE labels CURSOR LOCAL FAST_FORWARD FOR SELECT DISTINCT label FROM (SELECT r.stack_label AS label FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r) labels WHERE label IS NOT NULL ORDER BY label;
OPEN labels; FETCH NEXT FROM labels INTO @label;
WHILE @@FETCH_STATUS=0 BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_stack_labels WHERE label=@label) BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_stack_labels WITH (UPDLOCK,HOLDLOCK) WHERE label=@label) INSERT INTO admission_stack_labels(label) VALUES (@label); END;
FETCH NEXT FROM labels INTO @label; END; CLOSE labels; DEALLOCATE labels;
DECLARE keys_cursor CURSOR LOCAL FAST_FORWARD FOR SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT r.stack_label AS label FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r) x ON l.label=x.label
UNION
SELECT CASE WHEN DATALENGTH(RTRIM(s.id)) BETWEEN 2 AND 256 AND RTRIM(s.id) COLLATE Latin1_General_BIN2 NOT LIKE '%[^A-Za-z0-9_-]%' THEN 'admission:stack:' + RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s WITH (INDEX(idx_stacks_label), FORCESEEK) JOIN (SELECT r.stack_label AS label FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r) x ON s.label=x.label
UNION
SELECT 'admission:topology' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r WHERE (CAST(r.resource AS NVARCHAR(MAX)) LIKE '%$ref%' OR CAST(r.resource AS NVARCHAR(MAX)) LIKE '%$gen%' OR CAST(r.resource AS NVARCHAR(MAX)) LIKE '%' + CHAR(92) + '%') OR (CAST(r.existing_resource AS NVARCHAR(MAX)) LIKE '%$ref%' OR CAST(r.existing_resource AS NVARCHAR(MAX)) LIKE '%$gen%' OR CAST(r.existing_resource AS NVARCHAR(MAX)) LIKE '%' + CHAR(92) + '%') OR (CAST(r.resource_target AS NVARCHAR(MAX)) LIKE '%$ref%' OR CAST(r.resource_target AS NVARCHAR(MAX)) LIKE '%$gen%' OR CAST(r.resource_target AS NVARCHAR(MAX)) LIKE '%' + CHAR(92) + '%') OR (CAST(r.existing_target AS NVARCHAR(MAX)) LIKE '%$ref%' OR CAST(r.existing_target AS NVARCHAR(MAX)) LIKE '%$gen%' OR CAST(r.existing_target AS NVARCHAR(MAX)) LIKE '%' + CHAR(92) + '%') OR COALESCE(r.reference_labels,'') NOT IN ('','null','{}','[]') OR COALESCE(r.provenance_records,'') NOT IN ('','null','{}','[]') OR COALESCE(r.resolved_root_digests,'') NOT IN ('','null','{}','[]') OR COALESCE(r.remaining_resolvables,'') NOT IN ('','null','{}','[]') OR EXISTS (SELECT 1 FROM resource_updates h WHERE h.ksuid=r.ksuid AND ((CAST(h.resource AS NVARCHAR(MAX)) LIKE '%$ref%' OR CAST(h.resource AS NVARCHAR(MAX)) LIKE '%$gen%' OR CAST(h.resource AS NVARCHAR(MAX)) LIKE '%' + CHAR(92) + '%') OR (CAST(h.existing_resource AS NVARCHAR(MAX)) LIKE '%$ref%' OR CAST(h.existing_resource AS NVARCHAR(MAX)) LIKE '%$gen%' OR CAST(h.existing_resource AS NVARCHAR(MAX)) LIKE '%' + CHAR(92) + '%') OR (CAST(h.resource_target AS NVARCHAR(MAX)) LIKE '%$ref%' OR CAST(h.resource_target AS NVARCHAR(MAX)) LIKE '%$gen%' OR CAST(h.resource_target AS NVARCHAR(MAX)) LIKE '%' + CHAR(92) + '%') OR (CAST(h.existing_target AS NVARCHAR(MAX)) LIKE '%$ref%' OR CAST(h.existing_target AS NVARCHAR(MAX)) LIKE '%$gen%' OR CAST(h.existing_target AS NVARCHAR(MAX)) LIKE '%' + CHAR(92) + '%') OR COALESCE(h.reference_labels,'') NOT IN ('','null','{}','[]') OR COALESCE(h.provenance_records,'') NOT IN ('','null','{}','[]') OR COALESCE(h.resolved_root_digests,'') NOT IN ('','null','{}','[]') OR COALESCE(h.remaining_resolvables,'') NOT IN ('','null','{}','[]'))))) keys ORDER BY guard_key;
OPEN keys_cursor; FETCH NEXT FROM keys_cursor INTO @key;
WHILE @@FETCH_STATUS=0 BEGIN
UPDATE admission_revisions WITH (UPDLOCK,HOLDLOCK) SET revision=revision+1 WHERE guard_key=@key;
IF @@ROWCOUNT=0 INSERT INTO admission_revisions(guard_key,revision) VALUES (@key,1);
FETCH NEXT FROM keys_cursor INTO @key; END; CLOSE keys_cursor; DEALLOCATE keys_cursor;
END;
-- +goose StatementEnd
-- +goose StatementBegin
-- Read-only membership is not planning input. Classify both command row images;
-- retain normalized intent, observed history and setup contributions below.
CREATE TRIGGER admission_forma_commands_all ON forma_commands AFTER INSERT, UPDATE, DELETE AS
BEGIN
SET NOCOUNT ON;
IF NOT EXISTS (SELECT 1 FROM inserted) AND NOT EXISTS (SELECT 1 FROM deleted) RETURN;
DECLARE @label NVARCHAR(450), @key NVARCHAR(450);
DECLARE labels CURSOR LOCAL FAST_FORWARD FOR SELECT DISTINCT label FROM (SELECT cs.stack_label AS label FROM command_stacks cs JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON cs.command_id=r.command_id WHERE NOT (COALESCE(r.command,'')='sync' AND COALESCE(r.source,'') IN ('synchronizer','discovery')) UNION SELECT ru.stack_label FROM resource_updates ru JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON ru.command_id=r.command_id UNION SELECT h.stack FROM resources h WITH (INDEX(idx_resources_command_id), FORCESEEK) JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON h.command_id=r.command_id) labels WHERE label IS NOT NULL ORDER BY label;
OPEN labels; FETCH NEXT FROM labels INTO @label;
WHILE @@FETCH_STATUS=0 BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_stack_labels WHERE label=@label) BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_stack_labels WITH (UPDLOCK,HOLDLOCK) WHERE label=@label) INSERT INTO admission_stack_labels(label) VALUES (@label); END;
FETCH NEXT FROM labels INTO @label; END; CLOSE labels; DEALLOCATE labels;
DECLARE keys_cursor CURSOR LOCAL FAST_FORWARD FOR SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT cs.stack_label AS label FROM command_stacks cs JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON cs.command_id=r.command_id WHERE NOT (COALESCE(r.command,'')='sync' AND COALESCE(r.source,'') IN ('synchronizer','discovery')) UNION SELECT ru.stack_label FROM resource_updates ru JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON ru.command_id=r.command_id UNION SELECT h.stack FROM resources h WITH (INDEX(idx_resources_command_id), FORCESEEK) JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON h.command_id=r.command_id) x ON l.label=x.label
UNION
SELECT CASE WHEN DATALENGTH(RTRIM(s.id)) BETWEEN 2 AND 256 AND RTRIM(s.id) COLLATE Latin1_General_BIN2 NOT LIKE '%[^A-Za-z0-9_-]%' THEN 'admission:stack:' + RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s WITH (INDEX(idx_stacks_label), FORCESEEK) JOIN (SELECT cs.stack_label AS label FROM command_stacks cs JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON cs.command_id=r.command_id WHERE NOT (COALESCE(r.command,'')='sync' AND COALESCE(r.source,'') IN ('synchronizer','discovery')) UNION SELECT ru.stack_label FROM resource_updates ru JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON ru.command_id=r.command_id UNION SELECT h.stack FROM resources h WITH (INDEX(idx_resources_command_id), FORCESEEK) JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON h.command_id=r.command_id) x ON s.label=x.label
UNION
SELECT 'admission:targets' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r WHERE COALESCE(r.target_updates,'') NOT IN ('','null','{}','[]'))
UNION
SELECT 'admission:topology' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r WHERE COALESCE(r.target_updates,'') NOT IN ('','null','{}','[]'))
UNION
SELECT 'admission:stack-mapping' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r WHERE COALESCE(r.stack_updates,'') NOT IN ('','null','{}','[]'))
UNION
SELECT 'admission:policies' AS guard_key WHERE EXISTS (SELECT 1 FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r WHERE COALESCE(r.policy_updates,'') NOT IN ('','null','{}','[]'))
UNION
SELECT 'admission:topology' AS guard_key WHERE EXISTS (SELECT 1 FROM resource_updates u JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON u.command_id=r.command_id WHERE (CAST(u.resource AS NVARCHAR(MAX)) LIKE '%$ref%' OR CAST(u.resource AS NVARCHAR(MAX)) LIKE '%$gen%' OR CAST(u.resource AS NVARCHAR(MAX)) LIKE '%' + CHAR(92) + '%') OR (CAST(u.existing_resource AS NVARCHAR(MAX)) LIKE '%$ref%' OR CAST(u.existing_resource AS NVARCHAR(MAX)) LIKE '%$gen%' OR CAST(u.existing_resource AS NVARCHAR(MAX)) LIKE '%' + CHAR(92) + '%') OR (CAST(u.resource_target AS NVARCHAR(MAX)) LIKE '%$ref%' OR CAST(u.resource_target AS NVARCHAR(MAX)) LIKE '%$gen%' OR CAST(u.resource_target AS NVARCHAR(MAX)) LIKE '%' + CHAR(92) + '%') OR (CAST(u.existing_target AS NVARCHAR(MAX)) LIKE '%$ref%' OR CAST(u.existing_target AS NVARCHAR(MAX)) LIKE '%$gen%' OR CAST(u.existing_target AS NVARCHAR(MAX)) LIKE '%' + CHAR(92) + '%') OR COALESCE(u.reference_labels,'') NOT IN ('','null','{}','[]') OR COALESCE(u.provenance_records,'') NOT IN ('','null','{}','[]') OR COALESCE(u.resolved_root_digests,'') NOT IN ('','null','{}','[]') OR COALESCE(u.remaining_resolvables,'') NOT IN ('','null','{}','[]') OR EXISTS (SELECT 1 FROM resource_updates h WHERE h.ksuid=u.ksuid AND ((CAST(h.resource AS NVARCHAR(MAX)) LIKE '%$ref%' OR CAST(h.resource AS NVARCHAR(MAX)) LIKE '%$gen%' OR CAST(h.resource AS NVARCHAR(MAX)) LIKE '%' + CHAR(92) + '%') OR (CAST(h.existing_resource AS NVARCHAR(MAX)) LIKE '%$ref%' OR CAST(h.existing_resource AS NVARCHAR(MAX)) LIKE '%$gen%' OR CAST(h.existing_resource AS NVARCHAR(MAX)) LIKE '%' + CHAR(92) + '%') OR (CAST(h.resource_target AS NVARCHAR(MAX)) LIKE '%$ref%' OR CAST(h.resource_target AS NVARCHAR(MAX)) LIKE '%$gen%' OR CAST(h.resource_target AS NVARCHAR(MAX)) LIKE '%' + CHAR(92) + '%') OR (CAST(h.existing_target AS NVARCHAR(MAX)) LIKE '%$ref%' OR CAST(h.existing_target AS NVARCHAR(MAX)) LIKE '%$gen%' OR CAST(h.existing_target AS NVARCHAR(MAX)) LIKE '%' + CHAR(92) + '%') OR COALESCE(h.reference_labels,'') NOT IN ('','null','{}','[]') OR COALESCE(h.provenance_records,'') NOT IN ('','null','{}','[]') OR COALESCE(h.resolved_root_digests,'') NOT IN ('','null','{}','[]') OR COALESCE(h.remaining_resolvables,'') NOT IN ('','null','{}','[]'))))
UNION
SELECT 'admission:topology' AS guard_key WHERE EXISTS (SELECT 1 FROM resources h WITH (INDEX(idx_resources_command_id), FORCESEEK) JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON h.command_id=r.command_id WHERE (CAST(h.data AS NVARCHAR(MAX)) LIKE '%$ref%' OR CAST(h.data AS NVARCHAR(MAX)) LIKE '%$gen%' OR CAST(h.data AS NVARCHAR(MAX)) LIKE '%' + CHAR(92) + '%'))) keys ORDER BY guard_key;
OPEN keys_cursor; FETCH NEXT FROM keys_cursor INTO @key;
WHILE @@FETCH_STATUS=0 BEGIN
UPDATE admission_revisions WITH (UPDLOCK,HOLDLOCK) SET revision=revision+1 WHERE guard_key=@key;
IF @@ROWCOUNT=0 INSERT INTO admission_revisions(guard_key,revision) VALUES (@key,1);
FETCH NEXT FROM keys_cursor INTO @key; END; CLOSE keys_cursor; DEALLOCATE keys_cursor;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER admission_command_stacks_all ON command_stacks AFTER INSERT, UPDATE, DELETE AS
BEGIN
SET NOCOUNT ON;
IF NOT EXISTS (SELECT 1 FROM inserted) AND NOT EXISTS (SELECT 1 FROM deleted) RETURN;
DECLARE @label NVARCHAR(450), @key NVARCHAR(450);
DECLARE labels CURSOR LOCAL FAST_FORWARD FOR SELECT DISTINCT label FROM (SELECT r.stack_label AS label FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r WHERE NOT EXISTS (SELECT 1 FROM forma_commands c WHERE c.command_id=r.command_id AND c.command='sync' AND c.source IN ('synchronizer','discovery'))) labels WHERE label IS NOT NULL ORDER BY label;
OPEN labels; FETCH NEXT FROM labels INTO @label;
WHILE @@FETCH_STATUS=0 BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_stack_labels WHERE label=@label) BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_stack_labels WITH (UPDLOCK,HOLDLOCK) WHERE label=@label) INSERT INTO admission_stack_labels(label) VALUES (@label); END;
FETCH NEXT FROM labels INTO @label; END; CLOSE labels; DEALLOCATE labels;
DECLARE keys_cursor CURSOR LOCAL FAST_FORWARD FOR SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l JOIN (SELECT r.stack_label AS label FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r WHERE NOT EXISTS (SELECT 1 FROM forma_commands c WHERE c.command_id=r.command_id AND c.command='sync' AND c.source IN ('synchronizer','discovery'))) x ON l.label=x.label
UNION
SELECT CASE WHEN DATALENGTH(RTRIM(s.id)) BETWEEN 2 AND 256 AND RTRIM(s.id) COLLATE Latin1_General_BIN2 NOT LIKE '%[^A-Za-z0-9_-]%' THEN 'admission:stack:' + RTRIM(s.id) ELSE 'admission:stack:exceptional' END AS guard_key FROM stacks s WITH (INDEX(idx_stacks_label), FORCESEEK) JOIN (SELECT r.stack_label AS label FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r WHERE NOT EXISTS (SELECT 1 FROM forma_commands c WHERE c.command_id=r.command_id AND c.command='sync' AND c.source IN ('synchronizer','discovery'))) x ON s.label=x.label
UNION
SELECT CASE WHEN DATALENGTH(RTRIM(r.stack_id)) BETWEEN 2 AND 256 AND RTRIM(r.stack_id) COLLATE Latin1_General_BIN2 NOT LIKE '%[^A-Za-z0-9_-]%' THEN 'admission:stack:' + RTRIM(r.stack_id) ELSE 'admission:stack:exceptional' END FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r WHERE r.stack_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM forma_commands c WHERE c.command_id=r.command_id AND c.command='sync' AND c.source IN ('synchronizer','discovery'))) keys ORDER BY guard_key;
OPEN keys_cursor; FETCH NEXT FROM keys_cursor INTO @key;
WHILE @@FETCH_STATUS=0 BEGIN
UPDATE admission_revisions WITH (UPDLOCK,HOLDLOCK) SET revision=revision+1 WHERE guard_key=@key;
IF @@ROWCOUNT=0 INSERT INTO admission_revisions(guard_key,revision) VALUES (@key,1);
FETCH NEXT FROM keys_cursor INTO @key; END; CLOSE keys_cursor; DEALLOCATE keys_cursor;
END;
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER admission_resources_all;
DROP TRIGGER admission_stacks_all;
DROP TRIGGER admission_targets_all;
DROP TRIGGER admission_policies_all;
DROP TRIGGER admission_stack_policies_all;
DROP TRIGGER admission_generators_all;
DROP TRIGGER admission_resource_updates_all;
DROP TRIGGER admission_forma_commands_all;
DROP TRIGGER admission_command_stacks_all;
DROP TABLE admission_resource_uris;
DROP TABLE admission_stack_labels;
