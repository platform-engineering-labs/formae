// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package aurora

import (
	"strings"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/stretchr/testify/require"
)

func TestAdmissionMigrationFunctionBlocks(t *testing.T) {
	sql := `-- +goose Up
CREATE TABLE example (id INT);
-- +goose StatementBegin
CREATE FUNCTION guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
PERFORM 1;
RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER guard BEFORE INSERT ON example FOR EACH ROW EXECUTE FUNCTION guard();
-- +goose Down
DROP TABLE example;
`
	statements := parseGooseUp(sql)
	require.Len(t, statements, 3)
	require.True(t, strings.Contains(statements[1], "PERFORM 1;\nRETURN NEW;\nEND $$;"))
}

func TestScopedCommandUpdateMigrationParsesForAurora(t *testing.T) {
	raw, err := datastore.EmbedMigrationsPostgres.ReadFile("migrations_postgres/00034_scope_command_update.sql")
	require.NoError(t, err)
	statements := parseGooseUp(string(raw))
	require.Len(t, statements, 1)
	function := statements[0]
	require.Contains(t, function, "CREATE OR REPLACE FUNCTION admission_forma_commands_update()")
	require.Contains(t, function, "to_jsonb(OLD) - 'modified_ts'")
	require.Contains(t, function, "affected_resource_updates AS MATERIALIZED")
	require.Contains(t, function, "affected_resource_update_history AS MATERIALIZED")
	require.Contains(t, function, "affected_resources AS MATERIALIZED")
	require.Contains(t, function, `ORDER BY guard_key COLLATE "C"`)
	require.NotContains(t, function, "admission_forma_commands_insert")
}
