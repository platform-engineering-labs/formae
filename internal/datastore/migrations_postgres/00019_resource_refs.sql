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
ALTER TABLE resources ADD COLUMN refs text[] NOT NULL DEFAULT '{}';
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_resources_refs ON resources USING GIN (refs);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_resources_refs;
ALTER TABLE resources DROP COLUMN refs;
