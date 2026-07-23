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

const (
	CommandsTable                  string              = "forma_commands"
	DefaultFormaCommandsQueryLimit                     = 10
	Optional                       QueryItemConstraint = iota
	Required
	Excluded
)

type QueryItemConstraint int

// QueryItem is one filter clause in a query.
//
// Item plus ExtraItems form the value set: a single-valued filter has
// ExtraItems == nil. A multi-valued filter (e.g. `target:eu target:us` for
// "target IN (eu, us)") puts the first value in Item and additional values
// in ExtraItems. The SQL renderer combines them with OR / IN as appropriate.
//
// String values may carry leading or trailing `*` wildcards (e.g. `*foo`,
// `foo*`) which the renderer translates to SQL LIKE patterns.
type QueryItem[T any] struct {
	Item       T
	ExtraItems []T
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
	Stack         string
	Type          string
	Label         string
	Operation     string
	Properties    json.RawMessage // current (cloud) properties — update ops only
	OldProperties json.RawMessage // properties at last reconcile — update ops only
}

// ResourceUpdateRef identifies a specific ResourceUpdate by its key components.
// Used for batch operations on ResourceUpdates.
type ResourceUpdateRef struct {
	KSUID     string
	Operation types.OperationType
}

// ForceCancelRow is an in-progress resource update that should be force-canceled.
// It carries the progress JSON to append to the row's progress_result, and the
// serialized most-recent-progress entry to store as most_recent_progress.
type ForceCancelRow struct {
	KSUID                  string
	Operation              types.OperationType
	ProgressJSON           json.RawMessage // full serialized []plugin.TrackedProgress to write as progress_result
	MostRecentProgressJSON json.RawMessage // serialized plugin.TrackedProgress to write as most_recent_progress
}

// ForceCancelResult reports the outcome of a ForceCancelResourceUpdates call.
type ForceCancelResult struct {
	// CanceledInProgress are rows whose prior state was InProgress and are now Canceled.
	CanceledInProgress []ResourceUpdateRef
	// CanceledNotStarted are rows whose prior state was NotStarted and are now Canceled.
	CanceledNotStarted []ResourceUpdateRef
	// Skipped are rows that were already in a terminal state (CAS no-op).
	Skipped []ResourceUpdateRef
}

// ExpiredStackInfo contains information about a stack whose TTL policy has expired.
type ExpiredStackInfo struct {
	StackLabel   string
	StackID      string
	OnDependents string // "abort" or "cascade"
}

// StackReconcileInfo contains information about a stack with an auto-reconcile policy.
type StackReconcileInfo struct {
	StackLabel      string
	StackID         string
	IntervalSeconds int64
	LastReconcileAt time.Time
}

// PersistTargetReapRequest carries everything the reap transaction needs to
// evaluate the conditional-transition CAS and write the audit row. The
// per-target reap thresholds (reap_kind, reap_max_unreachable_seconds) are NOT
// in the request: they are re-read from the target's own persisted row inside
// the transaction so a stale caller cannot force a reap against a behaviour the
// target no longer carries.
type PersistTargetReapRequest struct {
	// Label identifies the target to reap.
	Label string
	// IncarnationID is the incarnation the caller observed. The CAS requires the
	// target's current row to still carry this incarnation, so a stale reaper
	// (target reaped then recovered to a fresh incarnation) reaps nothing.
	IncarnationID string
	// LastSeenBefore is the grace cutoff for last_seen_at: the CAS requires
	// last_seen_at <= this instant.
	LastSeenBefore time.Time
	// LastSampleBefore is the grace cutoff for last_sample_at: the CAS requires
	// last_sample_at <= this instant.
	LastSampleBefore time.Time
	// ReapedAt is the reap instant recorded in the audit row.
	ReapedAt time.Time
}

// ResourceSnapshot contains resource state at a point in time.
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
	// StoreResource persists a resource after successful creation/update in the
	// cloud. The optional expectedIncarnation is the target incarnation the
	// caller believes is current; when supplied (non-empty) the write is
	// rejected (ErrResourceWriteRejected) if the resource's current row was
	// written under a different incarnation, closing the reaped-then-recovered
	// stale-write race. A write to a resource whose current row is a reaped
	// tombstone is always rejected regardless of the incarnation argument.
	StoreResource(resource *pkgmodel.Resource, commandID string, expectedIncarnation ...string) (string, error)
	// DeleteResource removes a resource record after successful deletion in the cloud
	DeleteResource(resource *pkgmodel.Resource, commandID string) (string, error)
	// LoadResource retrieves a resource by its formae URI
	LoadResource(uri pkgmodel.FormaeURI) (*pkgmodel.Resource, error)
	// LoadResourceByNativeID finds a resource by its cloud provider native ID
	LoadResourceByNativeID(nativeID string, resourceType string) (*pkgmodel.Resource, error)
	// LoadAllResources returns all stored resources
	LoadAllResources() ([]*pkgmodel.Resource, error)
	// LoadReapedResources returns the current-version rows tombstoned with the
	// 'reaped' marker (PersistTargetReap), across all targets. Reaped rows are
	// excluded from every other resource query; this is the one path back to
	// them, used by destroy-of-reaped cleanup and the dangling-reference report.
	LoadReapedResources() ([]*pkgmodel.Resource, error)
	// LatestLabelForResource returns the most recent label variant for a resource
	LatestLabelForResource(label string) (string, error)
	// LoadResourceById retrieves a resource by its KSUID
	LoadResourceById(ksuid string) (*pkgmodel.Resource, error)
	// FindResourcesDependingOn returns all resources that reference the given resource via $ref
	FindResourcesDependingOn(ksuid string) ([]*pkgmodel.Resource, error)
	// FindResourcesDependingOnMany returns all resources that reference any of the given resources via $ref.
	// Returns a map from referenced KSUID to the resources that depend on it.
	FindResourcesDependingOnMany(ksuids []string) (map[string][]*pkgmodel.Resource, error)
	// FindTargetsDependingOnMany returns all targets whose config references any of the given resources via $ref.
	// Returns a map from source KSUID to the list of dependent targets.
	FindTargetsDependingOnMany(ksuids []string) (map[string][]*pkgmodel.Target, error)

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
	// UpdateTargetHealth applies an in-place health observation to the target's current
	// (max-version) row. Returns applied=true when exactly one row was updated. A guard
	// rejection (reaped state, stale observedAt, or incarnation mismatch) returns
	// applied=false with no error.
	UpdateTargetHealth(obs pkgmodel.TargetHealthObservation) (applied bool, err error)
	// AdvanceTargetAccrual applies an in-place unreachability-accrual update to a
	// target's current (max-version) row: adds deltaSeconds to
	// unreachable_accum_seconds and sets last_sample_at to lastSampleAt. Guarded by
	// incarnation match, current max-version pinning, and health_state == 'unreachable'
	// (mirrors UpdateTargetHealth's guard shape). Returns applied=false with no error
	// when the guard rejects the write (incarnation mismatch, or the target is no
	// longer the max-version/unreachable row it was when the caller read it).
	AdvanceTargetAccrual(targetLabel, incarnationID string, lastSampleAt time.Time, deltaSeconds int64) (applied bool, err error)
	// GetUnreachableTargets returns all current (max-version) targets whose
	// health_state is 'unreachable', with Health fully populated. Used by the
	// TargetReaper to compute per-tick accrual and detect reap candidates.
	GetUnreachableTargets() ([]*pkgmodel.Target, error)
	// PersistTargetReap performs the whole target reap in one transaction:
	//  1. a conditional transition (the atomic CAS, no locks) that flips the
	//     target's current row from 'unreachable' to 'reaped' only when it still
	//     matches the request's incarnation and the target's OWN persisted
	//     reap_kind='after', accrued unreachable time >= its threshold, and the
	//     grace cutoffs hold. If rows-affected != 1 nothing is reaped;
	//  2. an assertion that no incomplete forma_command touches the target's
	//     label (across all stacks); if any does, nothing is reaped;
	//  3. tombstoning every current-row resource on that target with the reaped
	//     marker;
	//  4. inserting a UNIQUE audit row.
	// Returns reaped=true only when the reap committed; a rejected CAS or a
	// failed assertion rolls back and returns reaped=false with no error.
	// Idempotent: a second call for an already-reaped incarnation reaps nothing.
	PersistTargetReap(req PersistTargetReapRequest) (reaped bool, err error)
	// CheckTargetsReaped inspects the current (max-version) row of each target in
	// labels and returns the subset whose health_state is 'reaped'. Labels with no
	// target row, or whose current row is any other health state, are omitted.
	// Used by command admission to reject an apply that touches a reaped target
	// without re-declaring it (which would otherwise resurrect it out of band).
	CheckTargetsReaped(labels []string) ([]string, error)

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
	// StackHasActiveCommands returns true if the stack has any forma commands
	// that are not in a terminal state (Success, Failed, Canceled)
	StackHasActiveCommands(stackLabel string) (bool, error)

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

	// UpdateFormaCommandTargetUpdates updates the target_updates JSON blob and command metadata
	// (state, modified_ts) without re-writing ResourceUpdates. Used by markTargetUpdateAsComplete
	// to persist target state changes that would otherwise only live in the in-memory cache.
	UpdateFormaCommandTargetUpdates(commandID string, targetUpdatesJSON json.RawMessage, state forma_command.CommandState, modifiedTs time.Time) error

	// ForceCancelResourceUpdates CAS-terminalizes in-flight resource updates to Canceled in one
	// transaction. For rows whose prior state was InProgress it also writes the provided progress
	// JSON (force-cancel marker) and most_recent_progress. Returns the rows actually transitioned
	// (split by prior state) and the intended rows that were already terminal (Skipped). Idempotent:
	// a retry affects zero rows and returns the same Skipped set.
	ForceCancelResourceUpdates(commandID string, inProgress []ForceCancelRow, notStarted []ResourceUpdateRef, modifiedTs time.Time) (ForceCancelResult, error)
}
