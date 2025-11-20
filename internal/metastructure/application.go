// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	"github.com/platform-engineering-labs/formae/internal/metastructure/plugin_coordinator"
	"github.com/platform-engineering-labs/formae/internal/metastructure/plugin_operation"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
)

func CreateApplication() gen.ApplicationBehavior {
	return &Application{}
}

type Application struct{}

// Load invoked on loading application using method ApplicationLoad of gen.Node interface.
func (app *Application) Load(node gen.Node, args ...any) (gen.ApplicationSpec, error) {
	return gen.ApplicationSpec{
		Name:        "Application",
		Description: "Orchestrator application",
		Mode:        gen.ApplicationModePermanent,
		Group: []gen.ApplicationMemberSpec{
			{
				Name:    "PluginOperatorSupervisor",
				Factory: plugin_operation.NewPluginOperatorSupervisor,
			},
			{
				Name:    "ResourceUpdaterSupervisor",
				Factory: resource_update.NewResourceUpdaterSupervisor,
			},
			{
				Name:    "ChangesetSupervisor",
				Factory: changeset.NewChangesetSupervisor,
			},
			{
				Name:    "PluginCoordinator",
				Factory: plugin_coordinator.NewPluginCoordinator,
			},
			{
				Name:    "MetastructureSupervisor",
				Factory: newSupervisor,
			},
		},
	}, nil
}

// Start invoked once the application started
func (app *Application) Start(mode gen.ApplicationMode) {}

// Terminate invoked once the application stopped
func (app *Application) Terminate(reason error) {}
