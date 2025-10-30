// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package cancel

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/stretchr/testify/assert"
)

func TestValidate_ValidOptions(t *testing.T) {
	opts := &CancelOptions{
		Query:          "stack:test",
		OutputConsumer: printer.ConsumerHuman,
		OutputSchema:   "json",
	}

	err := Validate(opts)
	assert.NoError(t, err)
}

func TestValidate_ValidMachineConsumer(t *testing.T) {
	opts := &CancelOptions{
		Query:          "",
		OutputConsumer: printer.ConsumerMachine,
		OutputSchema:   "yaml",
	}

	err := Validate(opts)
	assert.NoError(t, err)
}

func TestValidate_InvalidConsumer(t *testing.T) {
	opts := &CancelOptions{
		Query:          "",
		OutputConsumer: "invalid",
		OutputSchema:   "json",
	}

	err := Validate(opts)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "output consumer must be either 'human' or 'machine'")
}

func TestValidate_InvalidSchemaForMachine(t *testing.T) {
	opts := &CancelOptions{
		Query:          "",
		OutputConsumer: printer.ConsumerMachine,
		OutputSchema:   "xml",
	}

	err := Validate(opts)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "output schema must be either 'json' or 'yaml'")
}

func TestValidate_NoQueryIsValid(t *testing.T) {
	opts := &CancelOptions{
		Query:          "",
		OutputConsumer: printer.ConsumerHuman,
		OutputSchema:   "json",
	}

	err := Validate(opts)
	assert.NoError(t, err)
}
