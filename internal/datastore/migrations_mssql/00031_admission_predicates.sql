-- © 2026 Platform Engineering Labs Inc.
-- SPDX-License-Identifier: FSL-1.1-ALv2
-- +goose Up
-- Resource predicate scopes are advanced under the existing URI serialization,
-- before the common sorted revision-lock pass. Full URI history participates.
-- Target identities use physical target equality, also conservatively covering
-- embedded Target values used by in-memory maps. SQL Server MAX retains long
-- JSON targets; registration uses a locked insert recheck, never truncation.
-- Resource identity uses the physical ksuid column, which loaders restore over JSON.
CREATE TABLE admission_inventory_targets (label NVARCHAR(MAX) NOT NULL, guard_key NVARCHAR(450) COLLATE Latin1_General_BIN2 NOT NULL PRIMARY KEY DEFAULT ('admission:inventory-target:'+CONVERT(VARCHAR(36),NEWID())));
CREATE TABLE admission_resource_ids (label NVARCHAR(450) COLLATE Latin1_General_BIN2 NOT NULL PRIMARY KEY, guard_key NVARCHAR(450) COLLATE Latin1_General_BIN2 NOT NULL UNIQUE DEFAULT ('admission:resource-id:'+CONVERT(VARCHAR(36),NEWID())));
-- +goose StatementBegin
ALTER TRIGGER admission_resources_all ON resources AFTER INSERT, UPDATE, DELETE AS
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
DECLARE @target NVARCHAR(MAX);
DECLARE target_labels CURSOR LOCAL FAST_FORWARD FOR SELECT DISTINCT label FROM (SELECT r.target AS label FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r UNION SELECT (SELECT value FROM OPENJSON(CASE WHEN ISJSON(r.data)=1 THEN r.data ELSE '{}' END) WITH (value NVARCHAR(MAX) '$.Target')) FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r UNION SELECT h.target FROM resources h WITH (READCOMMITTEDLOCK) JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON h.uri=r.uri UNION SELECT (SELECT value FROM OPENJSON(CASE WHEN ISJSON(h.data)=1 THEN h.data ELSE '{}' END) WITH (value NVARCHAR(MAX) '$.Target')) FROM resources h WITH (READCOMMITTEDLOCK) JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON h.uri=r.uri) scopes WHERE label IS NOT NULL ORDER BY label;
OPEN target_labels; FETCH NEXT FROM target_labels INTO @target;
WHILE @@FETCH_STATUS=0 BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_inventory_targets WHERE label=@target) BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_inventory_targets WITH (TABLOCKX,HOLDLOCK) WHERE label=@target) INSERT INTO admission_inventory_targets(label) VALUES (@target); END;
FETCH NEXT FROM target_labels INTO @target; END; CLOSE target_labels; DEALLOCATE target_labels;
DECLARE @identity NVARCHAR(450);
DECLARE identity_labels CURSOR LOCAL FAST_FORWARD FOR SELECT DISTINCT label FROM (SELECT r.ksuid AS label FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r UNION SELECT h.ksuid FROM resources h WITH (READCOMMITTEDLOCK) JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON h.uri=r.uri) scopes WHERE label IS NOT NULL ORDER BY label;
OPEN identity_labels; FETCH NEXT FROM identity_labels INTO @identity;
WHILE @@FETCH_STATUS=0 BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_resource_ids WHERE label=@identity) BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_resource_ids WITH (UPDLOCK,HOLDLOCK) WHERE label=@identity) INSERT INTO admission_resource_ids(label) VALUES (@identity); END;
FETCH NEXT FROM identity_labels INTO @identity; END; CLOSE identity_labels; DEALLOCATE identity_labels;
DECLARE labels CURSOR LOCAL FAST_FORWARD FOR SELECT DISTINCT label FROM (SELECT r.stack AS label FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r UNION SELECT JSON_VALUE(CASE WHEN ISJSON(r.data)=1 THEN r.data ELSE '{}' END, '$.Stack') FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r UNION SELECT h.stack FROM resources h WITH (READCOMMITTEDLOCK) JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON h.uri=r.uri UNION SELECT JSON_VALUE(CASE WHEN ISJSON(h.data)=1 THEN h.data ELSE '{}' END, '$.Stack') FROM resources h WITH (READCOMMITTEDLOCK) JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON h.uri=r.uri) labels WHERE label IS NOT NULL ORDER BY label;
OPEN labels; FETCH NEXT FROM labels INTO @label;
WHILE @@FETCH_STATUS=0 BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_stack_labels WHERE label=@label) BEGIN
IF NOT EXISTS (SELECT 1 FROM admission_stack_labels WITH (UPDLOCK,HOLDLOCK) WHERE label=@label) INSERT INTO admission_stack_labels(label) VALUES (@label); END;
FETCH NEXT FROM labels INTO @label; END; CLOSE labels; DEALLOCATE labels;
DECLARE keys_cursor CURSOR LOCAL FAST_FORWARD FOR SELECT guard_key FROM (SELECT l.guard_key FROM admission_stack_labels l WITH (READCOMMITTEDLOCK) JOIN (SELECT r.stack AS label FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r UNION SELECT JSON_VALUE(CASE WHEN ISJSON(r.data)=1 THEN r.data ELSE '{}' END, '$.Stack') FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r UNION SELECT h.stack FROM resources h WITH (READCOMMITTEDLOCK) JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON h.uri=r.uri UNION SELECT JSON_VALUE(CASE WHEN ISJSON(h.data)=1 THEN h.data ELSE '{}' END, '$.Stack') FROM resources h WITH (READCOMMITTEDLOCK) JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON h.uri=r.uri) x ON l.label=x.label
UNION
SELECT p.guard_key FROM admission_inventory_targets p WITH (READCOMMITTEDLOCK) JOIN (SELECT r.target AS label FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r UNION SELECT (SELECT value FROM OPENJSON(CASE WHEN ISJSON(r.data)=1 THEN r.data ELSE '{}' END) WITH (value NVARCHAR(MAX) '$.Target')) FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r UNION SELECT h.target FROM resources h WITH (READCOMMITTEDLOCK) JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON h.uri=r.uri UNION SELECT (SELECT value FROM OPENJSON(CASE WHEN ISJSON(h.data)=1 THEN h.data ELSE '{}' END) WITH (value NVARCHAR(MAX) '$.Target')) FROM resources h WITH (READCOMMITTEDLOCK) JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON h.uri=r.uri) x ON p.label=x.label
UNION
SELECT p.guard_key FROM admission_resource_ids p WITH (READCOMMITTEDLOCK) JOIN (SELECT r.ksuid AS label FROM (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r UNION SELECT h.ksuid FROM resources h WITH (READCOMMITTEDLOCK) JOIN (SELECT * FROM inserted UNION ALL SELECT * FROM deleted) r ON h.uri=r.uri) x ON p.label=x.label
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
-- +goose Down
-- +goose StatementBegin
ALTER TRIGGER admission_resources_all ON resources AFTER INSERT, UPDATE, DELETE AS
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
DROP TABLE admission_resource_ids;
DROP TABLE admission_inventory_targets;
