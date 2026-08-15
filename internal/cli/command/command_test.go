// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package command

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/agent"
	"github.com/platform-engineering-labs/formae/internal/cli/status"
)

// TestCommandStatusHasNoMaxResultsFlag verifies `command status` returns a
// single command by definition and therefore never offers --max-results.
func TestCommandStatusHasNoMaxResultsFlag(t *testing.T) {
	sub, _, err := CommandCmd().Find([]string{"status"})
	require.NoError(t, err)
	assert.Nil(t, sub.Flags().Lookup("max-results"),
		"command status returns a single command and must not offer --max-results")
	assert.Nil(t, sub.Flags().Lookup("query"),
		"command status returns a single command and must not offer --query")
}

// TestCommandStatusTakesPositionalID verifies `command status` accepts an
// optional single positional command id and rejects more than one.
func TestCommandStatusTakesPositionalID(t *testing.T) {
	sub, _, err := CommandCmd().Find([]string{"status"})
	require.NoError(t, err)
	if err := sub.Args(sub, []string{"3Hrx15wROBJnYK2T5oEXKErKMVf"}); err != nil {
		t.Fatalf("a single positional id must be accepted: %v", err)
	}
	if err := sub.Args(sub, []string{"a", "b"}); err == nil {
		t.Fatal("two positional ids must be rejected")
	}
	if err := sub.Args(sub, []string{}); err != nil {
		t.Fatalf("no positional id must be accepted (id is optional): %v", err)
	}
}

// TestCommandListKeepsQueryAndMaxResults verifies `command list` retains the
// full query/max-results surface `status command` used to offer.
func TestCommandListKeepsQueryAndMaxResults(t *testing.T) {
	sub, _, err := CommandCmd().Find([]string{"list"})
	require.NoError(t, err)
	assert.NotNil(t, sub.Flags().Lookup("query"), "command list must keep --query")
	assert.NotNil(t, sub.Flags().Lookup("max-results"), "command list must keep --max-results")
}

// TestOutputLayoutSurvivesOnBothSubcommands verifies --output-layout is
// offered on both the single-result and list subcommands.
func TestOutputLayoutSurvivesOnBothSubcommands(t *testing.T) {
	for _, name := range []string{"status", "list"} {
		sub, _, err := CommandCmd().Find([]string{name})
		require.NoError(t, err)
		assert.NotNil(t, sub.Flags().Lookup("output-layout"),
			"%s must keep --output-layout", name)
	}
}

// TestQueryFlagHasNoSpaceDefault verifies the surviving --query flag no
// longer carries the meaningless single-space default value.
func TestQueryFlagHasNoSpaceDefault(t *testing.T) {
	sub, _, err := CommandCmd().Find([]string{"list"})
	require.NoError(t, err)
	f := sub.Flags().Lookup("query")
	require.NotNil(t, f)
	assert.Equal(t, "", f.DefValue, "the --query default must not be a single space")
}

// TestDeprecatedAliasesAreMarkedIndividually verifies `status`, `status
// command` and `status agent` each carry their own Deprecated string, since
// cobra does not propagate Deprecated from a parent to its children.
func TestDeprecatedAliasesAreMarkedIndividually(t *testing.T) {
	statusCmd := status.StatusCmd()
	assert.NotEmpty(t, statusCmd.Deprecated, "`status` must be marked deprecated")

	sub, _, err := statusCmd.Find([]string{"command"})
	require.NoError(t, err)
	assert.NotEmpty(t, sub.Deprecated, "`status command` must be marked deprecated")

	sub, _, err = statusCmd.Find([]string{"agent"})
	require.NoError(t, err)
	assert.NotEmpty(t, sub.Deprecated, "`status agent` must be marked deprecated")

	// The new homes must NOT be deprecated.
	assert.Empty(t, CommandCmd().Deprecated, "the new `command` noun must not be deprecated")
	newStatus, _, err := CommandCmd().Find([]string{"status"})
	require.NoError(t, err)
	assert.Empty(t, newStatus.Deprecated, "the new `command status` must not be deprecated")
	newList, _, err := CommandCmd().Find([]string{"list"})
	require.NoError(t, err)
	assert.Empty(t, newList.Deprecated, "the new `command list` must not be deprecated")

	agentStatus, _, err := agent.AgentCmd().Find([]string{"status"})
	require.NoError(t, err)
	assert.Empty(t, agentStatus.Deprecated, "the new `agent status` must not be deprecated")
}

// TestStatusCommandCompatQueryStillWorks verifies the deprecated `status
// command` alias still accepts --query, routing it to the new list-style
// status path instead of failing with an unknown-flag error.
func TestStatusCommandCompatQueryStillWorks(t *testing.T) {
	sub, _, err := status.StatusCmd().Find([]string{"command"})
	require.NoError(t, err)
	require.NoError(t, sub.Flags().Set("query", "id:X"))
	f := sub.Flags().Lookup("query")
	require.NotNil(t, f, "the deprecated `status command` must still accept --query")
	assert.Equal(t, "id:X", f.Value.String())
}
