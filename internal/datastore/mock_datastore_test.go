// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"encoding/json"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stats"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// mockDatastore satisfies the Datastore interface for registry tests.
// Only the name field is used; all interface methods are stubs.
type mockDatastore struct {
	name string
}

func (m *mockDatastore) StoreFormaCommand(_ *forma_command.FormaCommand, _ string) error {
	return nil
}
func (m *mockDatastore) LoadFormaCommands() ([]*forma_command.FormaCommand, error) {
	return nil, nil
}
func (m *mockDatastore) LoadIncompleteFormaCommands() ([]*forma_command.FormaCommand, error) {
	return nil, nil
}
func (m *mockDatastore) DeleteFormaCommand(_ *forma_command.FormaCommand, _ string) error {
	return nil
}
func (m *mockDatastore) GetFormaCommandByCommandID(_ string) (*forma_command.FormaCommand, error) {
	return nil, nil
}
func (m *mockDatastore) GetMostRecentFormaCommandByClientID(_ string) (*forma_command.FormaCommand, error) {
	return nil, nil
}
func (m *mockDatastore) GetResourceModificationsSinceLastReconcile(_ string) ([]ResourceModification, error) {
	return nil, nil
}
func (m *mockDatastore) QueryFormaCommands(_ *StatusQuery) ([]*forma_command.FormaCommand, error) {
	return nil, nil
}
func (m *mockDatastore) QueryResources(_ *ResourceQuery) ([]*pkgmodel.Resource, error) {
	return nil, nil
}
func (m *mockDatastore) StoreResource(_ *pkgmodel.Resource, _ string) (string, error) {
	return "", nil
}
func (m *mockDatastore) DeleteResource(_ *pkgmodel.Resource, _ string) (string, error) {
	return "", nil
}
func (m *mockDatastore) LoadResource(_ pkgmodel.FormaeURI) (*pkgmodel.Resource, error) {
	return nil, nil
}
func (m *mockDatastore) LoadResourceByNativeID(_, _ string) (*pkgmodel.Resource, error) {
	return nil, nil
}
func (m *mockDatastore) LoadAllResources() ([]*pkgmodel.Resource, error) { return nil, nil }
func (m *mockDatastore) LatestLabelForResource(_ string) (string, error) { return "", nil }
func (m *mockDatastore) LoadResourceById(_ string) (*pkgmodel.Resource, error) {
	return nil, nil
}
func (m *mockDatastore) FindResourcesDependingOn(_ string) ([]*pkgmodel.Resource, error) {
	return nil, nil
}
func (m *mockDatastore) FindResourcesDependingOnMany(_ []string) (map[string][]*pkgmodel.Resource, error) {
	return nil, nil
}
func (m *mockDatastore) FindTargetsDependingOnMany(_ []string) (map[string][]*pkgmodel.Target, error) {
	return make(map[string][]*pkgmodel.Target), nil
}
func (m *mockDatastore) BulkStoreResources(_ []pkgmodel.Resource, _ string) (string, error) {
	return "", nil
}
func (m *mockDatastore) LoadResourcesByStack(_ string) ([]*pkgmodel.Resource, error) {
	return nil, nil
}
func (m *mockDatastore) LoadAllResourcesByStack() (map[string][]*pkgmodel.Resource, error) {
	return nil, nil
}
func (m *mockDatastore) CreateStack(_ *pkgmodel.Stack, _ string) (string, error) { return "", nil }
func (m *mockDatastore) UpdateStack(_ *pkgmodel.Stack, _ string) (string, error) { return "", nil }
func (m *mockDatastore) DeleteStack(_, _ string) (string, error)                 { return "", nil }
func (m *mockDatastore) GetStackByLabel(_ string) (*pkgmodel.Stack, error)       { return nil, nil }
func (m *mockDatastore) CountResourcesInStack(_ string) (int, error)             { return 0, nil }
func (m *mockDatastore) ListAllStacks() ([]*pkgmodel.Stack, error)               { return nil, nil }
func (m *mockDatastore) CreateTarget(_ *pkgmodel.Target) (string, error)         { return "", nil }
func (m *mockDatastore) UpdateTarget(_ *pkgmodel.Target) (string, error)         { return "", nil }
func (m *mockDatastore) LoadTarget(_ string) (*pkgmodel.Target, error)           { return nil, nil }
func (m *mockDatastore) LoadAllTargets() ([]*pkgmodel.Target, error)             { return nil, nil }
func (m *mockDatastore) LoadTargetsByLabels(_ []string) ([]*pkgmodel.Target, error) {
	return nil, nil
}
func (m *mockDatastore) LoadDiscoverableTargets() ([]*pkgmodel.Target, error) { return nil, nil }
func (m *mockDatastore) QueryTargets(_ *TargetQuery) ([]*pkgmodel.Target, error) {
	return nil, nil
}
func (m *mockDatastore) DeleteTarget(_ string) (string, error)        { return "", nil }
func (m *mockDatastore) CountResourcesInTarget(_ string) (int, error) { return 0, nil }
func (m *mockDatastore) Stats() (*stats.Stats, error)                 { return nil, nil }
func (m *mockDatastore) GetKSUIDByTriplet(_, _, _ string) (string, error) {
	return "", nil
}
func (m *mockDatastore) BatchGetKSUIDsByTriplets(_ []pkgmodel.TripletKey) (map[pkgmodel.TripletKey]string, error) {
	return nil, nil
}
func (m *mockDatastore) BatchGetTripletsByKSUIDs(_ []string) (map[string]pkgmodel.TripletKey, error) {
	return nil, nil
}
func (m *mockDatastore) CreatePolicy(_ pkgmodel.Policy, _ string) (string, error) { return "", nil }
func (m *mockDatastore) UpdatePolicy(_ pkgmodel.Policy, _ string) (string, error) { return "", nil }
func (m *mockDatastore) GetPoliciesForStack(_ string) ([]pkgmodel.Policy, error) {
	return nil, nil
}
func (m *mockDatastore) GetStandalonePolicy(_ string) (pkgmodel.Policy, error) {
	return nil, nil
}
func (m *mockDatastore) ListAllStandalonePolicies() ([]pkgmodel.Policy, error) { return nil, nil }
func (m *mockDatastore) AttachPolicyToStack(_, _ string) error                 { return nil }
func (m *mockDatastore) IsPolicyAttachedToStack(_, _ string) (bool, error)     { return false, nil }
func (m *mockDatastore) GetStacksReferencingPolicy(_ string) ([]string, error) {
	return nil, nil
}
func (m *mockDatastore) GetAttachedPolicyLabelsForStack(_ string) ([]string, error) {
	return nil, nil
}
func (m *mockDatastore) DetachPolicyFromStack(_, _ string) error       { return nil }
func (m *mockDatastore) DeletePolicy(_ string) (string, error)         { return "", nil }
func (m *mockDatastore) DeletePoliciesForStack(_, _ string) error      { return nil }
func (m *mockDatastore) GetExpiredStacks() ([]ExpiredStackInfo, error) { return nil, nil }
func (m *mockDatastore) GetStacksWithAutoReconcilePolicy() ([]StackReconcileInfo, error) {
	return nil, nil
}
func (m *mockDatastore) GetResourcesAtLastReconcile(_ string) ([]ResourceSnapshot, error) {
	return nil, nil
}
func (m *mockDatastore) StackHasActiveCommands(_ string) (bool, error) { return false, nil }
func (m *mockDatastore) Close()                                        {}
func (m *mockDatastore) BulkStoreResourceUpdates(_ string, _ []resource_update.ResourceUpdate) error {
	return nil
}
func (m *mockDatastore) LoadResourceUpdates(_ string) ([]resource_update.ResourceUpdate, error) {
	return nil, nil
}
func (m *mockDatastore) UpdateResourceUpdateState(_ string, _ string, _ types.OperationType, _ resource_update.ResourceUpdateState, _ time.Time) error {
	return nil
}
func (m *mockDatastore) UpdateResourceUpdateProgress(_ string, _ string, _ types.OperationType, _ resource_update.ResourceUpdateState, _ time.Time, _ plugin.TrackedProgress) error {
	return nil
}
func (m *mockDatastore) BatchUpdateResourceUpdateState(_ string, _ []ResourceUpdateRef, _ resource_update.ResourceUpdateState, _ time.Time) error {
	return nil
}
func (m *mockDatastore) UpdateFormaCommandProgress(_ string, _ forma_command.CommandState, _ time.Time) error {
	return nil
}
func (m *mockDatastore) UpdateFormaCommandTargetUpdates(_ string, _ json.RawMessage, _ forma_command.CommandState, _ time.Time) error {
	return nil
}
