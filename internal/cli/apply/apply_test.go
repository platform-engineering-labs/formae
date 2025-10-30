// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package apply

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateApplyOptions(t *testing.T) {
	t.Run("missing forma file", func(t *testing.T) {
		opts := &ApplyOptions{
			FormaFile: "",
		}
		err := validateApplyOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "forma file is required", err.Error())
	})

	t.Run("missing mode", func(t *testing.T) {
		opts := &ApplyOptions{
			FormaFile: "example.pkl",
			Mode:      "",
		}
		err := validateApplyOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "the --mode flag is required", err.Error())
	})

	t.Run("invalid mode", func(t *testing.T) {
		opts := &ApplyOptions{
			FormaFile: "example.pkl",
			Mode:      "invalid_mode",
		}
		err := validateApplyOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "invalid mode: invalid_mode. Should be either reconcile or patch", err.Error())
	})

	t.Run("output consumer should be human or machine", func(t *testing.T) {
		opts := &ApplyOptions{
			FormaFile:      "example.pkl",
			Mode:           "reconcile",
			OutputConsumer: "invalid_consumer",
		}
		err := validateApplyOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "output consumer must be either 'human' or 'machine'", err.Error())
	})

	t.Run("output schema should be JSON or YAML for machine consumer", func(t *testing.T) {
		opts := &ApplyOptions{
			FormaFile:      "example.pkl",
			Mode:           "reconcile",
			OutputConsumer: "machine",
			OutputSchema:   "invalid_schema",
		}
		err := validateApplyOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "output schema must be either 'json' or 'yaml' for machine consumer", err.Error())
	})
}
