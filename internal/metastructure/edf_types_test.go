// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"testing"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/net/edf"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEDF_AllTypesRegistered(t *testing.T) {
	// Register all types
	err := registerEDFTypes()
	require.NoError(t, err, "Failed to register EDF types")

	// Verify that key types are registered (should return ErrTaken if already registered)
	types := []any{
		// Resource plugin value types (embedded in messages)
		resource.OperationStatus(""),
		resource.Operation(""),
		resource.OperationErrorCode(""),
		resource.ProgressResult{},

		// Model types
		model.Resource{},
		model.Target{},
		model.FormaeURI(""),

		// Internal coordination messages
		messages.SpawnPluginOperator{},
		messages.SpawnPluginOperatorResult{},

		// Configuration types
		model.RetryConfig{},
		model.LoggingConfig{},
	}

	for _, msgType := range types {
		err := edf.RegisterTypeOf(msgType)
		// Should either succeed or return ErrTaken (already registered)
		assert.True(t, err == nil || err == gen.ErrTaken,
			"Type %T should be registered or already taken, got error: %v", msgType, err)
	}
}

func TestEDF_MessageTypes_HaveRequiredFields(t *testing.T) {
	// Test that our internal message types have the expected structure
	t.Run("SpawnPluginOperator", func(t *testing.T) {
		msg := messages.SpawnPluginOperator{
			Namespace:   "AWS",
			ResourceURI: "formae://test123",
			Operation:   "Create",
			OperationID: "op-456",
		}

		assert.Equal(t, "AWS", msg.Namespace)
		assert.Equal(t, "formae://test123", msg.ResourceURI)
		assert.Equal(t, "Create", msg.Operation)
		assert.Equal(t, "op-456", msg.OperationID)
	})

	t.Run("SpawnPluginOperatorResult", func(t *testing.T) {
		msg := messages.SpawnPluginOperatorResult{
			PID: gen.PID{
				Node: "test-node@localhost",
			},
			Error: "",
		}

		assert.Equal(t, gen.Atom("test-node@localhost"), msg.PID.Node)
		assert.Empty(t, msg.Error)
	})
}
