// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/datastore"
)

// TestDatastore bundles a Datastore with a cleanup function for tests.
type TestDatastore struct {
	datastore.Datastore
	CleanUpFn func() error
}

// RunAll runs the full datastore test suite against the provided factory.
// Each subtest receives a fresh datastore instance.
func RunAll(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Helper()

	RunFormaApplyTest(t, newDS)
	RunLoadIncompleteFormaCommandsTest(t, newDS)
	RunGetFormaApplyByFormaHash(t, newDS)
	RunStoreAndLoadFormaCommandOptionalFields(t, newDS)
	RunStoreFormaCommandSyncSkipsResourceUpdates(t, newDS)
	RunGetMostRecentFormaCommandByClientID(t, newDS)
	RunGetMostRecentNonReconcileFormaCommandsByStack(t, newDS)
	RunQueryFormaCommands(t, newDS)

	RunStoreResource(t, newDS)
	RunUpdateResource(t, newDS)
	RunDeleteResource(t, newDS)
	RunQueryResources(t, newDS)
	RunStoreResourceSameResourceTwice(t, newDS)
	RunLoadResourceByNativeID(t, newDS)
	RunLoadResourceByNativeIDDifferentTypes(t, newDS)
	RunBatchGetKSUIDsByTriplets(t, newDS)
	RunBatchGetKSUIDsByTripletsPatchScenario(t, newDS)
	RunDifferentResourceTypesSameNativeId(t, newDS)
	RunGetResourceModificationsSinceLastReconcile(t, newDS)
	RunStoreResourceAfterDeleteWithSameNativeID(t, newDS)

	RunQueryTargetsAll(t, newDS)
	RunQueryTargetsByNamespace(t, newDS)
	RunQueryTargetsByDiscoverable(t, newDS)
	RunQueryTargetsByLabel(t, newDS)
	RunQueryTargetsDiscoverableAWS(t, newDS)
	RunQueryTargetsNonDiscoverable(t, newDS)
	RunQueryTargetsVersioning(t, newDS)
	RunCountResourcesInTarget(t, newDS)
	RunDeleteTargetSuccess(t, newDS)
	RunUpdateTargetNotFoundReturnsError(t, newDS)
	RunDeleteTargetNotFound(t, newDS)

	RunCreateStack(t, newDS)
	RunCreateStackAlreadyExists(t, newDS)
	RunGetStackByLabelNotFound(t, newDS)
	RunUpdateStack(t, newDS)
	RunDeleteStack(t, newDS)
	RunDeleteStackThenRecreate(t, newDS)
	RunCountResourcesInStack(t, newDS)

	RunFindResourcesDependingOn(t, newDS)
	RunFindResourcesDependingOnMultipleRefs(t, newDS)
	RunFindResourcesDependingOnNoRefs(t, newDS)
	RunFindResourcesDependingOnDeletedResourcesExcluded(t, newDS)

	RunStackTransition(t, newDS)
}
