-- © 2026 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose Up
-- data_migrations records which one-time data repairs have run against which
-- targets, so a repair that mutates rows runs at most once per target and a
-- completed repair stops costing a scan on every boot.
--
-- Keyed by target INCARNATION, not by label: deleting a target hard-removes all
-- its versions and re-creating it reuses the label with a fresh incarnation, so
-- a label-keyed row would wrongly mark a new target as already processed. A
-- fresh incarnation causes a rescan, which is the safe direction.
--
-- outcome records what was decided: WIPED (rows tombstoned for re-ingest),
-- CLEAN (scanned, nothing to repair), or PROCESSED-EXCLUDED (repairable but
-- deliberately skipped, needing operator action). All three mean "do not look
-- at this incarnation again". A target that is only deferred gets NO row, so it
-- is retried on the next boot.
--
-- The global completion row uses the reserved empty pair for target_label and
-- target_incarnation_id, with outcome COMPLETED: once every current incarnation
-- has a row, that row is written and later boots skip the scan entirely.
-- +goose StatementBegin
IF NOT EXISTS (SELECT 1 FROM sys.tables WHERE name = 'data_migrations')
BEGIN
    CREATE TABLE data_migrations (
        migration_key         nvarchar(450) COLLATE Latin1_General_BIN2 NOT NULL,
        target_label          nvarchar(450) COLLATE Latin1_General_BIN2 NOT NULL,
        target_incarnation_id nvarchar(450) COLLATE Latin1_General_BIN2 NOT NULL,
        outcome               nvarchar(450) NOT NULL,
        created_at            datetime2 DEFAULT SYSUTCDATETIME(),
        PRIMARY KEY (migration_key, target_label, target_incarnation_id)
    );
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
IF EXISTS (SELECT 1 FROM sys.tables WHERE name = 'data_migrations')
BEGIN
    DROP TABLE data_migrations;
END;
-- +goose StatementEnd
