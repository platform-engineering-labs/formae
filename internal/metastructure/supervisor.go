// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	"github.com/platform-engineering-labs/formae/internal/metastructure/discovery"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_persister"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_persister"
)

func factory_Supervisor() gen.ProcessBehavior {
	return &Supervisor{}
}

type Supervisor struct {
	act.Supervisor
}

// Init invoked on a spawn Supervisor process. This is a mandatory callback for the implementation
func (sup *Supervisor) Init(args ...any) (act.SupervisorSpec, error) {
	var spec act.SupervisorSpec

	spec.Type = act.SupervisorTypeOneForOne

	spec.Children = []act.SupervisorChildSpec{
		{
			Name:    "RateLimiter",
			Factory: changeset.NewRateLimiter,
		},
		{
			Name:    "FormaCommandPersister",
			Factory: forma_persister.NewFormaCommandPersister,
		},
		{
			Name:    "ResourcePersister",
			Factory: resource_persister.NewResourcePersister,
		},
		{
			Name:    "Synchronizer",
			Factory: factory_Synchronizer,
		},
		{
			Name:    "Discovery",
			Factory: discovery.NewDiscovery,
		},
	}

	spec.Restart.Strategy = act.SupervisorStrategyTransient
	spec.Restart.Intensity = 2 // How big bursts of restarts you want to tolerate.
	spec.Restart.Period = 1    // In seconds.

	return spec, nil
}

// HandleMessage invoked if Supervisor received a message sent with gen.Process.Send(...).
// Non-nil value of the returning error will cause termination of this process.
// To stop this process normally, return gen.TerminateReasonNormal or
// gen.TerminateReasonShutdown. Any other - for abnormal termination.
func (sup *Supervisor) HandleMessage(from gen.PID, message any) error {
	switch msg := message.(type) {
	default:
		sup.Log().Debug("Supervisor got an unknown message: %v %T", message, msg)
	}

	return nil
}
