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
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

const (
	CommandsTable                  string              = "forma_commands"
	DefaultFormaCommandsQueryLimit                     = 10
	Optional                       QueryItemConstraint = iota
	Required
	Excluded
)

type QueryItemConstraint int

type QueryItem[T any] struct {
	Item       T
	Constraint QueryItemConstraint
}

type StatusQuery struct {
	CommandID *QueryItem[string]
	ClientID  *QueryItem[string]
	Command   *QueryItem[string]
	Status    *QueryItem[string]
	Stack     *QueryItem[string]
	Managed   *QueryItem[bool]
	N         int
}

type ResourceQuery struct {
	Stack            *QueryItem[string]
	Type             *QueryItem[string]
	Label            *QueryItem[string]
	Target           *QueryItem[string]
	LastChangeStatus *QueryItem[string]
	NativeID         *QueryItem[string]
	Managed          *QueryItem[bool]
	N                int
}

type DestroyResourcesQuery struct {
	Stack    *QueryItem[string]
	Type     *QueryItem[string]
	Label    *QueryItem[string]
	Target   *QueryItem[string]
	NativeID *QueryItem[string]
}

type TargetQuery struct {
	Label        *QueryItem[string]
	Namespace    *QueryItem[string]
	Discoverable *QueryItem[bool]
	N            int
}

type ResourceModification struct {
	Stack     string
	Type      string
	Label     string
	Operation string
}

// ResourceUpdateRef identifies a specific ResourceUpdate by its key components
// Used for batch operations on ResourceUpdates
type ResourceUpdateRef struct {
	KSUID     string
	Operation types.OperationType
}

// ExpiredStackInfo contains information about a stack whose TTL policy has expired
type ExpiredStackInfo struct {
	StackLabel   string
	StackID      string
	OnDependents string // "abort" or "cascade"
}

// StackReconcileInfo contains information about a stack with an auto-reconcile policy
type StackReconcileInfo struct {
	StackLabel      string
	StackID         string
	IntervalSeconds int64
	LastReconcileAt time.Time
}

// ResourceSnapshot contains resource state at a point in time
type ResourceSnapshot struct {
	KSUID      string
	Type       string
	Label      string
	Target     string
	Properties json.RawMessage
	NativeID   string
	Schema     pkgmodel.Schema
}

// Datastore defines the persistence interface for formae.
// It handles storage and retrieval of FormaCommands (requested changes),
// Resources (actual cloud state), Stacks, and Targets.
type Datastore interface {
	// FormaCommand operations - these represent requested changes to infrastructure

	// StoreFormaCommand persists a new FormaCommand with its ResourceUpdates
	StoreFormaCommand(fa *forma_command.FormaCommand, commandID string) error
	// LoadFormaCommands returns all stored FormaCommands
	LoadFormaCommands() ([]*forma_command.FormaCommand, error)
	// LoadIncompleteFormaCommands returns FormaCommands that haven't reached a terminal state
	LoadIncompleteFormaCommands() ([]*forma_command.FormaCommand, error)
	// DeleteFormaCommand removes a FormaCommand and its associated ResourceUpdates
	DeleteFormaCommand(fa *forma_command.FormaCommand, commandID string) error
	// GetFormaCommandByCommandID retrieves a single FormaCommand by its ID
	GetFormaCommandByCommandID(commandID string) (*forma_command.FormaCommand, error)
	// GetMostRecentFormaCommandByClientID returns the latest command for a given client
	GetMostRecentFormaCommandByClientID(clientID string) (*forma_command.FormaCommand, error)
	// GetResourceModificationsSinceLastReconcile returns resources modified since the last reconcile
	GetResourceModificationsSinceLastReconcile(stack string) ([]ResourceModification, error)
	// QueryFormaCommands searches commands based on filter criteria
	QueryFormaCommands(query *StatusQuery) ([]*forma_command.FormaCommand, error)

	// Resource operations - these represent actual cloud state

	// QueryResources searches resources based on filter criteria
	QueryResources(query *ResourceQuery) ([]*pkgmodel.Resource, error)
	// StoreResource persists a resource after successful creation/update in the cloud
	StoreResource(resource *pkgmodel.Resource, commandID string) (string, error)
	// DeleteResource removes a resource record after successful deletion in the cloud
	DeleteResource(resource *pkgmodel.Resource, commandID string) (string, error)
	// LoadResource retrieves a resource by its formae URI
	LoadResource(uri pkgmodel.FormaeURI) (*pkgmodel.Resource, error)
	// LoadResourceByNativeID finds a resource by its cloud provider native ID
	LoadResourceByNativeID(nativeID string, resourceType string) (*pkgmodel.Resource, error)
	// LoadAllResources returns all stored resources
	LoadAllResources() ([]*pkgmodel.Resource, error)
	// LatestLabelForResource returns the most recent label variant for a resource
	LatestLabelForResource(label string) (string, error)
	// LoadResourceById retrieves a resource by its KSUID
	LoadResourceById(ksuid string) (*pkgmodel.Resource, error)
	// FindResourcesDependingOn returns all resources that reference the given resource via $ref
	FindResourcesDependingOn(ksuid string) ([]*pkgmodel.Resource, error)
	// FindResourcesDependingOnMany returns all resources that reference any of the given resources via $ref.
	// Returns a map from referenced KSUID to the resources that depend on it.
	FindResourcesDependingOnMany(ksuids []string) (map[string][]*pkgmodel.Resource, error)

	// Resource-by-stack operations - query resources grouped by stack

	// BulkStoreResources persists multiple resources in a single operation
	BulkStoreResources(resources []pkgmodel.Resource, commandID string) (string, error)
	// LoadResourcesByStack retrieves all resources belonging to a stack
	LoadResourcesByStack(stackLabel string) ([]*pkgmodel.Resource, error)
	// LoadAllResourcesByStack returns all resources grouped by stack label
	LoadAllResourcesByStack() (map[string][]*pkgmodel.Resource, error)

	// Stack metadata operations - persisted stack definitions with id, label, description

	// CreateStack persists a new stack entry (returns version string)
	CreateStack(stack *pkgmodel.Stack, commandID string) (string, error)
	// UpdateStack modifies an existing stack entry (returns version string)
	UpdateStack(stack *pkgmodel.Stack, commandID string) (string, error)
	// DeleteStack tombstones a stack entry (returns version string)
	DeleteStack(label string, commandID string) (string, error)
	// GetStackByLabel retrieves stack by its label (latest non-deleted version)
	GetStackByLabel(label string) (*pkgmodel.Stack, error)
	// CountResourcesInStack returns the count of non-deleted resources in a stack
	CountResourcesInStack(label string) (int, error)
	// ListAllStacks returns all non-deleted stack entries
	ListAllStacks() ([]*pkgmodel.Stack, error)

	// Target operations - cloud provider configurations

	// CreateTarget persists a new target configuration
	CreateTarget(target *pkgmodel.Target) (string, error)
	// UpdateTarget modifies an existing target configuration
	UpdateTarget(target *pkgmodel.Target) (string, error)
	// LoadTarget retrieves a target by its label
	LoadTarget(targetLabel string) (*pkgmodel.Target, error)
	// LoadAllTargets returns all stored targets
	LoadAllTargets() ([]*pkgmodel.Target, error)
	// LoadTargetsByLabels retrieves multiple targets by their labels
	LoadTargetsByLabels(targetNames []string) ([]*pkgmodel.Target, error)
	// LoadDiscoverableTargets returns targets that have discovery enabled
	LoadDiscoverableTargets() ([]*pkgmodel.Target, error)
	// QueryTargets searches targets based on filter criteria
	QueryTargets(query *TargetQuery) ([]*pkgmodel.Target, error)
	// DeleteTarget removes a target by its label (hard delete all versions)
	DeleteTarget(targetLabel string) (string, error)
	// CountResourcesInTarget returns the count of non-deleted resources belonging to a target
	CountResourcesInTarget(targetLabel string) (int, error)

	// Stats returns aggregated statistics about the datastore contents
	Stats() (*stats.Stats, error)

	// KSUID/Triplet mapping - conversion between internal IDs and user-facing identifiers

	// GetKSUIDByTriplet converts a (stack, label, type) triplet to a KSUID
	GetKSUIDByTriplet(stack, label, resourceType string) (string, error)
	// BatchGetKSUIDsByTriplets converts multiple triplets to KSUIDs in one query
	BatchGetKSUIDsByTriplets(triplets []pkgmodel.TripletKey) (map[pkgmodel.TripletKey]string, error)
	// BatchGetTripletsByKSUIDs converts multiple KSUIDs to triplets in one query
	BatchGetTripletsByKSUIDs(ksuids []string) (map[string]pkgmodel.TripletKey, error)

	// Policy operations - policies define behaviors attached to stacks

	// CreatePolicy persists a new policy (returns version string)
	CreatePolicy(policy pkgmodel.Policy, commandID string) (string, error)
	// UpdatePolicy updates an existing policy (returns version string)
	UpdatePolicy(policy pkgmodel.Policy, commandID string) (string, error)
	// GetPoliciesForStack returns all non-deleted policies for a given stack ID
	GetPoliciesForStack(stackID string) ([]pkgmodel.Policy, error)
	// GetStandalonePolicy retrieves a standalone policy by label (stack_id IS NULL)
	// Returns nil, nil if no policy is found
	GetStandalonePolicy(label string) (pkgmodel.Policy, error)
	// ListAllStandalonePolicies returns all non-deleted standalone policies (stack_id IS NULL)
	ListAllStandalonePolicies() ([]pkgmodel.Policy, error)
	// AttachPolicyToStack creates an association between a standalone policy and a stack
	// in the stack_policies junction table. Used for standalone policies referenced via $ref.
	AttachPolicyToStack(stackID, policyLabel string) error
	// IsPolicyAttachedToStack checks if a standalone policy is attached to a stack via the junction table
	IsPolicyAttachedToStack(stackLabel, policyLabel string) (bool, error)
	// GetStacksReferencingPolicy returns the labels of all stacks that reference a standalone policy
	GetStacksReferencingPolicy(policyLabel string) ([]string, error)
	// GetAttachedPolicyLabelsForStack returns the labels of all standalone policies attached to a stack
	GetAttachedPolicyLabelsForStack(stackLabel string) ([]string, error)
	// DetachPolicyFromStack removes the association between a standalone policy and a stack
	DetachPolicyFromStack(stackLabel, policyLabel string) error
	// DeletePolicy soft-deletes a standalone policy by label (returns version string)
	DeletePolicy(policyLabel string) (string, error)
	// DeletePoliciesForStack soft-deletes all policies for a stack (cascade delete)
	DeletePoliciesForStack(stackID string, commandID string) error
	// GetExpiredStacks returns stacks with TTL policies that have expired,
	// excluding stacks with active forma commands to avoid inconsistent state
	GetExpiredStacks() ([]ExpiredStackInfo, error)
	// GetStacksWithAutoReconcilePolicy returns stacks with auto-reconcile policies,
	// along with their interval configuration and last reconcile timestamp
	GetStacksWithAutoReconcilePolicy() ([]StackReconcileInfo, error)
	// GetResourcesAtLastReconcile returns the resource state as of the last reconcile
	// command for the given stack
	GetResourcesAtLastReconcile(stackLabel string) ([]ResourceSnapshot, error)

	// Close releases database connections
	Close()

	// ResourceUpdate methods for normalized schema
	// These methods work with the resource_updates table for improved write performance

	// BulkStoreResourceUpdates stores multiple ResourceUpdates in a single transaction
	// Used when creating a new FormaCommand
	BulkStoreResourceUpdates(commandID string, updates []resource_update.ResourceUpdate) error

	// LoadResourceUpdates loads all ResourceUpdates for a given command
	LoadResourceUpdates(commandID string) ([]resource_update.ResourceUpdate, error)

	// UpdateResourceUpdateState updates the state of a single ResourceUpdate
	// This is the key performance improvement: updating one row instead of re-serializing entire command
	UpdateResourceUpdateState(commandID string, ksuid string, operation types.OperationType, state resource_update.ResourceUpdateState, modifiedTs time.Time) error

	// UpdateResourceUpdateProgress updates a ResourceUpdate with progress information
	UpdateResourceUpdateProgress(commandID string, ksuid string, operation types.OperationType, state resource_update.ResourceUpdateState, modifiedTs time.Time, progress plugin.TrackedProgress) error

	// BatchUpdateResourceUpdateState updates multiple ResourceUpdates to the same state
	// Used for bulk operations like marking dependent resources as failed
	BatchUpdateResourceUpdateState(commandID string, refs []ResourceUpdateRef, state resource_update.ResourceUpdateState, modifiedTs time.Time) error

	// UpdateFormaCommandProgress updates only the command-level metadata (state, modified_ts)
	// without re-writing all ResourceUpdates. This is a performance optimization for
	// progress updates where the ResourceUpdate is already updated via UpdateResourceUpdateProgress.
	UpdateFormaCommandProgress(commandID string, state forma_command.CommandState, modifiedTs time.Time) error
}

// resourcesAreEqual compares two resources and returns two booleans: the first one indicating whether the
// non-readonly properties of the resources are equal, and the second one indicating whether the readonly i
// properties are equal.
func resourcesAreEqual(resource1, resource2 *pkgmodel.Resource) (bool, bool) {
	readWriteEqual, readOnlyEqual := true, true

	// Compare table fields
	if resource1.NativeID != resource2.NativeID ||
		resource1.Stack != resource2.Stack ||
		resource1.Type != resource2.Type ||
		resource1.Label != resource2.Label {
		readWriteEqual = false
	}

	if !util.JsonEqualRaw(resource1.Properties, resource2.Properties) {
		readWriteEqual = false
	}

	if !util.JsonEqualRaw(resource1.ReadOnlyProperties, resource2.ReadOnlyProperties) {
		readOnlyEqual = false
	}

	return readWriteEqual, readOnlyEqual
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
