// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package aurora

import (
	"strings"
	"testing"

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
