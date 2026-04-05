// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"ergo.services/ergo/gen"
	"ergo.services/ergo/net/edf"
)

// RegisterSharedEDFTypes registers the cross-node message types shared between
// the agent and plugins. Both sides must call this function to enable network
// serialization. Each type has MarshalEDF/UnmarshalEDF methods that use
// MessagePack, so nested/dependent types no longer need separate registration.
func RegisterSharedEDFTypes() error {
	types := []any{
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
