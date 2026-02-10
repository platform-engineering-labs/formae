// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package destroy

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/stretchr/testify/assert"
)

func TestValidateDestroyOptions(t *testing.T) {
	t.Run("either forma or query must be set", func(t *testing.T) {
		opts := &DestroyOptions{
			FormaFile:    "",
			Query:        "",
			OnDependents: OnDependentsAbort,
		}
		err := validateDestroyOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "either a forma file needs to be provided, or --query must be specified", err.Error())
	})

	t.Run("forma and query cannot both be set", func(t *testing.T) {
		opts := &DestroyOptions{
			FormaFile:    "example.pkl",
			Query:        "name=my-resource",
			OnDependents: OnDependentsAbort,
		}
		err := validateDestroyOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "either a forma file needs to be provided, or --query must be specified, but not both", err.Error())
	})

	t.Run("output-consumer should be human or machine", func(t *testing.T) {
		opts := &DestroyOptions{
			FormaFile:      "example.pkl",
			Query:          "",
			OutputConsumer: printer.Consumer("invalid_consumer"),
			OnDependents:   OnDependentsAbort,
		}
		err := validateDestroyOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "output consumer must be either 'human' or 'machine'", err.Error())
	})

	t.Run("output schema should be JSON or YAML for machine consumer", func(t *testing.T) {

		opts := &DestroyOptions{
			FormaFile:      "example.pkl",
			Query:          "",
			OutputConsumer: "machine",
			OutputSchema:   "invalid_schema",
			OnDependents:   OnDependentsAbort,
		}
		err := validateDestroyOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "output schema must be either 'json' or 'yaml' for machine consumer", err.Error())
	})

	t.Run("on-dependents must be abort or cascade", func(t *testing.T) {
		opts := &DestroyOptions{
			FormaFile:      "example.pkl",
			OutputConsumer: "human",
			OnDependents:   "invalid",
		}
		err := validateDestroyOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "--on-dependents must be either 'abort' or 'cascade'", err.Error())
	})

	t.Run("on-dependents accepts abort", func(t *testing.T) {
		opts := &DestroyOptions{
			FormaFile:      "example.pkl",
			OutputConsumer: "human",
			OnDependents:   OnDependentsAbort,
		}
		err := validateDestroyOptions(opts)
		assert.NoError(t, err)
	})

	t.Run("on-dependents accepts cascade", func(t *testing.T) {
		opts := &DestroyOptions{
			FormaFile:      "example.pkl",
			OutputConsumer: "human",
			OnDependents:   OnDependentsCascade,
		}
		err := validateDestroyOptions(opts)
		assert.NoError(t, err)
	})
}
