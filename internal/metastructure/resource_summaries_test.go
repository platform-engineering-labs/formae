// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package metastructure

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stats"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSummaryDatastore is a spy datastore that tracks which methods are called.
// Only QueryResources, ListResourceSummaries, BatchGetTripletsByKSUIDs,
// LoadResourceById, and LoadLatestResourceByKsuid are implemented; all others
// panic to catch unexpected calls.
type mockSummaryDatastore struct {
	resources      []*pkgmodel.Resource
	summaries      []pkgmodel.ResourceSummary
	byKsuid        map[string]*pkgmodel.Resource
	ksuidToTriplet map[string]pkgmodel.TripletKey

	// call counters
	listSummariesCalls       int
	queryResourcesCalls      int
	batchGetTripletsCalls    int
	loadResourceByIdCalls    int
	loadLatestByKsuidCalls   int
	getStackByLabelCalls     int
	getStandalonePolicyCalls int
}

func (m *mockSummaryDatastore) QueryResources(_ *datastore.ResourceQuery) ([]*pkgmodel.Resource, error) {
	m.queryResourcesCalls++
	return m.resources, nil
}

func (m *mockSummaryDatastore) ListResourceSummaries(_ *datastore.ResourceQuery) ([]pkgmodel.ResourceSummary, error) {
	m.listSummariesCalls++
	return m.summaries, nil
}

func (m *mockSummaryDatastore) BatchGetTripletsByKSUIDs(ksuids []string) (map[string]pkgmodel.TripletKey, error) {
	m.batchGetTripletsCalls++
	if m.ksuidToTriplet != nil {
		result := make(map[string]pkgmodel.TripletKey, len(ksuids))
		for _, k := range ksuids {
			if t, ok := m.ksuidToTriplet[k]; ok {
				result[k] = t
			}
		}
		return result, nil
	}
	return map[string]pkgmodel.TripletKey{}, nil
}

func (m *mockSummaryDatastore) LoadResourceById(ksuid string) (*pkgmodel.Resource, error) {
	m.loadResourceByIdCalls++
	if m.byKsuid != nil {
		if r, ok := m.byKsuid[ksuid]; ok {
			return r, nil
		}
	}
	return nil, nil
}

func (m *mockSummaryDatastore) LoadLatestResourceByKsuid(ksuid string) (*pkgmodel.Resource, error) {
	m.loadLatestByKsuidCalls++
	if m.byKsuid != nil {
		if r, ok := m.byKsuid[ksuid]; ok {
			return r, nil
		}
	}
	return nil, nil
}

func (m *mockSummaryDatastore) GetStackByLabel(_ string) (*pkgmodel.Stack, error) {
	m.getStackByLabelCalls++
	return nil, nil
}

func (m *mockSummaryDatastore) GetStandalonePolicy(_ string) (pkgmodel.Policy, error) {
	m.getStandalonePolicyCalls++
	return nil, nil
}

// Stub implementations for the rest of the Datastore interface (panic on call).

func (m *mockSummaryDatastore) StoreFormaCommand(_ *forma_command.FormaCommand, _ string) error {
	panic("not implemented")
}
func (m *mockSummaryDatastore) LoadFormaCommands() ([]*forma_command.FormaCommand, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) LoadIncompleteFormaCommands() ([]*forma_command.FormaCommand, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) DeleteFormaCommand(_ *forma_command.FormaCommand, _ string) error {
	panic("not implemented")
}
func (m *mockSummaryDatastore) GetFormaCommandByCommandID(_ string) (*forma_command.FormaCommand, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) GetMostRecentFormaCommandByClientID(_ string) (*forma_command.FormaCommand, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) GetResourceModificationsSinceLastReconcile(_ string) ([]datastore.ResourceModification, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) QueryFormaCommands(_ *datastore.StatusQuery) ([]*forma_command.FormaCommand, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) StoreResource(_ *pkgmodel.Resource, _ string, _ ...string) (string, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) DeleteResource(_ *pkgmodel.Resource, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) LoadResource(_ pkgmodel.FormaeURI) (*pkgmodel.Resource, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) LoadResourceByNativeID(_ string, _ string) (*pkgmodel.Resource, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) LoadAllResources() ([]*pkgmodel.Resource, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) LoadAllResourceVersions() ([]datastore.ResourceVersion, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) LoadFormaCommandIDs() ([]string, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) LoadResourceVersionsPage(_ string, _ string, _ int) ([]datastore.ResourceVersion, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) UpdateResourceVersionData(_ string, _ string, _ *pkgmodel.Resource) error {
	panic("not implemented")
}
func (m *mockSummaryDatastore) LoadReapedResources() ([]*pkgmodel.Resource, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) LatestLabelForResource(_ string) (string, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) FindResourcesDependingOn(_ string) ([]*pkgmodel.Resource, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) FindResourcesDependingOnMany(_ []string) (map[string][]*pkgmodel.Resource, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) FindTargetsDependingOnMany(_ []string) (map[string][]*pkgmodel.Target, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) BulkStoreResources(_ []pkgmodel.Resource, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) LoadResourcesByStack(_ string) ([]*pkgmodel.Resource, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) LoadAllResourcesByStack() (map[string][]*pkgmodel.Resource, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) CreateStack(_ *pkgmodel.Stack, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) UpdateStack(_ *pkgmodel.Stack, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) DeleteStack(_ string, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) CountResourcesInStack(_ string) (int, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) ListAllStacks() ([]*pkgmodel.Stack, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) CreateTarget(_ *pkgmodel.Target) (string, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) UpdateTarget(_ *pkgmodel.Target) (string, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) LoadTarget(_ string) (*pkgmodel.Target, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) LoadAllTargets() ([]*pkgmodel.Target, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) LoadDiscoverableTargets() ([]*pkgmodel.Target, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) QueryTargets(_ *datastore.TargetQuery) ([]*pkgmodel.Target, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) DeleteTarget(_ string) (string, error) { panic("not implemented") }
func (m *mockSummaryDatastore) CountResourcesInTarget(_ string) (int, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) Stats() (*stats.Stats, error) { panic("not implemented") }
func (m *mockSummaryDatastore) GetKSUIDByTriplet(_, _, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) BatchGetKSUIDsByTriplets(_ []pkgmodel.TripletKey) (map[pkgmodel.TripletKey]string, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) CreatePolicy(_ pkgmodel.Policy, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) UpdatePolicy(_ pkgmodel.Policy, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) GetPoliciesForStack(_ string) ([]pkgmodel.Policy, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) GetInlinePoliciesForStack(_ string) ([]pkgmodel.Policy, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) ListAllStandalonePolicies() ([]pkgmodel.Policy, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) AttachPolicyToStack(_, _ string) error { panic("not implemented") }
func (m *mockSummaryDatastore) IsPolicyAttachedToStack(_, _ string) (bool, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) GetStacksReferencingPolicy(_ string) ([]string, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) GetAttachedPolicyLabelsForStack(_ string) ([]string, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) DetachPolicyFromStack(_, _ string) error { panic("not implemented") }
func (m *mockSummaryDatastore) DeletePolicy(_ string) (string, error)   { panic("not implemented") }
func (m *mockSummaryDatastore) DeleteInlinePolicy(_, _, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) DeletePoliciesForStack(_ string, _ string) error {
	panic("not implemented")
}
func (m *mockSummaryDatastore) GetExpiredStacks() ([]datastore.ExpiredStackInfo, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) GetStacksWithAutoReconcilePolicy() ([]datastore.StackReconcileInfo, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) GetResourcesAtLastReconcile(_ string) ([]datastore.ResourceSnapshot, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) StackHasActiveCommands(_ string) (bool, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) Close() {}
func (m *mockSummaryDatastore) BulkStoreResourceUpdates(_ string, _ []resource_update.ResourceUpdate) error {
	panic("not implemented")
}
func (m *mockSummaryDatastore) LoadResourceUpdates(_ string) ([]resource_update.ResourceUpdate, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) UpdateResourceUpdateState(_ string, _ string, _ types.OperationType, _ resource_update.ResourceUpdateState, _ time.Time) error {
	panic("not implemented")
}
func (m *mockSummaryDatastore) UpdateResourceUpdateProgress(_ string, _ string, _ types.OperationType, _ resource_update.ResourceUpdateState, _ time.Time, _ time.Time, _ plugin.TrackedProgress, _ map[string]string) error {
	panic("not implemented")
}
func (m *mockSummaryDatastore) BatchUpdateResourceUpdateState(_ string, _ []datastore.ResourceUpdateRef, _ resource_update.ResourceUpdateState, _ time.Time) error {
	panic("not implemented")
}
func (m *mockSummaryDatastore) UpdateFormaCommandProgress(_ string, _ forma_command.CommandState, _ time.Time) error {
	panic("not implemented")
}
func (m *mockSummaryDatastore) UpdateFormaCommandTargetUpdates(_ string, _ json.RawMessage, _ forma_command.CommandState, _ time.Time) error {
	panic("not implemented")
}
func (m *mockSummaryDatastore) ForceCancelResourceUpdates(_ string, _ []datastore.ForceCancelRow, _ []datastore.ResourceUpdateRef, _ time.Time) (datastore.ForceCancelResult, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) UpdateTargetHealth(_ pkgmodel.TargetHealthObservation) (bool, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) AdvanceTargetAccrual(_, _ string, _ time.Time, _ int64) (bool, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) GetUnreachableTargets() ([]*pkgmodel.Target, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) PersistTargetReap(_ datastore.PersistTargetReapRequest) (bool, []string, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) CheckTargetsReaped(_ []string) ([]string, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) LoadStacksByLabels(_ []string) ([]*pkgmodel.Stack, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) LoadTargetsByLabels(_ []string) ([]*pkgmodel.Target, error) {
	panic("not implemented")
}
func (m *mockSummaryDatastore) LoadStandalonePoliciesByLabels(_ []string) ([]pkgmodel.Policy, error) {
	panic("not implemented")
}

// TestListResourceSummaries_NoHeavyWork asserts that ListResourceSummaries calls
// ListResourceSummaries on the datastore and does NOT call BatchGetTripletsByKSUIDs,
// GetStackByLabel, or GetStandalonePolicy.
func TestListResourceSummaries_NoHeavyWork(t *testing.T) {
	ds := &mockSummaryDatastore{
		summaries: []pkgmodel.ResourceSummary{
			{Label: "bucket-a", Stack: "stack-x", Type: "AWS::S3::Bucket", Ksuid: "ksuid-1"},
		},
	}
	m := &Metastructure{Datastore: ds}

	summaries, err := m.ListResourceSummaries("")
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	assert.Equal(t, "bucket-a", summaries[0].Label)

	// Must call the lightweight summary path
	assert.Equal(t, 1, ds.listSummariesCalls, "ListResourceSummaries must call datastore.ListResourceSummaries exactly once")

	// Must NOT call any heavy/enrichment paths
	assert.Equal(t, 0, ds.batchGetTripletsCalls, "ListResourceSummaries must not call BatchGetTripletsByKSUIDs")
	assert.Equal(t, 0, ds.getStackByLabelCalls, "ListResourceSummaries must not call GetStackByLabel")
	assert.Equal(t, 0, ds.getStandalonePolicyCalls, "ListResourceSummaries must not call GetStandalonePolicy")
	assert.Equal(t, 0, ds.queryResourcesCalls, "ListResourceSummaries must not call QueryResources (full deserialize)")
}

// TestExtractResourceByKsuid_FoundPerformsKsuidRewrite asserts that for a found
// resource whose Properties carry a formae URI reference, ExtractResourceByKsuid:
//   - calls LoadLatestResourceByKsuid exactly once
//   - calls BatchGetTripletsByKSUIDs exactly once (the KSUID→triplet rewrite)
//   - returns the (rewritten) resource
//
// The rewrite replaces {"$ref": "formae://<ksuid>#"} objects with a $res object
// carrying the resolved label/type/stack triplet. Plain string property values
// are not treated as ksuid references; only formae URI strings in JSON values trigger rewrite.
func TestExtractResourceByKsuid_FoundPerformsKsuidRewrite(t *testing.T) {
	referencedKsuid := "ksuid-ref-999"
	tripletKey := pkgmodel.TripletKey{Type: "AWS::EC2::VPC", Stack: "stack-x", Label: "my-vpc"}

	// Properties contain a $ref object wrapping a formae URI — the canonical
	// in-DB representation of a cross-resource reference.
	// replaceKSUIDs rewrites {"$ref":"formae://<ksuid>#"} → a $res object.
	refURI := "formae://" + referencedKsuid + "#"

	ds := &mockSummaryDatastore{
		byKsuid: map[string]*pkgmodel.Resource{
			"ksuid-1": {
				Label:      "bucket-a",
				Type:       "AWS::S3::Bucket",
				Stack:      "stack-x",
				Ksuid:      "ksuid-1",
				Properties: json.RawMessage(`{"VpcId":{"$ref":"` + refURI + `"}}`),
			},
		},
		ksuidToTriplet: map[string]pkgmodel.TripletKey{
			referencedKsuid: tripletKey,
		},
	}
	m := &Metastructure{Datastore: ds}

	result, err := m.ExtractResourceByKsuid("ksuid-1")
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify LoadLatestResourceByKsuid was called once
	assert.Equal(t, 1, ds.loadLatestByKsuidCalls, "ExtractResourceByKsuid must call LoadLatestResourceByKsuid exactly once")

	// Verify BatchGetTripletsByKSUIDs was called once (for the KSUID rewrite)
	assert.Equal(t, 1, ds.batchGetTripletsCalls, "ExtractResourceByKsuid must call BatchGetTripletsByKSUIDs exactly once for KSUID rewrite")

	// Verify the rewrite happened: the $ref containing the ksuid URI should be
	// gone from Properties, replaced by a $res object with label/type/stack.
	propsStr := string(result.Properties)
	assert.NotContains(t, propsStr, referencedKsuid, "Properties should not contain raw ksuid after rewrite")
	assert.Contains(t, propsStr, "$res", "Properties should contain $res after rewrite")
	assert.Contains(t, propsStr, tripletKey.Label, "Properties should contain resolved label after rewrite")
}

// TestExtractResourceByKsuid_NotFoundReturnsNil asserts that when LoadLatestResourceByKsuid
// returns nil (not found), ExtractResourceByKsuid returns nil, nil.
func TestExtractResourceByKsuid_NotFoundReturnsNil(t *testing.T) {
	ds := &mockSummaryDatastore{
		byKsuid: map[string]*pkgmodel.Resource{}, // nothing stored
	}
	m := &Metastructure{Datastore: ds}

	result, err := m.ExtractResourceByKsuid("no-such-ksuid")
	require.NoError(t, err)
	assert.Nil(t, result, "not-found must return nil resource")

	assert.Equal(t, 1, ds.loadLatestByKsuidCalls, "must call LoadLatestResourceByKsuid once even for not-found")
	assert.Equal(t, 0, ds.batchGetTripletsCalls, "must not call BatchGetTripletsByKSUIDs for not-found")
}

// TestExtractResourceByKsuid_NoPropertiesKsuids asserts that when a found resource
// has no ksuid references in its Properties, BatchGetTripletsByKSUIDs is NOT called
// (the optimisation: skip the batch call if no ksuids found).
func TestExtractResourceByKsuid_NoPropertiesKsuids(t *testing.T) {
	ds := &mockSummaryDatastore{
		byKsuid: map[string]*pkgmodel.Resource{
			"ksuid-2": {
				Label:      "bucket-b",
				Type:       "AWS::S3::Bucket",
				Stack:      "stack-y",
				Ksuid:      "ksuid-2",
				Properties: json.RawMessage(`{"BucketName":"plain-bucket"}`),
			},
		},
	}
	m := &Metastructure{Datastore: ds}

	result, err := m.ExtractResourceByKsuid("ksuid-2")
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, 1, ds.loadLatestByKsuidCalls)
	// reverseTranslateKSUIDsToTriplets short-circuits when no KSUIDs found,
	// so BatchGetTripletsByKSUIDs should not be called.
	assert.Equal(t, 0, ds.batchGetTripletsCalls)
}

// TestExtractResourceByKsuid_DeletedReturnsNil asserts that when
// LoadLatestResourceByKsuid returns nil (because the latest version is a delete
// or reaped tombstone), ExtractResourceByKsuid propagates nil rather than
// returning a stale prior revision.
func TestExtractResourceByKsuid_DeletedReturnsNil(t *testing.T) {
	// byKsuid is nil — LoadLatestResourceByKsuid returns nil, simulating a
	// resource whose latest row is a delete tombstone.
	ds := &mockSummaryDatastore{}
	m := &Metastructure{Datastore: ds}

	result, err := m.ExtractResourceByKsuid("deleted-ksuid")
	require.NoError(t, err)
	assert.Nil(t, result, "deleted resource must return nil, not a stale prior revision")

	assert.Equal(t, 1, ds.loadLatestByKsuidCalls)
	assert.Equal(t, 0, ds.batchGetTripletsCalls, "must not call BatchGetTripletsByKSUIDs for deleted resource")
	assert.Equal(t, 0, ds.loadResourceByIdCalls, "must not fall back to LoadResourceById")
}

func (m *mockSummaryDatastore) RecordAgentBoot(_ string) error {
	return nil
}
