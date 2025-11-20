// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"log/slog"
	"time"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/net/edf"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	internalTypes "github.com/platform-engineering-labs/formae/internal/metastructure/types"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// Internal message types for distributed plugin coordination

// GetPluginOperator is sent to PluginCoordinator to request a remote PluginOperator PID
type GetPluginOperator struct {
	Namespace   string // Plugin namespace (e.g., "AWS", "Azure", "FakeAWS")
	ResourceURI string // Resource identifier
	Operation   string // Operation type (e.g., "Create", "Read", "Update", "Delete")
	OperationID string // Unique operation identifier
}

// PluginOperatorPID is the response from PluginCoordinator containing a remote plugin operator PID
type PluginOperatorPID struct {
	PID gen.ProcessID // Remote process ID pointing to plugin's PluginOperator actor
}

// registerEDFTypes registers all types that will be serialized and sent over Ergo network
// Note: Request/Result types with pointers are handled by Ergo's Call mechanism and don't need
// explicit registration. Only value types that are embedded in messages need registration.
func registerEDFTypes() error {
	types := []any{
		// Basic types
		time.Duration(0),
		slog.Level(0),

		// Internal types (from old types.go)
		forma_command.CommandStatePending,
		model.FormaApplyModeReconcile,
		internalTypes.OperationType(""),
		internalTypes.ResourceUpdateState(""),
		internalTypes.FormaCommandSource(""),

		// Resource plugin enums and result types
		resource.OperationStatus(""),
		resource.Operation(""),
		resource.OperationErrorCode(""),
		resource.ProgressResult{},

		// Model types (dependencies for ProgressResult and other messages)
		model.FormaeURI(""),
		model.FieldHint{},
		model.Schema{},
		model.Resource{},
		model.Stack{},
		model.Target{},
		model.Prop{},
		model.Description{},
		model.Forma{},
		model.Command(""),

		// Configuration types
		config.FormaCommandConfig{},
		model.RetryConfig{},
		model.LoggingConfig{},

		// Internal messages (from old types.go)
		messages.MarkResourceUpdateAsComplete{},
		messages.UpdateResourceProgress{},

		// NEW: Internal coordination messages (agent <-> plugin coordinator)
		GetPluginOperator{},
		PluginOperatorPID{},
	}

	for _, t := range types {
		err := edf.RegisterTypeOf(t)
		if err != nil && err != gen.ErrTaken {
			return err
		}
	}

	return nil
}
