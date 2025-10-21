// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit
// +build unit

package status

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/stretchr/testify/assert"
)

func TestValidateStatusOptions(t *testing.T) {
	t.Run("output-consumer should be human or machine", func(t *testing.T) {
		opts := &StatusOptions{
			OutputConsumer: printer.Consumer("invalid_consumer"),
		}
		err := validateStatusOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "output consumer must be either 'human' or 'machine'", err.Error())
	})

	t.Run("output schema should be JSON or YAML for machine consumer", func(t *testing.T) {
		opts := &StatusOptions{
			OutputConsumer: "machine",
			OutputSchema:   "invalid_schema",
		}
		err := validateStatusOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "output schema must be either 'json' or 'yaml' for machine consumer", err.Error())
	})

	t.Run("output layout should be detailed or summary", func(t *testing.T) {
		opts := &StatusOptions{
			OutputConsumer: "human",
			OutputLayout:   StatusOutput("invalid_layout"),
		}
		err := validateStatusOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "output layout must be either 'detailed' or 'summary'", err.Error())
	})
}
