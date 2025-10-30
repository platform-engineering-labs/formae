// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package eval

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
)

func TestValidateEvalOptions(t *testing.T) {
	t.Run("forma file is required", func(t *testing.T) {
		opts := &EvalOptions{
			FormaFile:      "",
			Mode:           pkgmodel.FormaApplyModePatch,
			OutputConsumer: printer.ConsumerHuman,
			OutputSchema:   "json",
			Beautify:       true,
			Colorize:       true,
			Properties:     map[string]string{},
		}
		assert.EqualError(t, validateEvalOptions(opts), "forma file is required")
	})

	t.Run("invalid mode", func(t *testing.T) {
		opts := &EvalOptions{
			FormaFile:      "test.forma",
			Mode:           "invalid",
			OutputConsumer: printer.ConsumerHuman,
			OutputSchema:   "json",
			Beautify:       true,
			Colorize:       true,
			Properties:     map[string]string{},
		}
		assert.EqualError(t, validateEvalOptions(opts), "mode must be 'patch' or 'reconcile'")
	})

	t.Run("invalid output consumer", func(t *testing.T) {
		opts := &EvalOptions{
			FormaFile:      "test.forma",
			Mode:           pkgmodel.FormaApplyModePatch,
			OutputConsumer: "invalid",
			OutputSchema:   "json",
			Beautify:       true,
			Colorize:       true,
			Properties:     map[string]string{},
		}
		assert.EqualError(t, validateEvalOptions(opts), "output-consumer must be 'human' or 'machine'")
	})

	t.Run("invalid output schema for machine consumer", func(t *testing.T) {
		opts := &EvalOptions{
			FormaFile:      "test.forma",
			Mode:           pkgmodel.FormaApplyModePatch,
			OutputConsumer: printer.ConsumerMachine,
			OutputSchema:   "invalid",
			Beautify:       true,
			Colorize:       true,
			Properties:     map[string]string{},
		}
		assert.EqualError(t, validateEvalOptions(opts), "output-schema must be 'json' or 'yaml' for machine consumer")
	})
}
