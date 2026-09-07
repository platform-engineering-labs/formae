// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package cli

import (
	"errors"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
)

// isUsageError must classify bad flags, bad args, and unknown commands as usage
// errors (so the CLI prints command usage), while leaving genuine runtime errors
// alone.
func TestIsUsageError(t *testing.T) {
	usage := []error{
		cmd.FlagErrorf("max-results must be a positive number"),
		errors.New(`unknown flag: --mode`),
		errors.New(`unknown shorthand flag: 'x' in -x`),
		errors.New(`unknown command "foo" for "formae"`),
		errors.New(`flag needs an argument: --query`),
		errors.New(`invalid argument "abc" for "--count" flag: strconv.ParseInt: parsing "abc"`),
		errors.New(`required flag(s) "query" not set`),
		errors.New(`accepts 1 arg(s), received 2`),
		errors.New(`requires at least 1 arg(s), received 0`),
	}
	for _, err := range usage {
		if !isUsageError(err) {
			t.Errorf("expected usage error, got runtime for: %v", err)
		}
	}

	runtimeErrs := []error{
		errors.New("agent is not running; please start the agent and try again"),
		errors.New("incompatible agent version: expected 0.88.0, got 0.87.1"),
		errors.New("error fetching stats from agent: connection refused"),
	}
	for _, err := range runtimeErrs {
		if isUsageError(err) {
			t.Errorf("expected runtime error, got usage for: %v", err)
		}
	}
}
