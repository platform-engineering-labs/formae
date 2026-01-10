// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"time"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/net/edf"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// RegisterSharedEDFTypes registers all EDF types shared between the agent and plugins.
// Both the agent and plugin processes must call this function to enable network
// serialization of messages.
//
// IMPORTANT: Types must be registered in dependency order!
// Types that are embedded in other types must be registered first.
func RegisterSharedEDFTypes() error {
	types := []any{
		// 1. Basic types
		time.Duration(0),

		// 2. Model types in dependency order
		// First: types with no external dependencies
		model.FormaeURI(""),
		model.FieldUpdateMethod(""),
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
		model.RetryConfig{},

		// Fifth: OTel config types (for passing to remote plugin spawns)
		model.OTLPConfig{},
		model.PrometheusConfig{},
		model.OTelConfig{}, // depends on OTLPConfig and PrometheusConfig

		// 3. Resource operation types (some depend on model types)
		resource.OperationStatus(""),
		resource.Operation(""),
		resource.OperationErrorCode(""),
		resource.ProgressResult{}, // depends on model.Resource
		resource.Resource{},

		// 4. Plugin types used in coordination messages
		// These must be registered BEFORE PluginAnnouncement
		ListParameter{},      // Used in ResourceDescriptor
		ResourceDescriptor{}, // Used in PluginAnnouncement
		ListParam{},          // Used in ListResources

		// 5. Filter types (must be registered before messages that use them)
		FilterAction(""),
		ConditionType(""),
		FilterCondition{},
		MatchFilter{},

		// 6. Plugin announcement (depends on ResourceDescriptor and filter types)
		PluginAnnouncement{},

		// 7. Plugin coordination messages
		GetFilters{},
		GetFiltersResponse{},

		// 8. Plugin operation messages (ResourceUpdater <-> PluginOperator)
		ReadResource{},
		CreateResource{},
		UpdateResource{},
		DeleteResource{},
		PluginOperatorCheckStatus{},
		ResumeWaitingForResource{},
		PluginOperatorRetry{},
		PluginOperatorShutdown{},
		ListResources{},
		ListedResource{},
		Listing{},
		StartPluginOperation{},

		// 9. TrackedProgress - sent from PluginOperator to ResourceUpdater
		// Must be registered after resource.ProgressResult since it embeds it
		TrackedProgress{},
	}

	for _, t := range types {
		if err := edf.RegisterTypeOf(t); err != nil && err != gen.ErrTaken {
			return err
		}
	}

	return nil
}
