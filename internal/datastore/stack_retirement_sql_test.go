//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"strings"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/stretchr/testify/require"
)

type retirementQueryCapture struct{ query string }

func (c *retirementQueryCapture) Exec(string, ...any) error                       { return nil }
func (c *retirementQueryCapture) Store(*forma_command.FormaCommand, string) error { return nil }
func (c *retirementQueryCapture) Commit() error                                   { return nil }
func (c *retirementQueryCapture) Rollback() error                                 { return nil }
func (c *retirementQueryCapture) Query(query string, _ ...any) ([]string, error) {
	c.query = query
	return nil, nil
}

func TestExpiredCandidateQueryUsesDialectFirstAndBytewiseVersionOrdering(t *testing.T) {
	candidate := ExpiredStackInfo{StackID: "stack", StackLabel: "label", OnDependents: "abort", ExpiresAt: time.Now().UTC().Format(time.RFC3339)}
	for _, dialect := range []string{"sqlite", "postgres", "mssql"} {
		t.Run(dialect, func(t *testing.T) {
			capture := &retirementQueryCapture{}
			store := AdmissionStore{Dialect: dialect}
			matched, err := store.expiredCandidateStillMatches(capture, candidate)
			require.NoError(t, err)
			require.False(t, matched)
			require.True(t, strings.HasPrefix(capture.query, "WITH latest_policies AS"))
			switch dialect {
			case "mssql":
				require.Contains(t, capture.query, "SELECT TOP (1) CAST(1 AS VARCHAR(1))")
				require.Contains(t, capture.query, "ORDER BY p.version COLLATE Latin1_General_BIN2 DESC")
				require.NotContains(t, capture.query, " LIMIT 1")
			case "postgres":
				require.Contains(t, capture.query, `ORDER BY p.version COLLATE "C" DESC`)
				require.True(t, strings.HasSuffix(capture.query, " LIMIT 1"))
			default:
				require.Contains(t, capture.query, "ORDER BY p.version DESC")
				require.True(t, strings.HasSuffix(capture.query, " LIMIT 1"))
			}
		})
	}
}
