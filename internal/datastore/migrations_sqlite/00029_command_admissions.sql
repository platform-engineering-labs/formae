-- © 2026 Platform Engineering Labs Inc.
-- SPDX-License-Identifier: FSL-1.1-ALv2
-- +goose Up
-- Internal primitive only: authoritative-writer triggers follow separately.
CREATE TABLE admission_revisions (
 guard_key TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
 revision BIGINT NOT NULL CHECK (revision >= 0)
);
-- No FK cascade: lifecycle command replacement/deletion must not erase replay.
CREATE TABLE command_admissions (
 principal_scope TEXT COLLATE BINARY NOT NULL,
 idempotency_key TEXT COLLATE BINARY NOT NULL,
 command_id TEXT COLLATE BINARY NOT NULL,
 request_digest VARCHAR(64) NOT NULL,
 receipt TEXT NOT NULL,
 PRIMARY KEY (principal_scope, idempotency_key)
);
CREATE UNIQUE INDEX command_admissions_command_id_idx ON command_admissions(command_id) WHERE command_id <> '';
-- +goose Down
DROP TABLE command_admissions;
DROP TABLE admission_revisions;
