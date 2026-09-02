// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"errors"

	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
)

func CreateApplication(onAbnormalStop func(reason error)) gen.ApplicationBehavior {
	return &Application{onAbnormalStop: onAbnormalStop}
}

type Application struct {
	// onAbnormalStop is invoked from Terminate when the application stops for
	// any reason other than a deliberate shutdown. The application is
	// permanent: when it stops, every actor under it is gone, and only the
	// process owner can turn that into an exit.
	onAbnormalStop func(reason error)
}

// Load invoked on loading application using method ApplicationLoad of gen.Node interface.
func (app *Application) Load(node gen.Node, args ...any) (gen.ApplicationSpec, error) {
	return gen.ApplicationSpec{
		Name:        "Application",
		Description: "Orchestrator application",
		Mode:        gen.ApplicationModePermanent,
		Group: []gen.ApplicationMemberSpec{
			{
				Name:    "ChangesetSupervisor",
				Factory: changeset.NewChangesetSupervisor,
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
func (app *Application) Terminate(reason error) {
	if app.onAbnormalStop == nil {
		return
	}
	if errors.Is(reason, gen.TerminateReasonNormal) || errors.Is(reason, gen.TerminateReasonShutdown) {
		return
	}
	app.onAbnormalStop(reason)
}
