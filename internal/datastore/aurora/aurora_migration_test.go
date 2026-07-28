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
