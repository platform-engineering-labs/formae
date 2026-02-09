// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package actornames

import (
	"fmt"

	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/pkg/model"
)

const (
	ChangesetSupervisor       = gen.Atom("ChangesetSupervisor")
	Discovery                 = gen.Atom("Discovery")
	FormaCommandPersister     = gen.Atom("FormaCommandPersister")
	MetastructureBridge       = gen.Atom("MetastructureBridge")
	PluginOperatorSupervisor  = gen.Atom("PluginOperatorSupervisor")
	PluginCoordinator         = gen.Atom("PluginCoordinator")
	RateLimiter               = gen.Atom("RateLimiter")
	ResourcePersister         = gen.Atom("ResourcePersister")
	ResourceUpdaterSupervisor = gen.Atom("ResourceUpdaterSupervisor")
	StackExpirer              = gen.Atom("StackExpirer")
	Synchronizer              = gen.Atom("Synchronizer")
)

func ChangesetExecutor(commandID string) gen.Atom {
	return gen.Atom(fmt.Sprintf("formae://changeset/executor/%s", commandID))
}

func PluginOperator(resourceURI model.FormaeURI, operation string, operationID string) gen.Atom {
	return gen.Atom(fmt.Sprintf("%s/%s/%s", resourceURI, operation, operationID))
}

func ResolveCache(commandID string) gen.Atom {
	return gen.Atom(fmt.Sprintf("formae://changeset/resolve-cache/%s", commandID))
}

func ResourceUpdater(resourceURI model.FormaeURI, operation string, commandID string) gen.Atom {
	return gen.Atom(fmt.Sprintf("%s/resource-updater/%s/%s", resourceURI, operation, commandID))
}
