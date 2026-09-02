// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package metastructure

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/constants"
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

// mockExtractDatastore implements datastore.Datastore with only the methods
// needed by ExtractResources. Unimplemented methods panic to catch accidental usage.
type mockExtractDatastore struct {
	resources []*pkgmodel.Resource
	targets   map[string]*pkgmodel.Target
	stacks    map[string]*pkgmodel.Stack
	policies  map[string]pkgmodel.Policy

	// call counters for verifying batched vs. per-item access patterns
	getStackByLabelCalls                int
	getStandalonePolicyCalls            int
	loadStacksByLabelsCalls             int
	loadStandalonePoliciesByLabelsCalls int
}

func (m *mockExtractDatastore) QueryResources(_ *datastore.ResourceQuery) ([]*pkgmodel.Resource, error) {
	return m.resources, nil
}

func (m *mockExtractDatastore) ListResourceSummaries(_ *datastore.ResourceQuery) ([]pkgmodel.ResourceSummary, error) {
	panic("not implemented")
}

func (m *mockExtractDatastore) BatchGetTripletsByKSUIDs(_ []string) (map[string]pkgmodel.TripletKey, error) {
	return map[string]pkgmodel.TripletKey{}, nil
}

func (m *mockExtractDatastore) LoadTargetsByLabels(labels []string) ([]*pkgmodel.Target, error) {
	result := make([]*pkgmodel.Target, 0, len(labels))
	for _, label := range labels {
		if t, ok := m.targets[label]; ok {
			result = append(result, t)
		} else {
			result = append(result, nil)
		}
	}
	return result, nil
}

func (m *mockExtractDatastore) GetStackByLabel(label string) (*pkgmodel.Stack, error) {
	m.getStackByLabelCalls++
	if s, ok := m.stacks[label]; ok {
		return s, nil
	}
	return nil, nil
}

func (m *mockExtractDatastore) GetStandalonePolicy(label string) (pkgmodel.Policy, error) {
	m.getStandalonePolicyCalls++
	if p, ok := m.policies[label]; ok {
		return p, nil
	}
	return nil, nil
}

func (m *mockExtractDatastore) LoadStacksByLabels(labels []string) ([]*pkgmodel.Stack, error) {
	m.loadStacksByLabelsCalls++
	result := make([]*pkgmodel.Stack, 0, len(labels))
	for _, label := range labels {
		if s, ok := m.stacks[label]; ok {
			result = append(result, s)
		}
	}
	return result, nil
}

func (m *mockExtractDatastore) LoadStandalonePoliciesByLabels(labels []string) ([]pkgmodel.Policy, error) {
	m.loadStandalonePoliciesByLabelsCalls++
	result := make([]pkgmodel.Policy, 0, len(labels))
	for _, label := range labels {
		if p, ok := m.policies[label]; ok {
			result = append(result, p)
		}
	}
	return result, nil
}

// Stub implementations for the rest of the Datastore interface.
// These panic if called, ensuring the test only exercises expected code paths.

func (m *mockExtractDatastore) StoreFormaCommand(_ *forma_command.FormaCommand, _ string) error {
	panic("not implemented")
}
func (m *mockExtractDatastore) LoadFormaCommands() ([]*forma_command.FormaCommand, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) LoadIncompleteFormaCommands() ([]*forma_command.FormaCommand, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) DeleteFormaCommand(_ *forma_command.FormaCommand, _ string) error {
	panic("not implemented")
}
func (m *mockExtractDatastore) GetFormaCommandByCommandID(_ string) (*forma_command.FormaCommand, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) GetMostRecentFormaCommandByClientID(_ string) (*forma_command.FormaCommand, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) GetPropertiesAtLastWrite(_ string) (json.RawMessage, error) {
	return nil, nil
}
func (m *mockExtractDatastore) GetOwnedMembers(_ string) (pkgmodel.OwnedMembers, error) {
	return nil, nil
}

func (m *mockExtractDatastore) GetResourceModificationsSinceLastReconcile(_ string) ([]datastore.ResourceModification, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) QueryFormaCommands(_ *datastore.StatusQuery) ([]*forma_command.FormaCommand, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) StoreResource(_ *pkgmodel.Resource, _ string, _ ...string) (string, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) DeleteResource(_ *pkgmodel.Resource, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) LoadResource(_ pkgmodel.FormaeURI) (*pkgmodel.Resource, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) LoadResourceByNativeID(_ string, _ string) (*pkgmodel.Resource, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) LoadAllResources() ([]*pkgmodel.Resource, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) LoadAllResourceVersions() ([]datastore.ResourceVersion, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) LoadFormaCommandIDs() ([]string, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) LoadResourceVersionsPage(_ string, _ string, _ int) ([]datastore.ResourceVersion, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) UpdateResourceVersionData(_ string, _ string, _ *pkgmodel.Resource) error {
	panic("not implemented")
}
func (m *mockExtractDatastore) LoadReapedResources() ([]*pkgmodel.Resource, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) LatestLabelForResource(_ string) (string, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) LoadResourceById(_ string) (*pkgmodel.Resource, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) LoadLatestResourceByKsuid(_ string) (*pkgmodel.Resource, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) FindResourcesDependingOn(_ string) ([]*pkgmodel.Resource, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) FindResourcesReferencingGenerator(_ string) ([]*pkgmodel.Resource, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) FindResourcesDependingOnMany(_ []string) (map[string][]*pkgmodel.Resource, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) FindTargetsDependingOnMany(_ []string) (map[string][]*pkgmodel.Target, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) BulkStoreResources(_ []pkgmodel.Resource, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) LoadResourcesByStack(_ string) ([]*pkgmodel.Resource, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) LoadAllResourcesByStack() (map[string][]*pkgmodel.Resource, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) CreateStack(_ *pkgmodel.Stack, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) UpdateStack(_ *pkgmodel.Stack, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) DeleteStack(_ string, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) CountResourcesInStack(_ string) (int, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) ListAllStacks() ([]*pkgmodel.Stack, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) CreateTarget(_ *pkgmodel.Target) (string, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) UpdateTarget(_ *pkgmodel.Target) (string, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) LoadTarget(_ string) (*pkgmodel.Target, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) LoadAllTargets() ([]*pkgmodel.Target, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) LoadDiscoverableTargets() ([]*pkgmodel.Target, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) QueryTargets(_ *datastore.TargetQuery) ([]*pkgmodel.Target, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) DeleteTarget(_ string) (string, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) CountResourcesInTarget(_ string) (int, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) Stats() (*stats.Stats, error) { panic("not implemented") }
func (m *mockExtractDatastore) GetKSUIDByTriplet(_, _, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) BatchGetKSUIDsByTriplets(_ []pkgmodel.TripletKey) (map[pkgmodel.TripletKey]string, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) CreatePolicy(_ pkgmodel.Policy, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) UpdatePolicy(_ pkgmodel.Policy, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) GetPoliciesForStack(_ string) ([]pkgmodel.Policy, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) GetInlinePoliciesForStack(_ string) ([]pkgmodel.Policy, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) ListAllStandalonePolicies() ([]pkgmodel.Policy, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) AttachPolicyToStack(_, _ string) error { panic("not implemented") }
func (m *mockExtractDatastore) IsPolicyAttachedToStack(_, _ string) (bool, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) GetStacksReferencingPolicy(_ string) ([]string, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) GetAttachedPolicyLabelsForStack(_ string) ([]string, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) DetachPolicyFromStack(_, _ string) error { panic("not implemented") }
func (m *mockExtractDatastore) DeletePolicy(_ string) (string, error)   { panic("not implemented") }
func (m *mockExtractDatastore) DeleteInlinePolicy(_, _, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) DeletePoliciesForStack(_ string, _ string) error {
	panic("not implemented")
}
func (m *mockExtractDatastore) GetExpiredStacks() ([]datastore.ExpiredStackInfo, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) GetGeneratorsWithRotation() ([]datastore.GeneratorRotationInfo, error) {
	return nil, nil
}

func (m *mockExtractDatastore) GetStacksWithAutoReconcilePolicy() ([]datastore.StackReconcileInfo, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) GetResourcesAtLastReconcile(_ string) ([]datastore.ResourceSnapshot, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) StackHasActiveCommands(_ string) (bool, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) CreateGenerator(_ pkgmodel.Generator, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) UpdateGenerator(_ pkgmodel.Generator, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) DeleteGenerator(_, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) GetGenerator(_, _ string) (pkgmodel.Generator, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) LoadGeneratorsByStack(_ string) ([]pkgmodel.Generator, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) GetGeneratorIdentity(_, _ string) (datastore.GeneratorIdentity, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) GetGeneratorIdentityByID(_ string) (datastore.GeneratorIdentity, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) AdvanceGeneration(_, _, _ string, _ json.RawMessage) error {
	panic("not implemented")
}
func (m *mockExtractDatastore) Close() {}
func (m *mockExtractDatastore) BulkStoreResourceUpdates(_ string, _ []resource_update.ResourceUpdate) error {
	panic("not implemented")
}
func (m *mockExtractDatastore) LoadResourceUpdates(_ string) ([]resource_update.ResourceUpdate, error) {
	panic("not implemented")
}
func (m *mockExtractDatastore) UpdateResourceUpdateState(_ string, _ string, _ types.OperationType, _ resource_update.ResourceUpdateState, _ time.Time) error {
	panic("not implemented")
}
func (m *mockExtractDatastore) UpdateResourceUpdateProgress(_ string, _ string, _ types.OperationType, _ resource_update.ResourceUpdateState, _ time.Time, _ time.Time, _ plugin.TrackedProgress, _ map[string]string) error {
	panic("not implemented")
}
func (m *mockExtractDatastore) BatchUpdateResourceUpdateState(_ string, _ []datastore.ResourceUpdateRef, _ resource_update.ResourceUpdateState, _ time.Time) error {
	panic("not implemented")
}
func (m *mockExtractDatastore) UpdateFormaCommandProgress(_ string, _ forma_command.CommandState, _ time.Time) error {
	panic("not implemented")
}
func (m *mockExtractDatastore) UpdateFormaCommandTargetUpdates(_ string, _ json.RawMessage, _ forma_command.CommandState, _ time.Time) error {
	panic("not implemented")
}
func (m *mockExtractDatastore) ForceCancelResourceUpdates(_ string, _ []datastore.ForceCancelRow, _ []datastore.ResourceUpdateRef, _ time.Time) (datastore.ForceCancelResult, error) {
	panic("not implemented")
}

func (m *mockExtractDatastore) UpdateTargetHealth(_ pkgmodel.TargetHealthObservation) (bool, error) {
	panic("not implemented")
}

func (m *mockExtractDatastore) AdvanceTargetAccrual(_, _ string, _ time.Time, _ int64) (bool, error) {
	panic("not implemented")
}

func (m *mockExtractDatastore) GetUnreachableTargets() ([]*pkgmodel.Target, error) {
	panic("not implemented")
}

func (m *mockExtractDatastore) PersistTargetReap(_ datastore.PersistTargetReapRequest) (bool, []string, error) {
	panic("not implemented")
}

func (m *mockExtractDatastore) CheckTargetsReaped(_ []string) ([]string, error) {
	panic("not implemented")
}

func TestExtractResources_ManagedAndUnmanagedStacks(t *testing.T) {
	managedStack := &pkgmodel.Stack{
		Label:       "my-stack",
		Description: "My managed stack",
	}

	target := &pkgmodel.Target{
		Label:     "aws-target",
		Namespace: "AWS",
		Config:    json.RawMessage(`{"Region":"us-east-1"}`),
	}

	ds := &mockExtractDatastore{
		resources: []*pkgmodel.Resource{
			{
				Label:      "my-vpc",
				Type:       "AWS::EC2::VPC",
				Stack:      "my-stack",
				Target:     "aws-target",
				Properties: json.RawMessage(`{"CidrBlock":"10.0.0.0/16"}`),
			},
			{
				Label:      "discovered-vpc",
				Type:       "AWS::EC2::VPC",
				Stack:      constants.UnmanagedStack,
				Target:     "aws-target",
				Properties: json.RawMessage(`{"CidrBlock":"172.16.0.0/16"}`),
			},
		},
		targets: map[string]*pkgmodel.Target{
			"aws-target": target,
		},
		stacks: map[string]*pkgmodel.Stack{
			"my-stack": managedStack,
			// $unmanaged is NOT in the datastore
		},
		policies: map[string]pkgmodel.Policy{},
	}

	m := &Metastructure{Datastore: ds}
	forma, err := m.ExtractResources("")
	require.NoError(t, err)
	require.NotNil(t, forma)

	// Should have both resources
	assert.Len(t, forma.Resources, 2)

	// Should have the target
	require.Len(t, forma.Targets, 1)
	assert.Equal(t, "aws-target", forma.Targets[0].Label)

	// Should have both stacks - managed and unmanaged
	require.Len(t, forma.Stacks, 2, "should include both managed and unmanaged stacks")

	stacksByLabel := map[string]pkgmodel.Stack{}
	for _, s := range forma.Stacks {
		stacksByLabel[s.Label] = s
	}

	// Managed stack should come from the datastore with its original description
	managed, ok := stacksByLabel["my-stack"]
	require.True(t, ok, "should have managed stack")
	assert.Equal(t, "My managed stack", managed.Description)

	// Unmanaged stack is synthesized with NO description: description is optional,
	// and leaving it empty means adopting these resources into an existing stack
	// won't overwrite a description the user already set.
	unmanaged, ok := stacksByLabel[constants.UnmanagedStack]
	require.True(t, ok, "should have $unmanaged stack")
	assert.Empty(t, unmanaged.Description,
		"$unmanaged stack should be synthesized without a description")

	// Verify the forma can be serialized to JSON with Stacks present
	jsonBytes, err := json.Marshal(forma)
	require.NoError(t, err)
	assert.Contains(t, string(jsonBytes), `"Stacks"`)
	assert.Contains(t, string(jsonBytes), constants.UnmanagedStack)
}

func TestExtractResources_OnlyUnmanagedStack(t *testing.T) {
	target := &pkgmodel.Target{
		Label:     "aws-target",
		Namespace: "AWS",
		Config:    json.RawMessage(`{"Region":"us-east-1"}`),
	}

	ds := &mockExtractDatastore{
		resources: []*pkgmodel.Resource{
			{
				Label:      "discovered-bucket",
				Type:       "AWS::S3::Bucket",
				Stack:      constants.UnmanagedStack,
				Target:     "aws-target",
				Properties: json.RawMessage(`{"BucketName":"my-bucket"}`),
			},
		},
		targets: map[string]*pkgmodel.Target{
			"aws-target": target,
		},
		stacks:   map[string]*pkgmodel.Stack{},
		policies: map[string]pkgmodel.Policy{},
	}

	m := &Metastructure{Datastore: ds}
	forma, err := m.ExtractResources("")
	require.NoError(t, err)
	require.NotNil(t, forma)

	// Should have the unmanaged stack even though it's not in the datastore,
	// synthesized without a description (optional; empty means "unset").
	require.Len(t, forma.Stacks, 1, "should have exactly one stack")
	assert.Equal(t, constants.UnmanagedStack, forma.Stacks[0].Label)
	assert.Empty(t, forma.Stacks[0].Description)

	// Verify JSON serialization includes Stacks (not omitted by omitempty)
	jsonBytes, err := json.Marshal(forma)
	require.NoError(t, err)
	assert.Contains(t, string(jsonBytes), `"Stacks"`)
}

// TestExtractResources_BatchedLookups verifies that ExtractResources issues exactly
// one batched call for stacks and one for policies, and never falls back to the
// per-item GetStackByLabel / GetStandalonePolicy methods.
func TestExtractResources_BatchedLookups(t *testing.T) {
	managedStack := &pkgmodel.Stack{
		Label:       "stack-a",
		Description: "Stack A",
	}
	anotherStack := &pkgmodel.Stack{
		Label:       "stack-b",
		Description: "Stack B",
	}

	target := &pkgmodel.Target{
		Label:     "aws-target",
		Namespace: "AWS",
		Config:    json.RawMessage(`{"Region":"us-east-1"}`),
	}

	ds := &mockExtractDatastore{
		resources: []*pkgmodel.Resource{
			{
				Label:      "res-1",
				Type:       "AWS::EC2::VPC",
				Stack:      "stack-a",
				Target:     "aws-target",
				Properties: json.RawMessage(`{"CidrBlock":"10.0.0.0/16"}`),
			},
			{
				Label:      "res-2",
				Type:       "AWS::EC2::Subnet",
				Stack:      "stack-b",
				Target:     "aws-target",
				Properties: json.RawMessage(`{"CidrBlock":"10.0.1.0/24"}`),
			},
			{
				Label:      "res-3",
				Type:       "AWS::S3::Bucket",
				Stack:      "stack-a", // duplicate stack — still one batch call
				Target:     "aws-target",
				Properties: json.RawMessage(`{"BucketName":"my-bucket"}`),
			},
		},
		targets: map[string]*pkgmodel.Target{
			"aws-target": target,
		},
		stacks: map[string]*pkgmodel.Stack{
			"stack-a": managedStack,
			"stack-b": anotherStack,
		},
		policies: map[string]pkgmodel.Policy{},
	}

	m := &Metastructure{Datastore: ds}
	forma, err := m.ExtractResources("")
	require.NoError(t, err)
	require.NotNil(t, forma)

	// Results must be correct (both stacks present)
	require.Len(t, forma.Stacks, 2)
	stackLabels := map[string]bool{}
	for _, s := range forma.Stacks {
		stackLabels[s.Label] = true
	}
	assert.True(t, stackLabels["stack-a"], "stack-a should be present")
	assert.True(t, stackLabels["stack-b"], "stack-b should be present")

	// Access-pattern assertions: exactly one batched call, zero per-item calls
	assert.Equal(t, 1, ds.loadStacksByLabelsCalls, "LoadStacksByLabels must be called exactly once")
	assert.Equal(t, 0, ds.getStackByLabelCalls, "GetStackByLabel must not be called (N+1 avoided)")
	assert.Equal(t, 0, ds.loadStandalonePoliciesByLabelsCalls,
		"LoadStandalonePoliciesByLabels must not be called when there are no policy references")
	assert.Equal(t, 0, ds.getStandalonePolicyCalls, "GetStandalonePolicy must not be called (N+1 avoided)")
}

// TestExtractResources_BatchedPolicyLookups exercises the positive side of the
// batched-policy path: a stack carries a standalone policy reference, so the
// policy must be resolved via the single batched call, never the per-item one,
// and it must actually land in forma.Policies.
func TestExtractResources_BatchedPolicyLookups(t *testing.T) {
	stackWithPolicy := &pkgmodel.Stack{
		Label:       "stack-a",
		Description: "Stack A",
		Policies: []json.RawMessage{
			json.RawMessage(`{"$ref":"policy://my-policy"}`),
		},
	}

	target := &pkgmodel.Target{
		Label:     "aws-target",
		Namespace: "AWS",
		Config:    json.RawMessage(`{"Region":"us-east-1"}`),
	}

	ds := &mockExtractDatastore{
		resources: []*pkgmodel.Resource{
			{
				Label:      "res-1",
				Type:       "AWS::EC2::VPC",
				Stack:      "stack-a",
				Target:     "aws-target",
				Properties: json.RawMessage(`{"CidrBlock":"10.0.0.0/16"}`),
			},
		},
		targets: map[string]*pkgmodel.Target{
			"aws-target": target,
		},
		stacks: map[string]*pkgmodel.Stack{
			"stack-a": stackWithPolicy,
		},
		policies: map[string]pkgmodel.Policy{
			"my-policy": &pkgmodel.TTLPolicy{
				Type:         "ttl",
				Label:        "my-policy",
				TTLSeconds:   3600,
				OnDependents: "cascade",
			},
		},
	}

	m := &Metastructure{Datastore: ds}
	forma, err := m.ExtractResources("")
	require.NoError(t, err)
	require.NotNil(t, forma)

	// The referenced policy must have been loaded and marshalled into the forma.
	require.Len(t, forma.Policies, 1, "the referenced standalone policy should be loaded")
	assert.Contains(t, string(forma.Policies[0]), "my-policy")

	// Access-pattern assertions: exactly one batched call, zero per-item calls.
	assert.Equal(t, 1, ds.loadStandalonePoliciesByLabelsCalls,
		"LoadStandalonePoliciesByLabels must be called exactly once")
	assert.Equal(t, 0, ds.getStandalonePolicyCalls,
		"GetStandalonePolicy must not be called (N+1 avoided)")
	assert.Equal(t, 1, ds.loadStacksByLabelsCalls, "LoadStacksByLabels must be called exactly once")
	assert.Equal(t, 0, ds.getStackByLabelCalls, "GetStackByLabel must not be called (N+1 avoided)")
}

func (m *mockExtractDatastore) RecordAgentBoot(_ string) error {
	return nil
}
