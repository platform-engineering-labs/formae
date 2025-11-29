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
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// Internal message types for distributed plugin coordination are now in messages/plugin.go

// registerEDFTypes registers all types that will be serialized and sent over Ergo network
// Note: Request/Result types with pointers are handled by Ergo's Call mechanism and don't need
// explicit registration. Only value types that are embedded in messages need registration.
func registerEDFTypes() error {
	// IMPORTANT: Types must be registered in dependency order!
	// Types that are embedded in other types must be registered first.
	types := []any{
		// 1. Basic types
		time.Duration(0),
		slog.Level(0),

		// 2. Internal enum types (no dependencies)
		forma_command.CommandStatePending,
		model.FormaApplyModeReconcile,
		internalTypes.OperationType(""),
		internalTypes.ResourceUpdateState(""),
		internalTypes.FormaCommandSource(""),

		// 3. Model types in dependency order
		// First: types with no external dependencies
		model.FormaeURI(""),
		model.FieldHint{},
		model.Description{},
		model.Prop{},
		model.Command(""),

		// Second: Schema depends on FieldHint (map[string]FieldHint)
		model.Schema{},

		// Third: Resource depends on Schema
		model.Resource{},

		// Fourth: Target and other model types
		model.Target{},
		model.Stack{},
		model.Forma{},

		// 4. Resource operation types (some may depend on model types)
		resource.OperationStatus(""),
		resource.Operation(""),
		resource.OperationErrorCode(""),
		resource.ProgressResult{}, // depends on model.Resource
		resource.Resource{},

		// 5. Configuration types
		config.FormaCommandConfig{},
		model.RetryConfig{},
		model.LoggingConfig{},
		model.SynchronizationConfig{},
		model.DiscoveryConfig{},
		model.ServerConfig{},
		model.SqliteConfig{},
		model.PostgresConfig{},
		model.DatastoreConfig{},
		model.PluginConfig{},
		model.OTLPConfig{},
		model.OTelConfig{},

		// 6. Internal messages
		messages.MarkResourceUpdateAsComplete{},
		messages.UpdateResourceProgress{},

		// 7. Plugin types used in coordination messages
		// These must be registered BEFORE PluginAnnouncement and PluginInfoResponse
		plugin.ListParameter{},      // Used in ResourceDescriptor
		plugin.ResourceDescriptor{}, // Used in PluginAnnouncement and PluginInfoResponse
		plugin.ListParam{},          // Used in ListResources

		// Filter types (must be registered before messages that use them)
		plugin.FilterAction(""),
		plugin.ConditionType(""),
		plugin.FilterCondition{},
		plugin.MatchFilter{},

		// Slice and map types for PluginAnnouncement and PluginInfoResponse
		[]plugin.MatchFilter{},
		[]plugin.ResourceDescriptor{},
		[]plugin.FilterCondition{},
		map[string]model.Schema{},

		// 8. Plugin coordination messages (agent <-> plugins)
		// These must come AFTER all their component types
		messages.PluginAnnouncement{},
		messages.PluginHeartbeat{},
		messages.SpawnPluginOperator{},
		messages.SpawnPluginOperatorResult{},
		messages.GetPluginInfo{},
		messages.PluginInfoResponse{},

		// 9. Plugin operation messages (ResourceUpdater <-> PluginOperator)
		plugin.ReadResource{},
		plugin.CreateResource{},
		plugin.UpdateResource{},
		plugin.DeleteResource{},
		plugin.PluginOperatorCheckStatus{},
		plugin.ResumeWaitingForResource{},
		plugin.PluginOperatorRetry{},
		plugin.PluginOperatorShutdown{},
		plugin.ListResources{},
		plugin.Listing{},
		plugin.StartPluginOperation{},
	}

	for _, t := range types {
		err := edf.RegisterTypeOf(t)
		if err != nil && err != gen.ErrTaken {
			return err
		}
	}

	return nil
}
