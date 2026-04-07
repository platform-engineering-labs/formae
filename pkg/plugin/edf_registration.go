// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"time"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/net/edf"
	"github.com/platform-engineering-labs/formae/pkg/model"
)

// RegisterSharedEDFTypes registers the cross-node message types shared between
// the agent and plugins. Both sides must call this function to enable network
// serialization. Each message type has MarshalEDF/UnmarshalEDF methods that use
// MessagePack, so their nested types don't need separate registration.
//
// Additionally, types passed via Ergo process Env during remote spawns must be
// registered since they go through EDF directly (not wrapped in a message type).
func RegisterSharedEDFTypes() error {
	types := []any{
		// Types passed via Ergo Env during remote spawn (not in message types)
		time.Duration(0),
		model.RetryConfig{},

		// Cross-node message types (serialized via MarshalEDF/UnmarshalEDF)
		ReadResource{},
		CreateResource{},
		UpdateResource{},
		DeleteResource{},
		ListResources{},
		PluginOperatorCheckStatus{},
		TrackedProgress{},
		Listing{},
		PluginAnnouncement{},
		StartPluginOperation{},
		PluginOperatorShutdown{},
		ResumeWaitingForResource{},
		PluginOperatorRetry{},
	}

	for _, t := range types {
		if err := edf.RegisterTypeOf(t); err != nil && err != gen.ErrTaken {
			return err
		}
	}
	return nil
}
