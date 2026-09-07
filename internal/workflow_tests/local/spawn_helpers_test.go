// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"testing"

	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// spawnResourceUpdater spawns a ResourceUpdater actor directly on the node,
// registered under the canonical name for the given (uri, operation, commandID)
// tuple. from is the PID that will receive the ResourceUpdateFinished message.
// The ResourceUpdater is now spawned directly rather than via a global supervisor.
func spawnResourceUpdater(
	t *testing.T,
	node gen.Node,
	from gen.PID,
	resourceURI pkgmodel.FormaeURI,
	operation string,
	commandID string,
) error {
	t.Helper()
	name := actornames.ResourceUpdater(resourceURI, operation, commandID)
	_, err := node.SpawnRegister(name, resource_update.NewResourceUpdater, gen.ProcessOptions{}, from)
	return err
}

// spawnResolveCache spawns a ResolveCache actor directly on the node for the
// given commandID. This replaces the old EnsureResolveCache call to the
// (now-removed) supervisor handler on ChangesetSupervisor.
func spawnResolveCache(t *testing.T, node gen.Node, commandID string) error {
	t.Helper()
	name := actornames.ResolveCache(commandID)
	_, err := node.SpawnRegister(name, changeset.NewResolveCache, gen.ProcessOptions{})
	return err
}
