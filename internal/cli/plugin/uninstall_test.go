// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateUninstallOptions(t *testing.T) {
	t.Run("missing packages", func(t *testing.T) {
		opts := &UninstallOptions{OutputConsumer: "human"}
		err := validateUninstallOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "at least one plugin name is required", err.Error())
	})

	t.Run("output-consumer must be 'human' or 'machine'", func(t *testing.T) {
		opts := &UninstallOptions{
			Packages:       []string{"aws"},
			OutputConsumer: "invalid",
		}
		err := validateUninstallOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "output-consumer must be 'human' or 'machine'", err.Error())
	})

	t.Run("output-schema must be 'json' or 'yaml' for machine consumer", func(t *testing.T) {
		opts := &UninstallOptions{
			Packages:       []string{"aws"},
			OutputConsumer: "machine",
			OutputSchema:   "invalid",
		}
		err := validateUninstallOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "output-schema must be either 'json' or 'yaml' for machine consumer", err.Error())
	})

	t.Run("machine consumer with json", func(t *testing.T) {
		opts := &UninstallOptions{
			Packages:       []string{"aws"},
			OutputConsumer: "machine",
			OutputSchema:   "json",
		}
		assert.NoError(t, validateUninstallOptions(opts))
	})
}
