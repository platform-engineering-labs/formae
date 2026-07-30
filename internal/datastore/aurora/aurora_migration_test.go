// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package aurora

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/datastore"
)

func TestHasNoTransactionDirective(t *testing.T) {
	t.Run("returns true for content containing the directive", func(t *testing.T) {
		content := "-- +goose NO TRANSACTION\n-- +goose Up\nSELECT 1;\n"
		if !hasNoTransactionDirective(content) {
			t.Error("expected true for content with NO TRANSACTION directive, got false")
		}
	})

	t.Run("returns false for a normal migration without the directive", func(t *testing.T) {
		content := "-- +goose Up\nSELECT 1;\n-- +goose Down\nSELECT 0;\n"
		if hasNoTransactionDirective(content) {
			t.Error("expected false for content without NO TRANSACTION directive, got true")
		}
	})

	t.Run("returns true for 00019_resource_refs.sql", func(t *testing.T) {
		raw, err := datastore.EmbedMigrationsPostgres.ReadFile("migrations_postgres/00019_resource_refs.sql")
		if err != nil {
			t.Fatalf("failed to read 00019_resource_refs.sql: %v", err)
		}
		if !hasNoTransactionDirective(string(raw)) {
			t.Error("expected true for 00019_resource_refs.sql, got false")
		}
	})

	t.Run("returns false for 00018_target_reap_audit.sql", func(t *testing.T) {
		raw, err := datastore.EmbedMigrationsPostgres.ReadFile("migrations_postgres/00018_target_reap_audit.sql")
		if err != nil {
			t.Fatalf("failed to read 00018_target_reap_audit.sql: %v", err)
		}
		if hasNoTransactionDirective(string(raw)) {
			t.Error("expected false for 00018_target_reap_audit.sql, got true")
		}
	})
}

func TestStripConcurrently(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "create index concurrently",
			in:   "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_resources_refs ON resources USING GIN (refs)",
			want: "CREATE INDEX IF NOT EXISTS idx_resources_refs ON resources USING GIN (refs)",
		},
		{
			name: "drop index concurrently",
			in:   "DROP INDEX CONCURRENTLY IF EXISTS idx_resources_refs",
			want: "DROP INDEX IF EXISTS idx_resources_refs",
		},
		{
			name: "lowercase keyword",
			in:   "create index concurrently idx on t (c)",
			want: "create index idx on t (c)",
		},
		{
			name: "statement without the keyword is unchanged",
			in:   "ALTER TABLE resources ADD COLUMN IF NOT EXISTS refs text[] NOT NULL DEFAULT '{}'",
			want: "ALTER TABLE resources ADD COLUMN IF NOT EXISTS refs text[] NOT NULL DEFAULT '{}'",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripConcurrently(tc.in); got != tc.want {
				t.Errorf("stripConcurrently(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
