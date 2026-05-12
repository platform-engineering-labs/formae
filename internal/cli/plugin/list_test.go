// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateListOptions(t *testing.T) {
	t.Run("output-consumer must be 'human' or 'machine'", func(t *testing.T) {
		opts := &ListOptions{OutputConsumer: "invalid"}
		err := validateListOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "output-consumer must be 'human' or 'machine'", err.Error())
	})

	t.Run("output-schema must be 'json' or 'yaml' for machine consumer", func(t *testing.T) {
		opts := &ListOptions{
			OutputConsumer: "machine",
			OutputSchema:   "invalid",
		}
		err := validateListOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "output-schema must be either 'json' or 'yaml' for machine consumer", err.Error())
	})

	t.Run("human consumer is valid", func(t *testing.T) {
		opts := &ListOptions{OutputConsumer: "human"}
		assert.NoError(t, validateListOptions(opts))
	})

	t.Run("machine consumer with json", func(t *testing.T) {
		opts := &ListOptions{
			OutputConsumer: "machine",
			OutputSchema:   "json",
		}
		assert.NoError(t, validateListOptions(opts))
	})
}
