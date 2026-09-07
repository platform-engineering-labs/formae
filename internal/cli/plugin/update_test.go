// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateUpdateOptions(t *testing.T) {
	t.Run("no packages is valid (means update all)", func(t *testing.T) {
		opts := &UpdateOptions{OutputConsumer: "human"}
		assert.NoError(t, validateUpdateOptions(opts))
	})

	t.Run("output-consumer must be 'human' or 'machine'", func(t *testing.T) {
		opts := &UpdateOptions{OutputConsumer: "invalid"}
		err := validateUpdateOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "output-consumer must be 'human' or 'machine'", err.Error())
	})

	t.Run("output-schema must be 'json' or 'yaml' for machine consumer", func(t *testing.T) {
		opts := &UpdateOptions{
			OutputConsumer: "machine",
			OutputSchema:   "invalid",
		}
		err := validateUpdateOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "output-schema must be either 'json' or 'yaml' for machine consumer", err.Error())
	})

	t.Run("machine consumer with yaml", func(t *testing.T) {
		opts := &UpdateOptions{
			OutputConsumer: "machine",
			OutputSchema:   "yaml",
		}
		assert.NoError(t, validateUpdateOptions(opts))
	})
}
