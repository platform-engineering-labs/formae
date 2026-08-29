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
	// LoadAgentBootsForTest returns every agent_boots row ordered by
	// (booted_at, boot_id) ascending. The agent has no read path for these rows
	// by design (the reader is a separate process), so the suite needs a direct
	// accessor to prove they were written and that they accumulate. Backends
	// that don't provide it leave it nil and the relevant tests t.Skip().
	LoadAgentBootsForTest func() ([]datastore.AgentBoot, error)
	// SetStackValidFromForTest rewrites the valid_from of the named stack's
	// versions, in ascending version order, to the supplied timestamps. Used to
	// age a stack deterministically instead of sleeping, so TTL expiry can be
	// exercised against a known creation anchor. Backends that don't provide it
	// leave it nil and the relevant tests t.Skip().
	SetStackValidFromForTest func(label string, validFrom []time.Time) error
	// SetPolicyDataForTest overwrites the policy_data column of the current
	// (max-version) policies row for the given label. Used to stage stored state
	// no public API produces — a malformed or hand-edited payload — so the
	// expiry query's defensive behaviour can be asserted. Backends that don't
	// provide it leave it nil and the relevant tests t.Skip().
	SetPolicyDataForTest func(label, policyData string) error
	// NullResourceUpdateModifiedTsForTest sets modified_ts to SQL NULL on every
	// resource_updates row for the given ksuid. The column is nullable and the
	// normalizing migration writes NULL whenever the migrated command carried no
	// ModifiedTs, so such rows exist in datastores that predate that migration —
	// but no public API produces one, since a Go time.Time is always a value.
	// Used to assert that a row with no timestamp cannot latch a failure.
	// Backends that don't provide it leave it nil and the relevant tests
	// t.Skip().
	NullResourceUpdateModifiedTsForTest func(ksuid string) error
	// SetResourceUpdateModifiedTsRawForTest writes raw text into modified_ts on
	// every resource_updates row for the given ksuid. Backends that store the
	// column as text can hold more than one timestamp spelling — the
	// normalizing migration copies RFC 3339 straight out of the JSON blob,
	// while the driver writes its own layout — and no public API produces the
	// migrated spelling. Used to prove the comparison is on instants rather
	// than on characters. Backends storing a real timestamp type leave it nil
	// and the relevant tests t.Skip().
	SetResourceUpdateModifiedTsRawForTest func(ksuid, raw string) error
	// NullFormaCommandSubjectForTest sets subject and subject_name to SQL NULL
	// on the forma_commands row for the given commandID, so tests can stage the
	// unattributed rows a pre-migration command leaves behind — stored state no
	// public API produces, since a Go string is always a value (at worst "").
	// Used to assert that such a row reads back Subject/SubjectName as "".
	// Backends that don't provide it leave it nil and the relevant tests
	// t.Skip().
	NullFormaCommandSubjectForTest func(commandID string) error
	// GeneratorIDForTest returns the internal KSUID identity (the id column,
	// stable across CreateGenerator/UpdateGenerator) of the current
	// (max-version) generator row with the given label on the given stack, or
	// "" if none exists. Generator has no public API that exposes this id —
	// the Datastore interface returns only version strings — so the suite
	// needs a direct accessor to prove the id survives an update unchanged.
	// Backends that don't provide it leave it nil and the relevant tests
	// t.Skip().
	GeneratorIDForTest func(label, stackLabel string) (string, error)
}

// RunAll runs the full datastore test suite against the provided factory.
// Each subtest receives a fresh datastore instance.
func RunAll(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Helper()

	RunFormaApplyTest(t, newDS)
	RunLoadIncompleteFormaCommandsTest(t, newDS)
	RunGetFormaApplyByFormaHash(t, newDS)
	RunStoreAndLoadFormaCommandOptionalFields(t, newDS)
	RunStoreAndLoadFormaCommandEmptySubject(t, newDS)
	RunFormaCommandSubjectNullRoundTrip(t, newDS)
	RunFormaCommandSuppressedDriftNotesRoundTrip(t, newDS)
	RunStoreFormaCommandSyncSkipsResourceUpdates(t, newDS)
	RunCommandSourceRoundTrip(t, newDS)
	RunGetMostRecentFormaCommandByClientID(t, newDS)
	RunGetMostRecentFormaCommandByClientIDSkipsSchedulers(t, newDS)
	RunGetMostRecentFormaCommandByClientIDIgnoresSourcelessRows(t, newDS)
	RunGetMostRecentNonReconcileFormaCommandsByStack(t, newDS)
	RunQueryFormaCommands(t, newDS)
	RunQueryFormaCommandsUserOnly(t, newDS)
	RunQueryFormaCommands_StackWildcardEscape(t, newDS)
	RunQueryFormaCommandsBySubject(t, newDS)
	RunTerminalStatesLiteralsTest(t, newDS)
	RunUpdateResourceUpdateProgressPersistsStartTs(t, newDS)
	RunMonotonicTerminalityTest(t, newDS)
	RunMonotonicTerminalityRaceTest(t, newDS)
	RunForceCancelResourceUpdatesTest(t, newDS)
	RunResourceUpdateFailureReasonRoundTrip(t, newDS)
	RunResourceUpdateProvenanceRoundTrip(t, newDS)

	RunRecordAgentBoot(t, newDS)
	RunAgentBootsAreAppendOnly(t, newDS)
	RunRecordAgentBootEmptyVersion(t, newDS)

	RunStoreResource(t, newDS)
	RunUpdateResource(t, newDS)
	RunDeleteResource(t, newDS)
	RunQueryResources(t, newDS)
	RunListResourceSummaries(t, newDS)
	RunListResourceSummaries_StableOrdering(t, newDS)
	RunListResourceSummaries_EmptyNativeID(t, newDS)
	RunQueryResources_LikeMetacharsAreLiteral(t, newDS)
	RunStoreResourceSameResourceTwice(t, newDS)
	RunLoadResourceByNativeID(t, newDS)
	RunLoadResourceByNativeIDDifferentTypes(t, newDS)
	RunBatchGetKSUIDsByTriplets(t, newDS)
	RunBatchGetKSUIDsByTripletsPatchScenario(t, newDS)
	RunGetKSUIDByTriplet(t, newDS)
	RunDifferentResourceTypesSameNativeId(t, newDS)
	RunGetResourceModificationsSinceLastReconcile(t, newDS)
	RunStoreResourceAfterDeleteWithSameNativeID(t, newDS)
	RunStoreResourceWithDifferentKSUIDSameData(t, newDS)
	RunStoreResourceRenamePreservesKsuidAndAddsNewVersion(t, newDS)
	RunScrubResourceVersions(t, newDS)
	RunReapedResourcesInvisibleToLiveQueries(t, newDS)
	RunResourceWriteRejectedWhenTargetReaped(t, newDS)
	RunResourceWriteRejectedWhenIncarnationChanged(t, newDS)
	RunLoadLatestResourceByKsuid_DeletedReturnsNil(t, newDS)
	RunLoadLatestResourceByKsuid_LiveReturnsLatest(t, newDS)
	RunLoadLatestResourceByKsuid_MissingReturnsNil(t, newDS)

	RunQueryTargetsAll(t, newDS)
	RunQueryTargetsByNamespace(t, newDS)
	RunQueryTargetsByDiscoverable(t, newDS)
	RunQueryTargetsByLabel(t, newDS)
	RunQueryTargetsDiscoverableAWS(t, newDS)
	RunQueryTargetsNonDiscoverable(t, newDS)
	RunQueryTargetsVersioning(t, newDS)
	RunReapedTargetsInvisibleToQuery(t, newDS)
	RunStatsExcludesReapedTargets(t, newDS)
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
	RunCheckTargetsReaped(t, newDS)
	RunUpdateTargetUnreapsResourcesOnRecovery(t, newDS)

	RunCreateStack(t, newDS)
	RunCreateStackAlreadyExists(t, newDS)
	RunGetStackByLabelNotFound(t, newDS)
	RunUpdateStack(t, newDS)
	RunDeleteStack(t, newDS)
	RunDeleteStackThenRecreate(t, newDS)
	RunCountResourcesInStack(t, newDS)
	RunStackCreatedAtIsFirstVersion(t, newDS)
	RunStackCreatedAtSerializesOverAPI(t, newDS)

	RunGetExpiredStacks_RelativeAnchoredAtCreation(t, newDS)
	RunGetExpiredStacks_Absolute(t, newDS)
	RunGetExpiredStacks_MalformedExpiresAt(t, newDS)
	RunGetExpiredStacks_MalformedExpiresAtSortingLow(t, newDS)
	RunGetExpiredStacks_BothVariantsSet(t, newDS)
	RunGetExpiredStacks_OneRowPerStack(t, newDS)
	RunGetExpiredStacks_LegacyRelative(t, newDS)
	RunGetExpiredStacks_ReportsAnchorAndDeadline(t, newDS)
	RunTTLPolicyVariantRoundTrip(t, newDS)

	RunGetInlinePoliciesForStack(t, newDS)
	RunGetInlinePoliciesForStackExcludesAttachedStandalone(t, newDS)
	RunGetInlinePoliciesForStackExcludesDeleted(t, newDS)
	RunGetInlinePoliciesForStackReturnsLatestVersion(t, newDS)
	RunGetInlinePoliciesForStackEmptyStackID(t, newDS)

	RunDeleteInlinePolicy(t, newDS)
	RunDeleteInlinePolicyDeletesEveryMatchingID(t, newDS)
	RunDeleteInlinePolicyEmptyStackID(t, newDS)
	RunDeleteInlinePolicyLeavesAttachedStandalone(t, newDS)
	RunDeleteInlinePolicyLeavesSameLabelledStandalone(t, newDS)
	RunDeleteInlinePolicyIsIdempotent(t, newDS)
	RunDeleteInlinePolicyUnknownLabel(t, newDS)
	RunDeleteInlinePolicyClearsExpiry(t, newDS)
	RunDeleteInlinePolicyThenRecreate(t, newDS)

	RunCreateGeneratorThenGet(t, newDS)
	RunGetGeneratorAbsentReturnsNil(t, newDS)
	RunUpdateGeneratorBumpsVersionAndReadBackReflectsIt(t, newDS)
	RunDeleteGeneratorThenGetReturnsNil(t, newDS)
	RunLoadGeneratorsByStackReturnsOnlyThatStacksGenerators(t, newDS)
	RunGeneratorKSUIDStableAcrossUpdate(t, newDS)
	RunGeneratorKSUIDStableAcrossRename(t, newDS)
	RunDeleteGeneratorAfterRenameDeletesOnlyTheCurrentRow(t, newDS)
	RunGeneratorHasNoGenerationUntilOneIsDrawn(t, newDS)
	RunAdvanceGenerationRecordsIdentityAndDrawingSpec(t, newDS)
	RunGenerationSurvivesASpecUpdate(t, newDS)
	RunGenerationSurvivesARename(t, newDS)
	RunGetGeneratorIdentityByIDFindsTheLiveRow(t, newDS)
	RunGetGeneratorIdentityAbsentReturnsZeroValue(t, newDS)
	RunGeneratorIdentityOldLabelIsGoneAfterRename(t, newDS)
	RunGeneratorIdentityGoneAfterDelete(t, newDS)
	RunGenerationSurvivesRenameBackToOriginalLabel(t, newDS)
	RunAdvanceGenerationTwiceSecondWins(t, newDS)
	RunAdvanceGenerationDoesNotAffectOtherGenerator(t, newDS)

	RunFindResourcesDependingOn(t, newDS)
	RunFindResourcesDependingOnMultipleRefs(t, newDS)
	RunFindResourcesDependingOnNoRefs(t, newDS)
	RunFindResourcesDependingOnDeletedResourcesExcluded(t, newDS)
	RunFindResourcesDependingOnMany_MultipleFrontierRefs(t, newDS)
	RunFindResourcesDependingOnMany_RepeatedRef(t, newDS)
	RunFindResourcesDependingOnMany_FrontierMemberOverlap(t, newDS)
	RunFindResourcesDependingOnMany_DeepChain(t, newDS)
	RunFindResourcesDependingOnMany_BroadFanOut(t, newDS)

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
