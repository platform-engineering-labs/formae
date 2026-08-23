// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connect

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// These pin the command's shape: which flags live where, what bare connect
// does without a TTY, and that argv mistakes never produce an envelope.

func runConnect(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	c := ConnectCmd()
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs(args)
	err := c.Execute()
	return out.String(), err
}

func findSub(t *testing.T, parent *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, sub := range parent.Commands() {
		if sub.Name() == name {
			return sub
		}
	}
	t.Fatalf("subcommand %q not registered", name)
	return nil
}

// The aws flags belong to `connect aws` alone: the parent is the cloud
// dispatcher and carries only the shared profile selection.
func TestStructure_AwsFlagsLiveOnTheSubcommandOnly(t *testing.T) {
	parent := ConnectCmd()
	aws := findSub(t, parent, "aws")

	for _, flag := range []string{"account", "quick-create", "provider-exists", "profile-aws", "region", "role-arn", "no-input"} {
		assert.NotNil(t, aws.Flags().Lookup(flag), "flag %q missing on connect aws", flag)
		assert.Nil(t, parent.Flags().Lookup(flag), "flag %q must not exist on the parent", flag)
	}

	// --config/--profile are persistent on the parent, so the resume hint's
	// flag placement (`formae connect --profile <p> aws ...`) parses.
	assert.NotNil(t, parent.PersistentFlags().Lookup("config"))
	assert.NotNil(t, parent.PersistentFlags().Lookup("profile"))
}

// `connect list` is a member-readable listing, not a provisioning flow: it
// owns the shared output flags, takes no positional arguments, and carries
// none of the AWS-only flags that only `connect aws` needs.
func TestStructure_ListIsRegisteredAndCarriesNoAWSOnlyFlags(t *testing.T) {
	parent := ConnectCmd()
	list := findSub(t, parent, "list")

	assert.NotNil(t, list.Flags().Lookup("output-consumer"), "list must own the output-consumer flag")
	assert.NotNil(t, list.Flags().Lookup("output-schema"), "list must own the output-schema flag")

	for _, flag := range []string{"account", "quick-create", "provider-exists", "role-arn", "profile-aws", "region", "no-input"} {
		assert.Nil(t, list.Flags().Lookup(flag), "flag %q must not exist on connect list", flag)
	}
}

// A positional argument is rejected: list takes none.
func TestStructure_ListTakesNoPositionalArguments(t *testing.T) {
	out, err := runConnect(t, "list", "unexpected-arg")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected-arg")
	assert.NotContains(t, out, "schemaVersion")
}

func TestStructure_ConnectIsAnAuthCommand(t *testing.T) {
	c := ConnectCmd()
	assert.Equal(t, "Auth", c.Annotations["type"])
}

// Bare connect is the interactive form, and a form needs a TTY. Without one
// the error says what to pass instead of prompting into a pipe.
func TestStructure_BareConnectWithoutATTYErrors(t *testing.T) {
	restore := isInteractive
	isInteractive = func() bool { return false }
	t.Cleanup(func() { isInteractive = restore })

	out, err := runConnect(t)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--no-input")
	assert.NotContains(t, out, "schemaVersion")
}

// On a TTY, bare connect runs the form through the seam.
func TestStructure_BareConnectOnATTYReachesTheFormSeam(t *testing.T) {
	restoreTTY := isInteractive
	isInteractive = func() bool { return true }
	t.Cleanup(func() { isInteractive = restoreTTY })

	called := false
	restoreForm := runConnectFormFn
	runConnectFormFn = func(_ *theme.Theme, _ *formValues, _ []string) error {
		called = true
		return assert.AnError
	}
	t.Cleanup(func() { runConnectFormFn = restoreForm })

	_, err := runConnect(t)

	require.Error(t, err)
	assert.True(t, called, "bare connect on a TTY must run the form")
}

// --config and --profile are one selection; both at once is a contradiction.
func TestStructure_ConfigAndProfileAreMutuallyExclusive(t *testing.T) {
	restore := isInteractive
	isInteractive = func() bool { return true }
	t.Cleanup(func() { isInteractive = restore })

	_, err := runConnect(t, "--config", "/tmp/x.pkl", "--profile", "p")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--config")
	assert.Contains(t, err.Error(), "--profile")

	_, err = runConnect(t, "aws", "--config", "/tmp/x.pkl", "--profile", "p",
		"--account", "123456789012", "--quick-create", "--no-input")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--config")
}

// Argv the command cannot parse fails before the flags that say how to render
// a failure are established, so it exits non-zero without an envelope.
func TestStructure_ArgvErrorsAreNotEnvelopes(t *testing.T) {
	machine := func(args ...string) []string {
		return append(args, "--output-consumer", "machine", "--output-schema", "json")
	}
	for _, args := range [][]string{
		machine("aws", "unexpected-arg"),
		{"aws", "--output-consumer", "sideways"},
		machine("aws", "--no-such-flag"),
	} {
		out, err := runConnect(t, args...)
		if err == nil {
			t.Fatalf("%v should fail: %s", args, out)
		}
		if strings.Contains(out, `"schemaVersion"`) {
			t.Fatalf("%v produced an envelope: %s", args, out)
		}
	}
}

// Flag-set mistakes on `connect aws` are argv errors too: a FlagError, not an
// envelope, whatever the output flags say.
func TestStructure_FlagValidationErrorsAreNotEnvelopes(t *testing.T) {
	out, err := runConnect(t, "aws",
		"--account", "123456789012", "--quick-create", "--role-arn", "arn:aws:iam::123456789012:role/r",
		"--output-consumer", "machine", "--output-schema", "json")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
	assert.NotContains(t, out, "schemaVersion")
}
