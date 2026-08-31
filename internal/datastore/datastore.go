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

// MaxFormaCommandsQueryLimit is the hard ceiling on how many forma commands a
// single status/list query may request, regardless of the caller-supplied
// page size. The API server clamps to this value before querying the
// datastore.
const MaxFormaCommandsQueryLimit = 200

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
	CommandID   *QueryItem[string]
	ClientID    *QueryItem[string]
	Command     *QueryItem[string]
	Status      *QueryItem[string]
	Stack       *QueryItem[string]
	Subject     *QueryItem[string]
	SubjectName *QueryItem[string]
	// Source restricts results to a FormaCommand source (forma_command.Source,
	// e.g. "user"). Set by ListFormaCommandStatus, never parsed from a user
	// query string, so a caller cannot ask to see scheduler bookkeeping.
	Source *QueryItem[string]
	N      int
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
	Ksuid         string
	Properties    json.RawMessage // current (cloud) properties — update ops only
	OldProperties json.RawMessage // properties at last reconcile — update ops only
}

// ResourceUpdateRef identifies a specific ResourceUpdate by its key components.
// Used for batch operations on ResourceUpdates.
type ResourceUpdateRef struct {
	KSUID     string
	Operation types.OperationType
}

// ResourceVersion is one stored version of a resource — including superseded
// versions, not just the current one. It carries the version's identity
// (URI+Version) so a caller can rewrite that exact row in place. Used by the
// one-time secret backfill to scrub plaintext opaque values from resource
// history.
type ResourceVersion struct {
	URI      string
	Version  string
	Resource *pkgmodel.Resource
}

// AgentBoot is one recorded agent process start. Append-only; see
// Datastore.RecordAgentBoot.
type AgentBoot struct {
	BootID   string
	Version  string
	BootedAt time.Time
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
	// StackCreatedAt is the stack's first version's timestamp — the anchor a
	// relative TTL counts from.
	StackCreatedAt time.Time
	// ExpiresAt is the policy's absolute deadline in its stored form, empty for
	// a relative policy; TTLSeconds is the relative deadline, nil for an
	// absolute one. Both are reported so an expiry can be explained in the log
	// rather than only announced.
	ExpiresAt  string
	TTLSeconds *int64
}

// HasUnreadableDeadline reports whether this stack was reported expired on an
// absolute deadline that is not actually a readable instant.
//
// The expiry queries compare absolute deadlines as fixed-width strings, on
// purpose: a cast would let one corrupt row abort the whole scan and stop every
// stack from ever expiring. The cost is that a string comparison cannot tell a
// real calendar date from an impossible one — "2025-02-30T12:00:00Z" has the
// right shape and sorts like a date. Expiry destroys real resources, so a real
// parse gets the last word before anything is acted on.
func (e ExpiredStackInfo) HasUnreadableDeadline() bool {
	if e.ExpiresAt == "" {
		return false
	}
	_, err := pkgmodel.CanonicalizeExpiresAt(e.ExpiresAt)
	return err != nil
}

// Deadline renders the instant this stack was due to be destroyed, for logging.
func (e ExpiredStackInfo) Deadline() string {
	if e.ExpiresAt != "" {
		return e.ExpiresAt
	}
	if e.TTLSeconds == nil {
		return "unknown"
	}
	return e.StackCreatedAt.Add(time.Duration(*e.TTLSeconds) * time.Second).UTC().Format(pkgmodel.ExpiresAtLayout)
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

// GeneratorIdentity is controller state for one generator: its stable KSUID
// and the generation it currently holds. Deliberately kept off
// pkgmodel.Generator so it can never participate in desired-config equality.
//
// GenerationSpec's bytes are NOT canonical: Postgres and Aurora store it as
// JSONB, which normalizes key order and whitespace on write, so what comes
// back can differ byte-for-byte from what AdvanceGeneration was given.
// Parse it; never byte-compare or hash it against the spec that was drawn.
//
// Aliased to pkgmodel.GeneratorIdentity (not a local struct) so that
// resource_update.ResourceDataLookup — which must not import
// internal/datastore, since internal/datastore imports resource_update for
// ResourceUpdate — can still declare a GetGeneratorIdentity method returning
// this exact type, and any Datastore implementation satisfies both
// interfaces with the same method.
type GeneratorIdentity = pkgmodel.GeneratorIdentity

// GeneratorRotationInfo is one rotating generator's cadence and the instant
// its last rotation committed.
//
// LastRotationAt is DERIVED at query time and stored nowhere: it is the start
// of the most recent command that both advanced this generator's generation
// and succeeded. Keeping it off the generator is the same choice policies
// make with LastReconcileAt — a stored last-rotated-at would participate in
// desired-config equality, show up as metadata drift, and be rendered into
// formae people copy between environments.
//
// Zero means no rotation has ever committed for this generator, which makes it
// due immediately. A draw whose command failed leaves the zero value in place:
// the generation row exists, but the command that would have propagated it
// does not read Success, so it advances no cadence.
type GeneratorRotationInfo struct {
	GeneratorID     string
	Label           string
	StackLabel      string
	IntervalSeconds int
	LastRotationAt  time.Time
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
	// LoadFormaCommandIDs returns the IDs of all stored FormaCommands, ordered by
	// command_id. Cheap (IDs only) so a caller can page through commands one at a
	// time via GetFormaCommandByCommandID instead of materializing every command
	// and all its resource updates at once — used by the secret backfill to bound
	// memory on large datasets.
	LoadFormaCommandIDs() ([]string, error)
	// LoadIncompleteFormaCommands returns FormaCommands that haven't reached a terminal state
	LoadIncompleteFormaCommands() ([]*forma_command.FormaCommand, error)
	// DeleteFormaCommand removes a FormaCommand and its associated ResourceUpdates
	DeleteFormaCommand(fa *forma_command.FormaCommand, commandID string) error
	// GetFormaCommandByCommandID retrieves a single FormaCommand by its ID
	GetFormaCommandByCommandID(commandID string) (*forma_command.FormaCommand, error)
	// GetMostRecentFormaCommandByClientID returns the most recent user command
	// for a given client, skipping any scheduler bookkeeping (sync,
	// discovery, auto-reconcile, stack expiry) that ran more recently.
	// A client with no such command yields (nil, nil): having run nothing is
	// an empty answer, not a failure, and callers render it as "no commands".
	GetMostRecentFormaCommandByClientID(clientID string) (*forma_command.FormaCommand, error)
	// GetResourceModificationsSinceLastReconcile returns resources modified since the last reconcile
	GetResourceModificationsSinceLastReconcile(stack string) ([]ResourceModification, error)

	// GetPropertiesAtLastWrite returns the resource's Properties as recorded
	// by the most recent version persisted under an apply command: the state
	// formae's own last write observed (the create/update echo). Sync and
	// discovery never advance it. Returns nil when formae never wrote the
	// resource.
	GetPropertiesAtLastWrite(ksuid string) (json.RawMessage, error)
	// QueryFormaCommands searches commands based on filter criteria
	QueryFormaCommands(query *StatusQuery) ([]*forma_command.FormaCommand, error)

	// Resource operations - these represent actual cloud state

	// QueryResources searches resources based on filter criteria
	QueryResources(query *ResourceQuery) ([]*pkgmodel.Resource, error)
	// ListResourceSummaries returns a lightweight projection of resources — only
	// the top-level indexed columns (label, stack, type, native_id, ksuid) — for
	// the same set of rows that QueryResources would return for the given query.
	// No data JSONB blob is read or unmarshaled.
	ListResourceSummaries(q *ResourceQuery) ([]pkgmodel.ResourceSummary, error)
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
	// LoadAllResourceVersions returns every stored version of every resource,
	// including superseded ones (unlike LoadAllResources, which returns only the
	// latest version per URI). Used by the one-time secret backfill to scrub
	// plaintext opaque values from resource history.
	LoadAllResourceVersions() ([]ResourceVersion, error)
	// LoadResourceVersionsPage returns a bounded page of resource versions whose
	// (uri, version) keyset is strictly after (afterURI, afterVersion), ordered by
	// (uri, version), at most limit rows. Pass "", "" to start. Lets the secret
	// backfill scrub history without loading every version into memory at once;
	// callers page until a short (< limit) page is returned. Rewriting a version's
	// data in place (UpdateResourceVersionData) does not move its keyset position,
	// so paging stays stable across the scrub.
	LoadResourceVersionsPage(afterURI string, afterVersion string, limit int) ([]ResourceVersion, error)
	// UpdateResourceVersionData overwrites the persisted data of a single
	// resource version in place (keyed by uri+version), without appending a new
	// version. Used by the secret backfill to rewrite a superseded version's
	// payload with hashed values.
	UpdateResourceVersionData(uri string, version string, resource *pkgmodel.Resource) error
	// LoadReapedResources returns the current-version rows tombstoned with the
	// 'reaped' marker (PersistTargetReap), across all targets. Reaped rows are
	// excluded from every other resource query; this is the one path back to
	// them, used by destroy-of-reaped cleanup and the dangling-reference report.
	LoadReapedResources() ([]*pkgmodel.Resource, error)
	// LatestLabelForResource returns the most recent label variant for a resource
	LatestLabelForResource(label string) (string, error)
	// LoadResourceById retrieves a resource by its KSUID
	LoadResourceById(ksuid string) (*pkgmodel.Resource, error)
	// LoadLatestResourceByKsuid retrieves the true latest version of the resource
	// identified by ksuid, without filtering by operation. Returns nil, nil when the
	// ksuid's latest version is a delete or reaped tombstone, so callers receive
	// not-found semantics for deleted resources regardless of their prior history.
	LoadLatestResourceByKsuid(ksuid string) (*pkgmodel.Resource, error)
	// FindResourcesDependingOn returns all resources that reference the given
	// resource via $ref. It takes a resource KSUID and only a resource KSUID:
	// backends read $ref dependencies from different places (postgres and aurora
	// from the refs column, sqlite and mssql by scanning the document), and the
	// refs column records generator KSUIDs too, so the families agree on a
	// resource KSUID and would disagree on a generator one. Generators are
	// FindResourcesReferencingGenerator's question, not this one.
	FindResourcesDependingOn(ksuid string) ([]*pkgmodel.Resource, error)
	// FindResourcesDependingOnMany returns all resources that reference any of the given resources via $ref.
	// Returns a map from referenced KSUID to the resources that depend on it.
	// It carries FindResourcesDependingOn's resource-KSUID-only contract.
	FindResourcesDependingOnMany(ksuids []string) (map[string][]*pkgmodel.Resource, error)
	// FindResourcesReferencingGenerator returns all live resources that bind a
	// property to the given generator through a translated $gen envelope.
	// Superseded versions and deleted or reaped resources are excluded, so each
	// returned resource appears once at its current version. An unknown
	// generator KSUID yields an empty result, not an error, and a resource KSUID
	// reached through $ref names no generator and yields nothing.
	//
	// Every backend returns the same set by construction. Each one's SQL is an
	// index prefilter only, deliberately broader than the truth so that a
	// destination is never missed, and pkgmodel.BindsGenerator is the
	// authoritative test every candidate row must pass before it is returned.
	FindResourcesReferencingGenerator(generatorKsuid string) ([]*pkgmodel.Resource, error)
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
	// LoadStacksByLabels retrieves multiple stacks by their labels in a single query.
	// Only found stacks are returned; labels with no matching non-deleted stack row are
	// omitted from the result. The caller is responsible for synthesizing entries for
	// any labels not present in the returned slice.
	LoadStacksByLabels(labels []string) ([]*pkgmodel.Stack, error)
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
	// reapedStacks holds the distinct stack labels whose live resources this
	// reap tombstoned, so the caller can clean up any stack the reap empties; it
	// is nil/empty when nothing committed.
	PersistTargetReap(req PersistTargetReapRequest) (reaped bool, reapedStacks []string, err error)
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
	// GetInlinePoliciesForStack returns the non-deleted inline policies of a stack
	// (those whose stack_id is the stack). Unlike GetPoliciesForStack it leaves out
	// the standalone policies attached to the stack through the junction table. An
	// empty stack ID has no inline policies and returns none.
	GetInlinePoliciesForStack(stackID string) ([]pkgmodel.Policy, error)
	// GetStandalonePolicy retrieves a standalone policy by label (stack_id IS NULL)
	// Returns nil, nil if no policy is found
	GetStandalonePolicy(label string) (pkgmodel.Policy, error)
	// LoadStandalonePoliciesByLabels retrieves multiple standalone policies by their
	// labels in a single query. Only found, non-deleted policies are returned; labels
	// with no matching row are omitted from the result.
	LoadStandalonePoliciesByLabels(labels []string) ([]pkgmodel.Policy, error)
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
	// DeleteInlinePolicy soft-deletes the inline policies (stack_id set) on a stack
	// that carry the given label (returns version string). Deleting when no live
	// row matches is a no-op success that returns an empty version. When several
	// rows match, the version returned is that of the last tombstone written.
	DeleteInlinePolicy(stackID string, policyLabel string, commandID string) (string, error)
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

	// Generator operations - a generator produces a value (e.g. a random
	// password) that a secret will later reference. Unlike a policy, a
	// generator has no standalone form: it is always owned by exactly one
	// stack, so there is no stack_generators junction table and no
	// attach/detach.

	// CreateGenerator persists a new generator (returns version string)
	CreateGenerator(gen pkgmodel.Generator, commandID string) (string, error)
	// UpdateGenerator persists a new version of an existing generator, found
	// by label and stack (returns version string)
	UpdateGenerator(gen pkgmodel.Generator, commandID string) (string, error)
	// DeleteGenerator soft-deletes the generator with the given label on the
	// given stack (returns version string). A label with no live match is a
	// no-op success that returns an empty version.
	DeleteGenerator(label, stackLabel string) (string, error)
	// GetGenerator retrieves the current generator with the given label on
	// the given stack. Returns nil, nil if no live generator is found.
	GetGenerator(label, stackLabel string) (pkgmodel.Generator, error)
	// LoadGeneratorsByStack returns all non-deleted generators owned by a
	// stack.
	LoadGeneratorsByStack(stackLabel string) ([]pkgmodel.Generator, error)
	// GetGeneratorIdentity returns the identity of the live generator with
	// this label on this stack. A zero GeneratorIdentity and a nil error
	// mean no such generator, matching GetGenerator's absent-is-not-an-error
	// convention.
	GetGeneratorIdentity(label, stackLabel string) (GeneratorIdentity, error)
	// GetGeneratorIdentityByID returns the identity of the live generator
	// with this KSUID, whichever stack owns it. Zero value plus nil error
	// when absent.
	GetGeneratorIdentityByID(generatorID string) (GeneratorIdentity, error)
	// GetGeneratorsWithRotation returns every live generator that declares a
	// rotation cadence, with the instant its last rotation committed. Modeled
	// on GetStacksWithAutoReconcilePolicy: the cadence and the last run come
	// back together, and the caller decides what is due.
	//
	// A generator whose stack has been deleted, or whose own latest row is a
	// delete, is absent. So is one whose latest row no longer declares a
	// cadence, which is what makes removing rotation take effect on the next
	// sweep rather than at the next restart.
	GetGeneratorsWithRotation() ([]GeneratorRotationInfo, error)
	// AdvanceGeneration records that a new generation was drawn for this
	// generator, under this spec. Writes a new version row, preserving the
	// KSUID. Errors if generationID is empty, if drawnUnder is not valid
	// JSON (a generation always has a spec it was drawn under, and every
	// backend must agree on what counts as one), or if the generator has
	// been deleted — a tombstoned id is not resurrected.
	//
	// Called by the GeneratorUpdater, which records the generation before it
	// reports the drawn value: a value handed to a destination under a
	// generation nobody stored is exactly the state the next apply cannot
	// reason about.
	//
	// commandID is the command the draw belongs to. It is what makes the
	// rotation cadence derivable: a generation row alone says a value was
	// drawn, and the command it was drawn by says whether that value ever
	// reached its destinations. GetGeneratorsWithRotation joins the two, so
	// a draw whose command failed advances no cadence.
	AdvanceGeneration(generatorID, generationID, commandID string, drawnUnder json.RawMessage) error

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

	// UpdateResourceUpdateProgress updates a ResourceUpdate with progress information.
	// startTs is persisted so an in-progress status read (which loads straight from
	// the datastore, before finalization) reports the real start time.
	// resolvedRootDigests rides the progress write so provenance digests are
	// durable exactly when progress is; nil leaves the stored map unchanged.
	UpdateResourceUpdateProgress(commandID string, ksuid string, operation types.OperationType, state resource_update.ResourceUpdateState, startTs time.Time, modifiedTs time.Time, progress plugin.TrackedProgress, resolvedRootDigests map[string]string) error

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

	// RecordAgentBoot appends one row recording that this agent process started
	// and which build it is running. Append-only: rows are never updated or
	// deleted, so the sequence is the agent's start history.
	//
	// Nothing in the agent reads these rows back. They exist for an
	// out-of-process reader that needs the running version for an installation
	// which has not yet run any command, a question the command history cannot
	// answer because agent_version only exists on a command row.
	RecordAgentBoot(version string) error
}
