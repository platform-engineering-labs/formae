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
	// RawInsertResource writes a resources row directly with the exact
	// (uri, version, target, operation) provided, bypassing the Datastore's
	// version generation. Tests that need to exercise specific version
	// strings (e.g. byte-order vs case-insensitive comparison) use it.
	// Backends that don't provide it leave it nil and the test t.Skip()s.
	RawInsertResource func(uri, version, target, operation string) error
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
	RunQueryFormaCommands_StackWildcardEscape(t, newDS)

	RunStoreResource(t, newDS)
	RunUpdateResource(t, newDS)
	RunDeleteResource(t, newDS)
	RunQueryResources(t, newDS)
	RunQueryResources_LikeMetacharsAreLiteral(t, newDS)
	RunStoreResourceSameResourceTwice(t, newDS)
	RunLoadResourceByNativeID(t, newDS)
	RunLoadResourceByNativeIDDifferentTypes(t, newDS)
	RunBatchGetKSUIDsByTriplets(t, newDS)
	RunBatchGetKSUIDsByTripletsPatchScenario(t, newDS)
	RunDifferentResourceTypesSameNativeId(t, newDS)
	RunGetResourceModificationsSinceLastReconcile(t, newDS)
	RunStoreResourceAfterDeleteWithSameNativeID(t, newDS)
	RunStoreResourceWithDifferentKSUIDSameData(t, newDS)
	RunStoreResourceRenamePreservesKsuidAndAddsNewVersion(t, newDS)

	RunQueryTargetsAll(t, newDS)
	RunQueryTargetsByNamespace(t, newDS)
	RunQueryTargetsByDiscoverable(t, newDS)
	RunQueryTargetsByLabel(t, newDS)
	RunQueryTargetsDiscoverableAWS(t, newDS)
	RunQueryTargetsNonDiscoverable(t, newDS)
	RunQueryTargetsVersioning(t, newDS)
	RunCountResourcesInTarget(t, newDS)
	RunCountResourcesInTargetUsesByteOrderForVersionComparison(t, newDS)
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

	RunGetResourcesAtLastReconcile_Empty(t, newDS)
	RunGetResourcesAtLastReconcile_SuccessReturnsDesiredState(t, newDS)
	RunGetResourcesAtLastReconcile_FailedReconcileIncluded(t, newDS)
	RunGetResourcesAtLastReconcile_CanceledReconcileExcluded(t, newDS)
	RunGetResourcesAtLastReconcile_InProgressReconcileExcluded(t, newDS)
	RunGetResourcesAtLastReconcile_DeleteRowsExcluded(t, newDS)
	RunGetResourcesAtLastReconcile_NonUserSourceExcluded(t, newDS)
	RunGetResourcesAtLastReconcile_PatchModeExcluded(t, newDS)
	RunGetResourcesAtLastReconcile_MostRecentReconcileWins(t, newDS)
	RunGetResourcesAtLastReconcile_StackScoped(t, newDS)
}
