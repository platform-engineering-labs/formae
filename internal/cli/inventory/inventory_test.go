// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package inventory

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateInventoryOptions(t *testing.T) {
	t.Run("max-results must be 0 or positive", func(t *testing.T) {
		opts := &InventoryOptions{
			MaxResults: -1,
		}
		err := validateInventoryOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "max-results must be 0 (unlimited) or a positive number", err.Error())
	})

	t.Run("output-consumer must be 'human' or 'machine'", func(t *testing.T) {
		opts := &InventoryOptions{
			MaxResults:     10,
			OutputConsumer: "invalid",
		}
		err := validateInventoryOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "output-consumer must be 'human' or 'machine'", err.Error())
	})

	t.Run("output-schema must be 'json' or 'yaml' for machine consumer", func(t *testing.T) {
		opts := &InventoryOptions{
			MaxResults:     10,
			OutputConsumer: "machine",
			OutputSchema:   "invalid",
		}
		err := validateInventoryOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "output-schema must be 'json' or 'yaml' for machine consumer", err.Error())
	})
}
