-- © 2025 Platform Engineering Labs Inc.
--
-- SPDX-License-Identifier: FSL-1.1-ALv2

-- +goose NO TRANSACTION
-- +goose Up
-- Adds a refs column to resources for storing outbound reference KSUIDs on
-- each row. The column is used by indexed cascade-lookup queries to replace
-- full-table regex scans; the GIN index makes those lookups O(log n) instead
-- of O(n). The DEFAULT '{}' addition is metadata-only and does not rewrite
-- existing rows. The index is built CONCURRENTLY to avoid taking a long
-- exclusive lock on a large live table; CONCURRENTLY cannot run inside a
-- transaction block, so this migration is marked NO TRANSACTION.
--
-- Because each statement commits independently under NO TRANSACTION, the
-- migration version is only recorded once all statements succeed. Every
-- statement is therefore idempotent so a partial application (e.g. an
-- interrupted CONCURRENTLY build) can be re-run cleanly: the column add uses
-- IF NOT EXISTS, and the index is dropped-if-exists first to discard any
-- INVALID index left behind by an interrupted build before rebuilding it.
ALTER TABLE resources ADD COLUMN IF NOT EXISTS refs text[] NOT NULL DEFAULT '{}';
DROP INDEX CONCURRENTLY IF EXISTS idx_resources_refs;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_resources_refs ON resources USING GIN (refs);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_resources_refs;
ALTER TABLE resources DROP COLUMN IF EXISTS refs;
