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
	AutoReconciler        = gen.Atom("AutoReconciler")
	ChangesetSupervisor   = gen.Atom("ChangesetSupervisor")
	Discovery             = gen.Atom("Discovery")
	FormaCommandPersister = gen.Atom("FormaCommandPersister")
	GeneratorRotator      = gen.Atom("GeneratorRotator")
	MetastructureBridge   = gen.Atom("MetastructureBridge")
	PluginCoordinator     = gen.Atom("PluginCoordinator")
	RateLimiter           = gen.Atom("RateLimiter")
	ResourcePersister     = gen.Atom("ResourcePersister")
	StackExpirer          = gen.Atom("StackExpirer")
	Synchronizer          = gen.Atom("Synchronizer")
	TargetReaper          = gen.Atom("TargetReaper")
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

func TargetUpdater(label, operation, commandID string) gen.Atom {
	return gen.Atom(fmt.Sprintf("target://%s/target-updater/%s/%s", label, operation, commandID))
}

// GeneratorUpdater names the actor that draws one generator's value. Unlike
// TargetUpdater it is keyed on the generator update's node URI rather than a
// bare label, because a generator label is unique only within its stack: two
// stacks in one command may each declare "db-password", and a label-only name
// would put both on one actor. The node URI already carries the stack, the
// label (each percent-encoded) and the operation.
func GeneratorUpdater(nodeURI model.FormaeURI, commandID string) gen.Atom {
	return gen.Atom(fmt.Sprintf("%s/generator-updater/%s", nodeURI, commandID))
}
