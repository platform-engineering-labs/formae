// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"ergo.services/ergo/gen"
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
				Name:    "Supervisor",
				Factory: newSupervisor,
			},
		},
	}, nil
}

// Start invoked once the application started
func (app *Application) Start(mode gen.ApplicationMode) {}

// Terminate invoked once the application stopped
func (app *Application) Terminate(reason error) {}
