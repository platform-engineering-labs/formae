// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"testing"
	"time"

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
	// SetTargetHealthStateForTest forces the health_state column of the current
	// (max-version) targets row for the given label to the supplied value.
	// Used to set up guard conditions (e.g. 'reaped') that cannot be reached
	// through the public Datastore API. Backends that don't provide it leave
	// it nil and the relevant tests t.Skip().
	SetTargetHealthStateForTest func(label, state string) error
	// SetTargetAccrualForTest seeds the unreachability-accrual columns
	// (first_unreachable_at, unreachable_accum_seconds) of the current
	// (max-version) targets row for the given label. Used to set up a
	// non-pristine accrual state that a later success observation must clear.
	// Backends that don't provide it leave it nil and the relevant tests
	// t.Skip().
	SetTargetAccrualForTest func(label string, firstUnreachableAt time.Time, accumSeconds int64) error
	// MarkResourceReapedForTest forces the operation column of the current
	// (max-version) resources row for the given uri to 'reaped'. Used to set up
	// the reaped tombstone that the live-query exclusion and the resource-write
	// guard react to, a state no public Datastore API reaches yet. Backends that
	// don't provide it leave it nil and the relevant tests t.Skip().
	MarkResourceReapedForTest func(uri string) error
	// RawResourceOperationForTest returns the operation column of the current
	// (max-version) resources row for the given uri, bypassing the live-row
	// filters, or "" when no row exists. Used to prove a reaped row is retained
	// in the table. Backends that don't provide it leave it nil and the relevant
	// tests t.Skip().
	RawResourceOperationForTest func(uri string) (string, error)
	// CountReapAuditRowsForTest returns the number of target_reap_audit rows for
	// the given label. Used to prove exactly one audit row is written per reap
	// and none on a rejected/no-op reap. Backends that don't provide it leave it
	// nil and the relevant assertions are skipped.
	CountReapAuditRowsForTest func(label string) (int, error)
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
	RunTerminalStatesLiteralsTest(t, newDS)
	RunMonotonicTerminalityTest(t, newDS)
	RunMonotonicTerminalityRaceTest(t, newDS)
	RunForceCancelResourceUpdatesTest(t, newDS)

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
	RunReapedResourcesInvisibleToLiveQueries(t, newDS)
	RunResourceWriteRejectedWhenTargetReaped(t, newDS)
	RunResourceWriteRejectedWhenIncarnationChanged(t, newDS)

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
	RunTargetHealthDefaults(t, newDS)
	RunTargetHealthStableAcrossUpdate(t, newDS)
	RunUpdateTargetHealthReachable(t, newDS)
	RunUpdateTargetHealthMonotonicGuard(t, newDS)
	RunUpdateTargetHealthSubSecondMonotonic(t, newDS)
	RunUpdateTargetHealthReapedGuard(t, newDS)
	RunUpdateTargetHealthIncarnationMatch(t, newDS)
	RunUpdateTargetHealthIncarnationMismatch(t, newDS)
	RunTargetReapingPersists(t, newDS)
	RunTargetReapingPopulatedByDependencyLoad(t, newDS)
	RunUpdateTargetMintsFreshIncarnationOnReaped(t, newDS)
	RunUpdateTargetCarriesHealthForwardWhenNotReaped(t, newDS)
	RunSuccessObservationZeroesAccrual(t, newDS)
	RunAdvanceTargetAccrual(t, newDS)
	RunGetUnreachableTargets(t, newDS)
	RunPersistTargetReapHappyPath(t, newDS)
	RunPersistTargetReapGuards(t, newDS)
	RunPersistTargetReapConcurrent(t, newDS)

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
	RunGetResourcesAtLastReconcile_DestroyAfterApplyEmptiesBaseline(t, newDS)
	RunGetResourcesAtLastReconcile_FailedDestroyIncluded(t, newDS)
	RunGetResourcesAtLastReconcile_PartialDestroyLeavesUntouchedInBaseline(t, newDS)
}
