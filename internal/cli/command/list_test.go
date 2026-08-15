// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package command

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore"
)

// TestListDefaultsToFifty verifies `command list` defaults --max-results to
// 50, replacing the old `status command` verb's default of 10.
func TestListDefaultsToFifty(t *testing.T) {
	sub, _, err := CommandCmd().Find([]string{"list"})
	require.NoError(t, err)
	f := sub.Flags().Lookup("max-results")
	require.NotNil(t, f)
	assert.Equal(t, "50", f.DefValue)
}

// TestListResolvesZeroToCeiling verifies an explicit --max-results 0 resolves
// to the server-side ceiling. This must happen before the statuswatch TUI
// model is constructed: its constructor rewrites any MaxResults <= 0 to 10,
// so resolving 0 upstream is the only way to honor "give me everything".
func TestListResolvesZeroToCeiling(t *testing.T) {
	got, err := resolveListMaxResults(0)
	require.NoError(t, err)
	assert.Equal(t, datastore.MaxFormaCommandsQueryLimit, got)
}

// TestNewListOptionsSetsHeaderCommand verifies `command list` sets the TUI
// header to the verb the user actually typed ("command list"), so a future
// change to newListOptions (or a revert to constructing StatusOptions inline
// without it) cannot make `command list` silently fall back to displaying
// the deprecated `status command` alias's name instead.
func TestNewListOptionsSetsHeaderCommand(t *testing.T) {
	assert.Equal(t, "command list", newListOptions().HeaderCommand)
}

// TestListRejectsNegativeMaxResults verifies a negative page size is
// rejected as a usage error rather than silently defaulted away.
func TestListRejectsNegativeMaxResults(t *testing.T) {
	_, err := resolveListMaxResults(-1)
	assert.Error(t, err, "a negative page size must be a usage error, not a silent default")
}

// TestListAcceptsPositiveMaxResultsUnchanged verifies an ordinary positive
// page size passes through unchanged.
func TestListAcceptsPositiveMaxResultsUnchanged(t *testing.T) {
	got, err := resolveListMaxResults(5)
	require.NoError(t, err)
	assert.Equal(t, 5, got)
}
