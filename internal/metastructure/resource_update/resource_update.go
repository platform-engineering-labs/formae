// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/patch"
	"github.com/platform-engineering-labs/formae/internal/metastructure/provenance"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Type aliases for backward compatibility within this package
type (
	FormaCommandSource  = types.FormaCommandSource
	OperationType       = types.OperationType
	ResourceUpdateState = types.ResourceUpdateState
)

// Re-export constants for backward compatibility
const (
	FormaCommandSourceUser                = types.FormaCommandSourceUser
	FormaCommandSourceSynchronize         = types.FormaCommandSourceSynchronize
	FormaCommandSourceDiscovery           = types.FormaCommandSourceDiscovery
	FormaCommandSourcePolicyAutoReconcile = types.FormaCommandSourcePolicyAutoReconcile

	OperationCreate  = types.OperationCreate
	OperationUpdate  = types.OperationUpdate
	OperationDelete  = types.OperationDelete
	OperationRead    = types.OperationRead
	OperationReaped  = types.OperationReaped
	OperationReplace = types.OperationReplace

	ResourceUpdateStateUnknown    = types.ResourceUpdateStateUnknown
	ResourceUpdateStateNotStarted = types.ResourceUpdateStateNotStarted
	ResourceUpdateStatePending    = types.ResourceUpdateStatePending
	ResourceUpdateStateInProgress = types.ResourceUpdateStateInProgress
	ResourceUpdateStateFailed     = types.ResourceUpdateStateFailed
	ResourceUpdateStateSuccess    = types.ResourceUpdateStateSuccess
	ResourceUpdateStateCanceled   = types.ResourceUpdateStateCanceled
	ResourceUpdateStateRejected   = types.ResourceUpdateStateRejected
)

// LateCreateOnlyChangeError reports that resolving a reference at execution
// time produced a diff on createOnly fields that plan-time classification did
// not declare. The update must fail rather than silently drop the diff or
// escalate to an undeclared replacement.
type LateCreateOnlyChangeError struct {
	ResourceLabel string
	Fields        []string
}

func (e LateCreateOnlyChangeError) Error() string {
	return fmt.Sprintf("resolving references at execution time changed createOnly fields %v on %s; a replacement was not planned, refusing to proceed", e.Fields, e.ResourceLabel)
}

// ResourceUpdate represents an update to a resource in the system. A ResourceUpdate is a logical operation
// that may involve multiple plugin operations. For example a replace operation will involve two plugin
// operations: a delete and a create.
type ResourceUpdate struct {
	DesiredState             pkgmodel.Resource        `json:"Resource"`
	ResourceTarget           pkgmodel.Target          `json:"ResourceTarget"`
	PriorState               pkgmodel.Resource        `json:"ExistingResource"`
	ExistingTarget           pkgmodel.Target          `json:"ExistingTarget"`
	Operation                OperationType            `json:"Operation"`
	State                    ResourceUpdateState      `json:"State"`
	StartTs                  time.Time                `json:"StartTs"`
	ModifiedTs               time.Time                `json:"ModifiedTs"`
	Retries                  uint16                   `json:"Retries"`
	Remaining                int16                    `json:"Remaining"`
	Version                  string                   `json:"Version"`
	MostRecentProgressResult plugin.TrackedProgress   `json:"MostRecentProgressResult"`
	ProgressResult           []plugin.TrackedProgress `json:"ProgressResult"`
	Source                   FormaCommandSource       `json:"Source,omitempty"`
	RemainingResolvables     []pkgmodel.FormaeURI     `json:"RemainingResolvables,omitempty"`
	StackLabel               string                   `json:"StackLabel,omitempty"`
	GroupID                  string                   `json:"GroupId,omitempty"`
	ReferenceLabels          map[string]string        `json:"ReferenceLabels,omitempty"`
	PreviousProperties       json.RawMessage          `json:"PreviousProperties,omitempty"`
	MatchFilters             []pkgmodel.MatchFilter   `json:"matchFilters,omitempty"`  // Declarative filters (any match = exclude)
	IsCascade                bool                     `json:"IsCascade,omitempty"`     // True if this delete is triggered by cascade
	CascadeSource            string                   `json:"CascadeSource,omitempty"` // Label of resource that triggered the cascade
	// CreateOnlyPatch is a JSON-patch document listing only the ops against
	// createOnly fields that triggered a resource replacement. Populated on
	// the delete half of a replace pair so the CLI can render which
	// immutable properties forced the replace. Never sent to resource
	// plugins — the replace executes as a plain destroy + create.
	CreateOnlyPatch json.RawMessage `json:"CreateOnlyPatch,omitempty"`
	// FailureReason carries a human-readable explanation for a failure that
	// is not recorded as plugin progress — notably a terminal resolve miss,
	// where the resource fails before any plugin operation runs and would
	// otherwise surface an empty ErrorMessage. MostRecentFailureMessage falls
	// back to this when no progress-based failure message is available.
	FailureReason string `json:"FailureReason,omitempty"`
	// ProvenanceRecords is the per-occurrence provenance state computed at
	// planning: identities, digests, and classes only, never values. It is
	// written once with the row and IMMUTABLE thereafter; execution-time
	// regeneration reads it back so suppression decisions survive recovery.
	ProvenanceRecords []OccurrenceRecord `json:"ProvenanceRecords,omitempty"`
	// ResolvedRootDigests maps a source URI to the canonical-domain digest of
	// the pre-extraction value its reference resolved to at execution time.
	// Populated as resolutions arrive and made durable through the progress
	// write, so the write-origin merge can stamp $resolvedFrom even when
	// recovery resumes persisted progress without re-resolving. A missing
	// entry degrades to stamping nothing (provenance stays unknown), never to
	// attesting a recomputed value.
	ResolvedRootDigests map[string]string `json:"ResolvedRootDigests,omitempty"`
}

func (ru *ResourceUpdate) URI() pkgmodel.FormaeURI {
	return ru.DesiredState.URI()
}

func (ru *ResourceUpdate) HasResolvables() bool {
	return len(ru.RemainingResolvables) > 0
}

func (ru *ResourceUpdate) ListResolvables() []pkgmodel.FormaeURI {
	return ru.RemainingResolvables
}

// ResolveValue substitutes a freshly-read property value into the
// DesiredState's $ref/$value structures and keeps the derived
// PatchDocument in sync. PatchDocument is a derived view of (PriorState,
// DesiredState, Schema) — whenever the executor mutates the state the
// patch is derived from, the patch must be re-derived so the eventual
// plugin call sees a diff that matches reality. ResolveValue and its $gen
// sibling ResolveGeneratorValue are the only apply-time mutators of
// DesiredState.Properties, and both route the regen through
// reDerivePatchAfterSubstitution.
//
// mode is the command's configured apply mode (reconcile vs patch), the
// same mode planning used to derive the original patch — regeneration must
// use identical semantics or a reconcile-planned removal can silently
// vanish when a resolvable resolves at execution time.
func (ru *ResourceUpdate) ResolveValue(formaeUri pkgmodel.FormaeURI, value string, mode pkgmodel.FormaApplyMode) error {
	properties, err := resolver.ResolvePropertyReferences(formaeUri, ru.DesiredState.Properties, value)
	if err != nil {
		slog.Error("Failed to resolve dynamic properties", "error", err)
		return fmt.Errorf("failed to resolve dynamic properties: %w", err)
	}
	ru.DesiredState.Properties = properties

	return ru.reDerivePatchAfterSubstitution(mode, string(formaeUri))
}

// ResolveGeneratorValue delivers a generator's freshly drawn value to every
// destination in this update that still needs it, and keeps the derived
// PatchDocument in sync. It is the $gen sibling of ResolveValue, and together
// with it they are the only apply-time mutators of DesiredState.Properties,
// so between them they own the patch regeneration.
//
// The value is written INSIDE each $gen envelope, as its $value, never in
// place of the envelope: see resolver.SetGenValues for why that distinction
// is what keeps the credential hashed at rest.
//
// A destination whose occurrence classified stable is skipped. The changeset
// already declines to wire such a destination to the draw, but the two
// decisions are not the same one: a single resource may hold both a stable
// and an unstable destination for the same generator, and one edge covers the
// whole resource. Re-reading the classification here is what stops the fresh
// draw landing on the stable destination too and rotating a credential
// nothing asked to rotate.
//
// generationID is the generation the value was drawn under, and every
// destination that receives the value is stamped with its digest: the same
// digest the planner computes for that generation, so the next apply can
// prove the destination did not move and suppress its op. Without the stamp
// the occurrence classifies as unknown movement on every subsequent apply,
// which plans, redraws, and silently rotates the credential.
//
// mode is the command's configured apply mode, threaded through for exactly
// the reason ResolveValue documents: regeneration must use the semantics
// planning used or a reconcile-planned removal silently vanishes.
func (ru *ResourceUpdate) ResolveGeneratorValue(generatorKsuid string, value string, generationID string, mode pkgmodel.FormaApplyMode) error {
	var paths []string
	var outputs []string
	for _, occurrence := range pkgmodel.FindGenObjectsFromProperties(ru.DesiredState.Properties) {
		if occurrence.Generator != generatorKsuid {
			continue
		}
		if IsGenDestinationStable(ru.ProvenanceRecords, occurrence.Path) {
			continue
		}
		paths = append(paths, occurrence.Path)
		outputs = append(outputs, occurrence.Output)
	}
	if len(paths) == 0 {
		return nil
	}

	properties, err := resolver.SetGenValues(ru.DesiredState.Properties, generatorKsuid, paths, value)
	if err != nil {
		// The error names paths only; the drawn value is never in it.
		slog.Error("Failed to deliver a generated value", "error", err)
		return fmt.Errorf("failed to deliver a generated value: %w", err)
	}
	ru.DesiredState.Properties = properties
	ru.stampDrawnGeneration(generatorKsuid, outputs, generationID)

	return ru.reDerivePatchAfterSubstitution(mode, "generator "+generatorKsuid)
}

// stampDrawnGeneration records, for every generator output this update just
// received a value for, the digest of the generation it was drawn under.
//
// The digest is provenance.DigestOfString over the generation's identity, and
// it must stay byte-identical to what the planner computes for the same
// generation (resolver's generationRootDigest): the occurrence classifier
// compares the two directly, and a digest that differs by so much as a
// wrapper would never match, so every re-apply would re-plan and redraw with
// nothing anywhere reporting a fault.
//
// The carrier holds digests only and is persisted verbatim, so the generation
// identity itself never goes in. The map is written before the plugin call,
// so the write-origin merge of the echo finds it and stamps $resolvedFrom
// into the envelope that lands at rest.
//
// generationID is the delivery boundary's to guarantee non-empty
// (ExecutionDAG.propagateDrawnGeneratorValue refuses a draw naming none).
// Re-checking it here would stamp nothing and carry on, which is the silent
// outcome that guard exists to make loud.
func (ru *ResourceUpdate) stampDrawnGeneration(generatorKsuid string, outputs []string, generationID string) {
	digest := provenance.DigestOfString(generationID)
	for _, output := range outputs {
		key := generatorSourceKey(generatorKsuid, output)
		if key == "" {
			continue
		}
		if ru.ResolvedRootDigests == nil {
			ru.ResolvedRootDigests = make(map[string]string)
		}
		ru.ResolvedRootDigests[key] = digest
	}
}

// reDerivePatchAfterSubstitution re-derives PatchDocument after an apply-time
// mutation of DesiredState.Properties. subject names what was substituted and
// appears in error messages only — it must never carry a resolved value.
//
// Only Updates need a fresh patch — Create/Delete/Replace carry full
// desired/prior state to the provider rather than a diff. Patch regen is also
// a no-op when no Schema is available (sync/discovery paths).
func (ru *ResourceUpdate) reDerivePatchAfterSubstitution(mode pkgmodel.FormaApplyMode, subject string) error {
	if ru.Operation != OperationUpdate || len(ru.DesiredState.Schema.Fields) == 0 {
		return nil
	}

	patchDoc, createOnlyPatch, derr := ru.regeneratePatchDocument(mode)
	if derr != nil {
		return fmt.Errorf("failed to re-derive patch document after resolving %s: %w", subject, derr)
	}
	if len(createOnlyPatch) > 0 {
		fields, ferr := createOnlyPatchFields(createOnlyPatch)
		if ferr != nil {
			return fmt.Errorf("failed to inspect createOnly patch after resolving %s: %w", subject, ferr)
		}
		return LateCreateOnlyChangeError{ResourceLabel: ru.DesiredState.Label, Fields: fields}
	}
	ru.DesiredState.PatchDocument = patchDoc

	return nil
}

// regeneratePatchDocument re-derives PatchDocument from (PriorState,
// DesiredState, Schema) through the SAME opaque-suppression + plugin-format
// conversion path that NewResourceUpdateForExisting uses to build the initial
// patch (resource_update_factory.go). Without routing through
// SuppressUnchangedOpaqueValues here too, an apply-time resolvable
// substitution (e.g. a dependent resource picking up a just-created sibling's
// native ID) would regenerate the patch straight from PriorState/DesiredState's
// raw properties — an UNCHANGED opaque field could resurface as a spurious
// patch op, and resource_updater.go's update() forwards PatchDocument to the
// plugin unconverted, so any hash material in it would reach the plugin
// unguarded.
//
// mode must match the apply mode the command was planned under (reconcile ->
// ExactMatch, patch -> EnsureExists) — see patch.GeneratePatch — so a
// reconcile-planned removal is not silently dropped by regenerating under
// patch semantics. Returns (patch, createOnlyPatch, err); the caller decides
// what to do with a createOnly diff surfaced this late.
func (ru *ResourceUpdate) regeneratePatchDocument(mode pkgmodel.FormaApplyMode) (json.RawMessage, json.RawMessage, error) {
	existingForPatch, desiredForPatch, err := SuppressUnchangedOpaqueValues(
		ru.PriorState.Properties, ru.DesiredState.Properties, ru.DesiredState.Schema, ru.DesiredState.Type)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to suppress unchanged opaque values: %w", err)
	}

	// Read-safe/comparison conversion: existingPluginProps is only used as the
	// "before" side of this local diff, never transmitted to a plugin. A
	// genuinely-rotated opaque field's existing side is a stored hash that can
	// never be un-hashed back to plaintext — that must not block patch
	// generation.
	existingPluginProps, err := resolver.ConvertExistingStateForComparison(existingForPatch)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to convert existing properties to plugin format: %w", err)
	}

	// Guarded conversion: desiredForPatch is the "after" side and, once an
	// unchanged opaque field has been suppressed above, must never carry a
	// stored hash — a genuinely hashed leftover here means something upstream
	// is broken, and that must fail loudly rather than silently reach a plugin.
	newPluginProps, err := resolver.ConvertToPluginFormat(desiredForPatch)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to convert desired properties to plugin format: %w", err)
	}

	// The onlyForceResent decision is deliberately ignored here: this call
	// regenerates the payload for an update that is already happening, so a
	// requiredOnUpdate field's force-resent op must ride along in patchDoc as
	// returned — dropping it would send the plugin an Update with the
	// provider-mandated field missing. Only planning (whether to create this
	// ResourceUpdate at all) may treat a force-resent-only patch as empty; see
	// resource_update_factory.go.
	// Provably-stable occurrences stay suppressed through every regeneration:
	// the classification was decided at planning and persisted immutably on
	// this row, so recovery reproduces the same suppression without
	// recomputing anything from inputs that no longer exist.
	regenProperties := resolver.NewResolvableProperties()
	for _, rec := range ru.ProvenanceRecords {
		if rec.Class == OccurrenceStable {
			regenProperties.SuppressStableAt(rec.DestinationPath)
		}
	}

	patchDoc, createOnlyPatch, _, err := patch.GeneratePatch(
		existingPluginProps,
		newPluginProps,
		existingForPatch,
		desiredForPatch,
		regenProperties,
		ru.DesiredState.Schema,
		mode,
	)
	if err != nil {
		return nil, nil, err
	}

	return patchDoc, createOnlyPatch, nil
}

// ConvergenceOnly reports whether every op in this update's patch document
// targets a reference occurrence the provenance classification requires to
// converge: same source identity as what was last written, a written value on
// record, and movement that is real or unknown. Such an update propagates a
// source change the stored state has already absorbed; it does not assert a
// desired state that differs from the current one, so drift-absorption checks
// may treat the resource as unmodified by the user. A repoint (identity
// change), a first declaration, or any op outside the classified occurrences
// keeps the update a real change.
func (ru *ResourceUpdate) ConvergenceOnly() bool {
	if ru.Operation != OperationUpdate || len(ru.ProvenanceRecords) == 0 {
		return false
	}
	converging := map[string]bool{}
	for _, rec := range ru.ProvenanceRecords {
		if rec.Class != OccurrenceStable && rec.HasStoredWritten && rec.DesiredIdentity == rec.StoredIdentity {
			converging["/"+strings.ReplaceAll(rec.DestinationPath, ".", "/")] = true
		}
	}
	if len(converging) == 0 {
		return false
	}
	var ops []struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(ru.DesiredState.PatchDocument, &ops); err != nil || len(ops) == 0 {
		return false
	}
	for _, op := range ops {
		if !converging[op.Path] {
			return false
		}
	}
	return true
}

// createOnlyPatchFields extracts the distinct top-level field names touched
// by a createOnly patch document, in first-seen order, so
// LateCreateOnlyChangeError can name what changed without exposing the raw
// JSON-patch op shape to callers.
func createOnlyPatchFields(createOnlyPatch json.RawMessage) ([]string, error) {
	var ops []struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(createOnlyPatch, &ops); err != nil {
		return nil, fmt.Errorf("failed to parse createOnly patch ops: %w", err)
	}

	seen := make(map[string]struct{}, len(ops))
	var fields []string
	for _, op := range ops {
		field, _, _ := strings.Cut(strings.TrimPrefix(op.Path, "/"), "/")
		if field == "" {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		fields = append(fields, field)
	}
	return fields, nil
}

func (ru *ResourceUpdate) RequiresDelete() bool {
	return ru.Operation == OperationDelete || ru.Operation == OperationReplace
}

func (ru *ResourceUpdate) IsCreate() bool {
	return ru.Operation == OperationCreate || ru.Operation == OperationReplace
}

func (ru *ResourceUpdate) IsUpdate() bool {
	return ru.Operation == OperationUpdate
}

func (ru *ResourceUpdate) IsSync() bool {
	return ru.Operation == OperationRead
}

func (ru *ResourceUpdate) IsDelete() bool {
	return ru.Operation == OperationDelete || ru.Operation == OperationReplace
}

func (ru *ResourceUpdate) HasProgress() bool {
	// The progress is considered if it's not a read
	for _, progress := range ru.ProgressResult {
		if progress.Operation != resource.OperationRead {
			return true
		}
	}

	return false
}

func (ru *ResourceUpdate) FindProgress(operation resource.Operation) (bool, *plugin.TrackedProgress) {
	for i := range ru.ProgressResult {
		if ru.ProgressResult[i].Operation == operation {
			return true, &ru.ProgressResult[i]
		}
	}
	return false, nil
}

func (ru *ResourceUpdate) RecordProgress(progress *plugin.TrackedProgress) error {
	tracked := *progress
	// Set StartTs if not already set (first progress for this resource update)
	if ru.StartTs.IsZero() {
		tracked.StartTs = util.TimeNow()
	}

	found := false
	for i, existingProgress := range ru.ProgressResult {
		if existingProgress.Operation == progress.Operation {
			// Preserve StartTs from existing progress
			tracked.StartTs = existingProgress.StartTs
			ru.ProgressResult[i] = tracked
			found = true
		}
	}
	if !found {
		ru.ProgressResult = append(ru.ProgressResult, tracked)
	}
	ru.MostRecentProgressResult = tracked

	return ru.updateResourceUpdateFromProgress(&progress.ProgressResult)
}

func (ru *ResourceUpdate) updateResourceUpdateFromProgress(progress *resource.ProgressResult) error {
	ru.UpdateState()
	slog.Debug("Updating resource state for " + string(ru.URI()) + " to " + string(ru.State))

	slog.Debug("Setting NativeID from progress", "nativeID", progress.NativeID, "uri", ru.URI())
	ru.DesiredState.NativeID = progress.NativeID
	now := util.TimeNow()
	if ru.StartTs.IsZero() {
		ru.StartTs = now
	}
	ru.ModifiedTs = now

	// Only update properties when the operation has finished successfully.
	// Intermediate progress updates may have empty/partial properties which would
	// wipe out $ref structures needed for dependency tracking.
	if !progress.FinishedSuccessfully() {
		return nil
	}

	// For Update operations with a Read sub-operation, update ExistingResource properties
	// instead of Resource properties to preserve user-provided values for metadata/legacy.
	if ru.Operation == OperationUpdate && progress.Operation == resource.OperationRead {
		err := ru.updateExistingResourceProperties(string(progress.ResourceProperties))
		if err != nil {
			slog.Error("Failed to update resource properties", "error", err)
			return err
		}
	} else {
		// The echo of our own write is a Create or Update progress; every
		// other Read-shaped merge (sync, discovery) is read-origin.
		writeOrigin := progress.Operation == resource.OperationCreate || progress.Operation == resource.OperationUpdate
		err := ru.updateResourceProperties(string(progress.ResourceProperties), writeOrigin)
		if err != nil {
			slog.Error("Failed to update resource properties", "error", err)
			return err
		}
	}

	return nil
}

func (ru *ResourceUpdate) Reject() {
	ru.State = ResourceUpdateStateRejected
	ru.ModifiedTs = util.TimeNow()
}

func (ru *ResourceUpdate) MarkAsSuccess() {
	ru.State = ResourceUpdateStateSuccess
	ru.ModifiedTs = util.TimeNow()
	// A reason left over from an earlier attempt would otherwise surface as the
	// ErrorMessage of an update that succeeded.
	ru.FailureReason = ""
}

func (ru *ResourceUpdate) MarkAsFailed() {
	ru.State = ResourceUpdateStateFailed
	ru.ModifiedTs = util.TimeNow()
}

func (ru *ResourceUpdate) MostRecentFailureMessage() string {
	// Account for non-recoverable errors or max attempts reached
	if msg := ru.FilterProgressMessage(func(p plugin.TrackedProgress) bool {
		return p.Failed() && p.StatusMessage != ""
	}); msg != "" {
		return msg
	}

	// Account for recoverable errors
	if msg := ru.FilterProgressMessage(func(p plugin.TrackedProgress) bool {
		return p.OperationStatus == resource.OperationStatusFailure && p.StatusMessage != ""
	}); msg != "" {
		return msg
	}

	// Fall back to a failure that was not recorded as plugin progress (e.g. a
	// terminal resolve miss, which fails before any plugin operation runs).
	return ru.FailureReason
}

func (ru *ResourceUpdate) MostRecentStatusMessage() string {
	// Find most recent non-empty status message
	for i := len(ru.ProgressResult) - 1; i >= 0; i-- {
		if ru.ProgressResult[i].StatusMessage != "" {
			return ru.ProgressResult[i].StatusMessage
		}
	}
	return ""
}

func (ru *ResourceUpdate) FilterProgressMessage(filter func(plugin.TrackedProgress) bool) string {
	for _, progress := range ru.ProgressResult {
		if filter(progress) {
			return progress.StatusMessage
		}
	}
	return ""
}

// UpdateState derives the ResourceUpdate state from its ProgressResult.
// This is used during restart recovery to restore the correct state based on
// the progress that was persisted before the restart.
func (ru *ResourceUpdate) UpdateState() {
	if len(ru.ProgressResult) == 0 {
		ru.State = ResourceUpdateStateNotStarted
		return
	}

	// Check for failures first, regardless of how many operations are recorded.
	// A failed intermediate step (e.g. Read during an Update) must be detected
	// even when not all required operations have been attempted yet.
	for _, progress := range ru.ProgressResult {
		if progress.Failed() {
			ru.State = ResourceUpdateStateFailed
			return
		}
	}

	ops := ru.requiredOperations()
	if len(ru.ProgressResult) < len(ops) {
		ru.State = ResourceUpdateStateInProgress
		return
	}

	finalState := ResourceUpdateStateSuccess
	for _, progress := range ru.ProgressResult {
		if progress.OperationStatus != resource.OperationStatusSuccess {
			finalState = ResourceUpdateStateInProgress
		}
	}
	ru.State = finalState
}

func (ru *ResourceUpdate) requiredOperations() []resource.Operation {
	switch ru.Operation {
	case OperationRead:
		return []resource.Operation{resource.OperationRead}
	case OperationCreate:
		return []resource.Operation{resource.OperationCreate}
	case OperationDelete:
		return []resource.Operation{resource.OperationRead, resource.OperationDelete}
	case OperationUpdate:
		return []resource.Operation{resource.OperationRead, resource.OperationUpdate}
	case OperationReplace:
		return []resource.Operation{resource.OperationRead, resource.OperationDelete, resource.OperationCreate}
	default:
		slog.Error("Unknown operation type", "operation", ru.Operation)
		return nil
	}
}

func (ru *ResourceUpdate) updateResourceProperties(incomingProperties string, writeOrigin bool) error {
	// Only the write-origin DesiredState merge stamps provenance: the carrier
	// holds what THIS update resolved and wrote.
	var digests map[string]string
	if writeOrigin {
		digests = ru.ResolvedRootDigests
	}
	return ru.updateProperties(incomingProperties, &ru.DesiredState.Properties, &ru.DesiredState.ReadOnlyProperties, writeOrigin, digests)
}

func (ru *ResourceUpdate) updateExistingResourceProperties(incomingProperties string) error {
	// Always read-origin: this absorbs the pre-update out-of-band Read into
	// PriorState, never the echo of formae's own write — and never stamps
	// provenance.
	return ru.updateProperties(incomingProperties, &ru.PriorState.Properties, &ru.PriorState.ReadOnlyProperties, false, nil)
}

// updateProperties splits the properties from the plugin read result into regular and read-only,
// based on the resource schema fields, and merges $ref structures from the existing target properties.
// This preserves $ref structures needed for destroy dependency tracking and PKL extraction.
func (ru *ResourceUpdate) updateProperties(incomingProperties string, targetProperties, targetReadOnlyProperties *json.RawMessage, writeOrigin bool, provenanceDigests map[string]string) error {
	if incomingProperties == "" {
		slog.Debug("No properties to split for resource", "uri", ru.URI())
		incomingProperties = "{}"
	}
	var allProperties map[string]any
	if err := json.Unmarshal([]byte(incomingProperties), &allProperties); err != nil {
		slog.Error("Failed to unmarshal resource properties", "error", err)
		return err
	}

	// Build a set of schema fields for quick lookup
	fieldsSet := make(map[string]struct{})
	for _, field := range ru.DesiredState.Schema.Fields {
		fieldsSet[field] = struct{}{}
	}

	// Split properties into regular and read-only
	properties := make(map[string]any)
	readOnlyProperties := make(map[string]any)
	for k, v := range allProperties {
		if _, ok := fieldsSet[k]; ok {
			properties[k] = v
		} else {
			readOnlyProperties[k] = v
		}
	}

	// Marshal back to JSON
	propertiesJson, err := json.Marshal(properties)
	if err != nil {
		slog.Error("Failed to marshal regular properties", "error", err)
		return err
	}

	// Merge refs from user-provided properties to preserve $ref structures
	mergedProps, mergeErr := mergeRefsPreservingUserRefs(*targetProperties, propertiesJson, ru.DesiredState.Schema, writeOrigin, provenanceDigests)
	if mergeErr != nil {
		slog.Error("Failed to merge refs into properties", "error", mergeErr)
		return mergeErr
	}
	*targetProperties = mergedProps

	if len(readOnlyProperties) > 0 {
		if readOnlyPropertiesJson, err := json.Marshal(readOnlyProperties); err == nil {
			*targetReadOnlyProperties = readOnlyPropertiesJson
		} else {
			slog.Error("Failed to marshal read-only properties", "error", err)
			return err
		}
	} else {
		*targetReadOnlyProperties = nil
	}

	return nil
}

// mergeRefsPreservingUserRefs merges user-provided properties with plugin-returned properties.
// This function handles special "$ref" objects (resolvable references) by preserving the user's
// $ref structure while updating the $value from the plugin. The plugin properties serve as the
// base, with selective preservation of user values.
//
// The schema parameter provides field hints that control how arrays are matched:
// - Default/Set: elements are matched by value (JSON equality after flattening $refs)
// - Array: elements are matched by index position
// - EntitySet: elements are matched by a key field (e.g., Tags by "Key")
func mergeRefsPreservingUserRefs(userProperties, pluginProperties json.RawMessage, schema pkgmodel.Schema, writeOrigin bool, provenanceDigests map[string]string) (json.RawMessage, error) {
	if userProperties == nil {
		userProperties = []byte("{}")
	}
	if pluginProperties == nil {
		pluginProperties = []byte("{}")
	}

	userParsed := gjson.ParseBytes(userProperties)
	pluginParsed := gjson.ParseBytes(pluginProperties)

	// Start with plugin properties as base and merge in user values where appropriate
	result := string(pluginProperties)

	merger := &propertyMerger{
		userRoot:    userParsed,
		pluginRoot:  pluginParsed,
		result:      &result,
		schema:      schema,
		writeOrigin: writeOrigin,
		provenance:  provenanceDigests,
	}

	merger.mergeValue("", userParsed, pluginParsed)

	return json.RawMessage(result), nil
}

// changeset.Update interface implementation for ExecutionDAG integration

func (ru *ResourceUpdate) NodeURI() pkgmodel.FormaeURI { return ru.URI() }
func (ru *ResourceUpdate) Resolvables() []pkgmodel.FormaeURI {
	return ru.RemainingResolvables
}
func (ru *ResourceUpdate) Namespace() string   { return string(ru.DesiredState.Namespace()) }
func (ru *ResourceUpdate) IsRateLimited() bool { return true }
func (ru *ResourceUpdate) IsReady() bool       { return ru.State == ResourceUpdateStateNotStarted }
func (ru *ResourceUpdate) IsRunning() bool     { return ru.State == ResourceUpdateStateInProgress }
func (ru *ResourceUpdate) IsSuccess() bool     { return ru.State == ResourceUpdateStateSuccess }
func (ru *ResourceUpdate) IsFailed() bool {
	return ru.State == ResourceUpdateStateFailed || ru.State == ResourceUpdateStateRejected
}
func (ru *ResourceUpdate) MarkInProgress() { ru.State = ResourceUpdateStateInProgress }
func (ru *ResourceUpdate) MarkFailed()     { ru.State = ResourceUpdateStateFailed }

// propertyMerger handles the recursive merging of JSON properties
type propertyMerger struct {
	userRoot   gjson.Result
	pluginRoot gjson.Result
	result     *string
	schema     pkgmodel.Schema
	// writeOrigin is true when this merge absorbs the echo of formae's own
	// successful Create/Update (as opposed to a Read-shaped merge: sync,
	// discovery, or the pre-update out-of-band read). It gates whether
	// $ref/$res envelopes get an $applied provenance baseline stamped.
	writeOrigin bool
	// provenance maps a source URI to the canonical root digest this update
	// resolved from; set only for the write-origin DesiredState merge, which
	// stamps $resolvedFrom from it.
	provenance map[string]string
}

// mergeValue recursively merges a value at the given path
func (m *propertyMerger) mergeValue(path string, userVal, pluginVal gjson.Result) {
	if userVal.IsObject() {
		m.mergeObject(path, userVal, pluginVal)
	} else if userVal.IsArray() {
		m.mergeArray(path, userVal, pluginVal)
	} else {
		m.mergePrimitive(path, userVal)
	}
}

// mergeObject handles merging of object values
func (m *propertyMerger) mergeObject(path string, userVal, pluginVal gjson.Result) {
	// Check if this is a $ref object (resolvable reference)
	if userRef := userVal.Get("$ref"); userRef.Exists() {
		m.mergeRefObject(path, userVal, pluginVal)
		return
	}

	// Check if this is a $res object — a STRUCTURED resolvable reference in its
	// pre-resolution shape ({"$res":true,"$label":..,"$type":..,"$stack":..,
	// "$property":..[,"$value":..]}). This shape survives at rest on the
	// non-translating paths (Synchronize/Discovery/Destroy/seed) where a user
	// apply's $res->$ref rewrite never runs. Without this branch it would fall
	// through to the generic recursive merge below, which walks the envelope's
	// keys against the plugin's live value and OVERWRITES $value with plaintext —
	// and because a $res envelope that points at another resource's Opaque
	// property carries no schema-opaque field of its own, the persist transformer
	// would never hash it, leaking the resolved secret in CLEARTEXT at rest.
	// Handle it exactly like $ref: preserve the resolvable structure, refresh
	// $value, and (when inherited-Opaque) drop the stale $hashed marker so the
	// persist transformer re-hashes the field.
	if userVal.Get("$res").Bool() {
		m.mergeResObject(path, userVal, pluginVal)
		return
	}

	// Check if this is a $gen object — a generator reference, in its authored
	// or translated shape ({"$gen":true,"$generator":..,"$output":..,
	// "$visibility":"Opaque"[,"$value":..]}). Like $res it is a structured
	// resolvable reference, so it is handled the same way: preserve the
	// envelope, refresh $value from the plugin echo, and drop a stale
	// $hashed marker so the persist transformer re-hashes the field. $gen is
	// always opaque, so the $applied provenance baseline mergeResObject would
	// otherwise stamp is always skipped for it, exactly as it already is for
	// an opaque $res.
	//
	// Without this branch it would fall through to the generic recursive
	// merge below, which walks the envelope's own metadata keys against the
	// plugin's echo: a bare-scalar echo happens to be absorbed by the
	// opaque-scalar branch further down, but a structured echo (a plugin
	// that round-trips the resolvable, mirroring $res) leaves a stale
	// $hashed marker sitting next to the freshly adopted plaintext value —
	// the persist transformer's idempotency guard then skips re-hashing it,
	// persisting the generated secret in cleartext while claiming it is
	// hashed.
	if userVal.Get("$gen").Bool() {
		m.mergeResObject(path, userVal, pluginVal)
		return
	}

	// Check if this is a $embed object — preserve the user's envelope wholesale.
	// The plugin value is always the assembled result of the template; we never
	// let the plugin overwrite the user's $embed declaration.
	if userVal.Get("$embed").Bool() {
		cleanPath := m.cleanPath(path)
		*m.result, _ = sjson.SetRaw(*m.result, cleanPath, userVal.Raw)
		return
	}

	// Opaque-value envelope ({"$value":...,"$visibility":"Opaque"[,"$hashed":true]})
	// where the plugin actually returned something at this path AS A BARE SCALAR (e.g.
	// a secret store's GetSecretValue-equivalent never re-wraps it on read): recursing
	// field-by-field below would try to match the envelope's own keys
	// ($value/$visibility/$hashed) against that bare scalar (which has no sub-fields)
	// and silently preserve the OLD envelope verbatim — for a $hashed:true envelope,
	// the stored hash would never be replaced by the freshly-read plaintext, so the
	// plugin-boundary guard would permanently reject this field on every
	// subsequent use. Replace the envelope's $value with the plugin's live value
	// (dropping $hashed — it no longer holds a hash) and keep the visibility/strategy
	// metadata. When the plugin did NOT return this path at all (e.g. a Create response
	// with no ResourceProperties), fall through to the general recursive merge below,
	// which correctly preserves the user's envelope unchanged.
	if userVal.Get("$visibility").String() == pkgmodel.VisibilityOpaque && pluginVal.Exists() && !pluginVal.IsObject() {
		cleanPath := m.cleanPath(path)
		updated, _ := sjson.Set(userVal.Raw, "$value", pluginVal.Value())
		updated, _ = sjson.Delete(updated, "$hashed")
		*m.result, _ = sjson.SetRaw(*m.result, cleanPath, updated)
		return
	}

	// An empty user object writes no leaves, so the recursion below would
	// drop it from the merged document entirely. Under a preserveEmptyValues
	// root the empty object IS the value and must persist.
	if len(userVal.Map()) == 0 && m.underPreservedRoot(path) {
		cleanPath := m.cleanPath(path)
		*m.result, _ = sjson.SetRaw(*m.result, cleanPath, "{}")
		return
	}

	// Not a $ref or $embed object - recursively merge each field
	userVal.ForEach(func(key, val gjson.Result) bool {
		childPath := m.buildChildPath(path, key.String())
		pluginChildVal := pluginVal.Get(escapePathKey(key.String()))
		m.mergeValue(childPath, val, pluginChildVal)
		return true
	})
}

// underPreservedRoot reports whether a merge path's top-level field carries
// the preserveEmptyValues hint.
func (m *propertyMerger) underPreservedRoot(path string) bool {
	if path == "" {
		return false
	}
	root, _, _ := strings.Cut(path, ".")
	return patch.PreserveEmptyRootFields(m.schema)[root]
}

// mergeRefObject handles merging of $ref objects (resolvable references)
func (m *propertyMerger) mergeRefObject(path string, userVal, pluginVal gjson.Result) {
	cleanPath := m.cleanPath(path)

	// Determine which $value to use
	userValue := userVal.Get("$value")
	valueToSet := m.selectRefValue(userValue, pluginVal)

	// Preserve user's $ref structure and update the $value
	updatedRef, _ := sjson.Set(userVal.Raw, "$value", valueToSet)

	// A $ref may also be an Opaque envelope (a resolvable that resolves another
	// resource's opaque/secret field) carrying a $hashed:true marker. When we just
	// refreshed its $value from the plugin's live read, $value now holds plaintext,
	// not the stored digest — so the $hashed marker is stale. Drop it, mirroring the
	// bare-opaque-envelope branch in mergeObject, so the persist transformer re-hashes
	// the field at rest. Leaving $hashed:true would persist the cleartext secret while
	// claiming it is hashed (a plaintext-at-rest leak, and the transformer's
	// idempotency guard would skip it). Only drop it when the value came from the
	// plugin; if we preserved the user's stored hash (plugin returned nothing), the
	// marker is still correct.
	if userVal.Get("$visibility").String() == pkgmodel.VisibilityOpaque &&
		userVal.Get("$hashed").Bool() && !m.keptUserValue(userValue, pluginVal) {
		updatedRef, _ = sjson.Delete(updatedRef, "$hashed")
	}

	// Provenance baseline: on the echo-merge of formae's own successful
	// write, the envelope's pre-merge $value is the resolution that was
	// actually sent; keep it as $applied so later diffs can compare the
	// written domain against itself. Opaque envelopes are exempt: their
	// value is hashed at rest and has a dedicated suppression path.
	if userVal.Get("$visibility").String() != pkgmodel.VisibilityOpaque {
		if m.writeOrigin {
			if userValue.Exists() && userValue.Value() != nil {
				updatedRef, _ = sjson.Set(updatedRef, "$applied", userValue.Value())
			}
		} else if userVal.Get("$applied").Exists() &&
			!m.keptUserValue(userValue, pluginVal) &&
			!reflect.DeepEqual(valueToSet, userValue.Value()) {
			// The merger adopted a plugin echo that differs from the absorbed
			// one: out-of-band drift in the observed domain. Drop the baseline
			// so the next plan runs the corrective fresh-vs-echo diff.
			updatedRef, _ = sjson.Delete(updatedRef, "$applied")
		}
	}

	updatedRef = m.applyResolutionProvenance(updatedRef, userVal, userValue, pluginVal)

	*m.result, _ = sjson.SetRaw(*m.result, cleanPath, updatedRef)
}

// applyResolutionProvenance stamps or invalidates $resolvedFrom on a merged
// reference envelope. Stamping happens only on the write-origin merge, from
// the digest the resolution carried (root domain). Invalidation is
// domain-correct: a non-write merge that ADOPTED a differing plugin value is
// compared in the WRITTEN domain (digest the adopted unwrapped value against
// the envelope's stored written digest), so an enriching read of an UNCHANGED
// secret (plaintext echo vs stored hash) never invalidates, and empty/absent
// reads adopt nothing and never invalidate.
func (m *propertyMerger) applyResolutionProvenance(updatedRef string, userVal, userValue, pluginVal gjson.Result) string {
	if m.writeOrigin {
		if uri := referenceURIOf(userVal); uri != "" {
			if digest, ok := m.provenance[uri]; ok && provenance.Valid(digest) {
				updatedRef, _ = sjson.Set(updatedRef, "$resolvedFrom", digest)
			}
		}
		return updatedRef
	}
	if !userVal.Get("$resolvedFrom").Exists() {
		return updatedRef
	}
	if m.keptUserValue(userValue, pluginVal) {
		return updatedRef // nothing adopted; the witness stands
	}
	adopted := provenance.UnwrapEffectiveValue(pluginVal)
	if !adopted.Exists() || adopted.Type == gjson.Null {
		return updatedRef
	}
	var adoptedDigest string
	if adopted.Type == gjson.String {
		adoptedDigest = provenance.DigestOfString(adopted.String())
	} else {
		adoptedDigest = provenance.DigestOfJSON(adopted.Raw)
	}
	storedWritten := ""
	if userValue.Exists() {
		if userVal.Get("$hashed").Bool() {
			storedWritten = provenance.FromStored(userValue.String())
		} else if userValue.Type == gjson.String {
			storedWritten = provenance.DigestOfString(userValue.String())
		} else {
			storedWritten = provenance.DigestOfJSON(userValue.Raw)
		}
	}
	if storedWritten == "" || adoptedDigest != storedWritten {
		updatedRef, _ = sjson.Delete(updatedRef, "$resolvedFrom")
	}
	return updatedRef
}

// referenceURIOf returns the envelope's source URI in the carrier's key form,
// or "" for a shape without one.
//
// Two shapes have one: a translated $ref, whose key IS its $ref string, and a
// translated $gen, whose key is built from the generator it names and the
// output it draws (generatorSourceKey). Everything else (an untranslated
// $res, an untranslated $gen) names no source this update resolved against
// and is never stamped.
func referenceURIOf(envelope gjson.Result) string {
	if ref := envelope.Get("$ref"); ref.Exists() {
		return ref.String()
	}
	if envelope.Get("$gen").Bool() {
		return generatorSourceKey(pkgmodel.GenGeneratorKSUID(envelope), envelope.Get("$output").String())
	}
	return ""
}

// generatorSourceKey renders the ResolvedRootDigests key for one generator
// output: "generator://<generator ksuid>#/<output>".
//
// A generator KSUID and a resource KSUID come from the same minter but name
// rows in different tables, so keying a generator on the "formae://" scheme
// resource references use could let a $gen and a $ref collide on one entry.
// The distinct scheme rules that out by construction, exactly as
// GeneratorUpdate.NodeURI does for the ExecutionDAG keyspace. Neither
// segment needs escaping: a KSUID is base62, and $output is checked against
// pkgmodel.KnownGeneratorOutputs at translation.
//
// Either half missing means the envelope names no generator output, and ""
// is never a key: the caller stamps nothing.
func generatorSourceKey(generatorKsuid, output string) string {
	if generatorKsuid == "" || output == "" {
		return ""
	}
	return "generator://" + generatorKsuid + "#/" + output
}

// mergeResObject handles merging of $res and $gen objects (structured resolvable
// references in their pre-resolution shape). It mirrors mergeRefObject: it preserves
// the user's envelope wholesale and only refreshes $value from the plugin read.
//
// The plugin may echo the resolvable back either as a bare scalar (the resolved
// live value) or as a $res/$ref/$gen/$value object; selectRefValue handles both (it
// already unwraps $ref/$value objects, and a $res or $gen echo likewise carries the
// live value at $value — normalized below).
//
// Opacity is INHERITED: a $res that resolves another resource's Opaque property is
// itself opaque even though the consumer's own schema does not mark this field. When
// the envelope carries the inherited $visibility:Opaque marker and we adopted the
// plugin's fresh (plaintext) value, the stale $hashed marker must be dropped so the
// persist transformer re-hashes the field at rest — exactly as mergeRefObject does
// for $ref. Leaving $hashed:true would persist cleartext while claiming it is hashed
// (a plaintext-at-rest leak that the transformer's idempotency guard would then skip).
func (m *propertyMerger) mergeResObject(path string, userVal, pluginVal gjson.Result) {
	cleanPath := m.cleanPath(path)

	userValue := userVal.Get("$value")

	// A plugin that round-trips the resolvable echoes it back as a $res/$ref/
	// $gen/$value object; unwrap to its $value so we compare
	// live-value-to-live-value.
	effectivePluginVal := pluginVal
	if pluginVal.IsObject() && (pluginVal.Get("$res").Exists() || pluginVal.Get("$ref").Exists() || pluginVal.Get("$gen").Exists()) {
		effectivePluginVal = pluginVal.Get("$value")
	}

	valueToSet := m.preferNonNullValue(userValue, effectivePluginVal)
	// Determine "did we keep the stored value?" against the SAME unwrapped value
	// used for valueToSet. keptUserValue only unwraps $ref, so passing the raw
	// pluginVal would treat a plugin's $res echo (a non-empty object) as a fresh
	// value even when its $value is empty — deleting $hashed while the stored hash
	// is retained, which makes the persist transformer hash the digest again
	// (hash-of-hash). Use effectivePluginVal so the two decisions stay consistent.
	keptUser := m.keptUserValue(userValue, effectivePluginVal)

	updatedRes, _ := sjson.Set(userVal.Raw, "$value", valueToSet)

	if userVal.Get("$visibility").String() == pkgmodel.VisibilityOpaque &&
		userVal.Get("$hashed").Bool() && !keptUser {
		updatedRes, _ = sjson.Delete(updatedRes, "$hashed")
	}

	// Provenance baseline: on the echo-merge of formae's own successful
	// write, the envelope's pre-merge $value is the resolution that was
	// actually sent; keep it as $applied so later diffs can compare the
	// written domain against itself. Opaque envelopes are exempt: their
	// value is hashed at rest and has a dedicated suppression path.
	if userVal.Get("$visibility").String() != pkgmodel.VisibilityOpaque {
		if m.writeOrigin {
			if userValue.Exists() && userValue.Value() != nil {
				updatedRes, _ = sjson.Set(updatedRes, "$applied", userValue.Value())
			}
		} else if userVal.Get("$applied").Exists() &&
			!keptUser &&
			!reflect.DeepEqual(valueToSet, userValue.Value()) {
			// The merger adopted a plugin echo that differs from the absorbed
			// one: out-of-band drift in the observed domain. Drop the baseline
			// so the next plan runs the corrective fresh-vs-echo diff.
			updatedRes, _ = sjson.Delete(updatedRes, "$applied")
		}
	}

	updatedRes = m.applyResolutionProvenance(updatedRes, userVal, userValue, effectivePluginVal)

	*m.result, _ = sjson.SetRaw(*m.result, cleanPath, updatedRes)
}

// keptUserValue reports whether selectRefValue preserved the user's stored $value
// (because the plugin returned nothing usable) rather than adopting the plugin's
// live value. It mirrors selectRefValue/preferNonNullValue exactly so the $hashed
// drop decision stays in lock-step with which value was actually chosen.
func (m *propertyMerger) keptUserValue(userValue, pluginVal gjson.Result) bool {
	effectivePluginVal := pluginVal
	if pluginVal.IsObject() && pluginVal.Get("$ref").Exists() {
		effectivePluginVal = pluginVal.Get("$value")
	}
	userHasValue := userValue.Exists() && userValue.Value() != nil
	pluginIsNullOrEmpty := effectivePluginVal.Value() == nil || effectivePluginVal.String() == ""
	return userHasValue && pluginIsNullOrEmpty
}

// selectRefValue determines which value to use for a $ref object's $value field
func (m *propertyMerger) selectRefValue(userValue gjson.Result, pluginVal gjson.Result) any {
	// If plugin value is also a $ref object, use its $value
	if pluginVal.IsObject() && pluginVal.Get("$ref").Exists() {
		pluginValue := pluginVal.Get("$value")
		return m.preferNonNullValue(userValue, pluginValue)
	}

	// Plugin value is a simple value - use it as the $value
	return m.preferNonNullValue(userValue, pluginVal)
}

// preferNonNullValue returns the user value if it exists and is non-null/non-empty,
// otherwise returns the plugin value
func (m *propertyMerger) preferNonNullValue(userValue, pluginValue gjson.Result) any {
	userHasValue := userValue.Exists() && userValue.Value() != nil
	pluginIsNullOrEmpty := pluginValue.Value() == nil || pluginValue.String() == ""

	if userHasValue && pluginIsNullOrEmpty {
		return userValue.Value()
	}
	return pluginValue.Value()
}

// mergeArray handles merging of array values
// Elements are matched based on the updateMethod hint for the field:
// - Default/Set: match by value (JSON equality after flattening $refs)
// - Array: match by index position
// - EntitySet: match by key field (e.g., Tags by "Key")
func (m *propertyMerger) mergeArray(path string, userVal, pluginVal gjson.Result) {
	userArray := userVal.Array()
	pluginArray := pluginVal.Array()

	// An empty user array writes no elements; under a preserveEmptyValues
	// root it is the value and must persist (mirror of the empty-object case).
	if len(userArray) == 0 && m.underPreservedRoot(path) {
		cleanPath := m.cleanPath(path)
		*m.result, _ = sjson.SetRaw(*m.result, cleanPath, "[]")
		return
	}

	// Resolve this array's own hint by its index-less full path (e.g.
	// "ContainerDefinitions.0.Environment" -> "ContainerDefinitions.Environment"),
	// mirroring the diff-calculator's hint-key convention. A nested array must resolve
	// its own hint rather than inherit its top-level field's, or e.g. the ECS env
	// sub-array would inherit ContainerDefinitions' EntitySet hint.
	fieldName := stripArrayIndicesForHintLookup(path)
	hint := m.schema.Hints[fieldName]

	// Track which user elements have been matched (to avoid double-matching)
	matchedUserIndices := make(map[int]bool)

	// Phase 1: Match plugin elements with user elements that have concrete values
	type pendingMatch struct {
		pluginIdx  int
		pluginElem gjson.Result
	}
	var unmatchedPluginElements []pendingMatch

	for i, pluginElem := range pluginArray {
		childPath := fmt.Sprintf("%s.%d", path, i)

		// Find matching user element based on update method
		matchedUserElem, matchedIdx := m.findMatchingUserElementWithIndex(userArray, pluginElem, hint, matchedUserIndices)

		if matchedIdx >= 0 {
			matchedUserIndices[matchedIdx] = true
			m.mergeValue(childPath, matchedUserElem, pluginElem)
		} else {
			// No match found - save for phase 2
			unmatchedPluginElements = append(unmatchedPluginElements, pendingMatch{i, pluginElem})
		}
	}

	// Phase 2: For unmatched plugin elements, pair with user elements that have $ref-without-$value
	// These couldn't be matched in phase 1 because they don't have a concrete value yet.
	for _, pending := range unmatchedPluginElements {
		childPath := fmt.Sprintf("%s.%d", path, pending.pluginIdx)

		matchedUserElem := m.findUnresolvedRefMatch(userArray, pending.pluginElem, pending.pluginIdx, hint, matchedUserIndices)
		if matchedUserElem.matchedIdx >= 0 {
			matchedUserIndices[matchedUserElem.matchedIdx] = true
		}

		m.mergeValue(childPath, matchedUserElem.elem, pending.pluginElem)
	}
}

// findMatchingUserElementWithIndex finds a user array element that matches the plugin element,
// returning both the element and its index. It skips elements already matched (in excludeIndices).
// Returns index -1 if no match found.
func (m *propertyMerger) findMatchingUserElementWithIndex(userArray []gjson.Result, pluginElem gjson.Result, hint pkgmodel.FieldHint, excludeIndices map[int]bool) (gjson.Result, int) {
	if len(userArray) == 0 {
		return gjson.Result{Type: gjson.Null}, -1
	}

	switch hint.UpdateMethod {
	case pkgmodel.FieldUpdateMethodArray:
		// Array: This should match by index, but we don't have the index here
		return gjson.Result{Type: gjson.Null}, -1

	case pkgmodel.FieldUpdateMethodEntitySet:
		// EntitySet: match by key field
		if hint.IndexField == "" {
			return gjson.Result{Type: gjson.Null}, -1
		}
		pluginKeyValue := pluginElem.Get(hint.IndexField).String()
		for i, userElem := range userArray {
			if excludeIndices[i] {
				continue
			}
			userKeyValue := m.flattenRefValue(userElem.Get(hint.IndexField))
			if userKeyValue == pluginKeyValue {
				return userElem, i
			}
		}
		return gjson.Result{Type: gjson.Null}, -1

	default:
		// Default/Set: match by comparing user fields against plugin element
		// Only compare the fields that exist in the user element (plugin may have more fields)
		for i, userElem := range userArray {
			if excludeIndices[i] {
				continue
			}
			// Skip elements that contain $ref without $value - these will be handled in phase 2
			if m.hasUnresolvedRef(userElem) {
				continue
			}
			if m.userElementMatchesPlugin(userElem, pluginElem) {
				return userElem, i
			}
		}
		return gjson.Result{Type: gjson.Null}, -1
	}
}

// unresolvedRefMatch holds the result of finding a user element with unresolved $ref
type unresolvedRefMatch struct {
	elem       gjson.Result
	matchedIdx int
}

// findUnresolvedRefMatch pairs a leftover plugin element with an unmatched user element that
// carries an unresolved $ref (no $value yet) — these couldn't be matched by value in phase 1.
//
//   - Ordered arrays (UpdateMethodArray) pair strictly by index position, so a literal at one
//     index never inherits the $ref of an unresolved-$ref element at another index.
//   - For default/Set object arrays the pairing is structural: a candidate is grafted only when
//     its concrete (non-unresolved-$ref) field(s) uniquely identify the plugin element. So a
//     literal element can never inherit a sibling's $ref; if no candidate's concrete identity
//     matches, the plugin's plain value is rendered instead.
//   - Pure-$ref candidates (no concrete identity field on any of them, e.g. networkInterfaces
//     whose fields are all $refs) fall back to positional pairing so refs are not lost.
func (m *propertyMerger) findUnresolvedRefMatch(userArray []gjson.Result, pluginElem gjson.Result, pluginIdx int, hint pkgmodel.FieldHint, excludeIndices map[int]bool) unresolvedRefMatch {
	if hint.UpdateMethod == pkgmodel.FieldUpdateMethodArray {
		if pluginIdx < len(userArray) && !excludeIndices[pluginIdx] {
			return unresolvedRefMatch{elem: userArray[pluginIdx], matchedIdx: pluginIdx}
		}
		return unresolvedRefMatch{elem: gjson.Result{Type: gjson.Null}, matchedIdx: -1}
	}

	var candidates []unresolvedRefMatch
	anyConcrete := false
	for i, userElem := range userArray {
		if excludeIndices[i] {
			continue
		}
		if !m.hasUnresolvedRef(userElem) {
			continue
		}
		candidates = append(candidates, unresolvedRefMatch{elem: userElem, matchedIdx: i})
		if m.hasConcreteField(userElem) {
			anyConcrete = true
		}
	}

	if len(candidates) == 0 {
		return unresolvedRefMatch{elem: gjson.Result{Type: gjson.Null}, matchedIdx: -1}
	}

	// No candidate carries a concrete identity field: keep positional pairing so refs survive.
	if !anyConcrete {
		return candidates[0]
	}

	// Graft only on a unique concrete-identity match.
	match := unresolvedRefMatch{elem: gjson.Result{Type: gjson.Null}, matchedIdx: -1}
	matchCount := 0
	for _, c := range candidates {
		if !m.hasConcreteField(c.elem) {
			continue
		}
		if m.concreteFieldsMatchPlugin(c.elem, pluginElem) {
			match = c
			matchCount++
		}
	}
	if matchCount == 1 {
		return match
	}
	// Concrete identity exists but no/ambiguous match → render the plain plugin value.
	return unresolvedRefMatch{elem: gjson.Result{Type: gjson.Null}, matchedIdx: -1}
}

// hasUnresolvedRef checks if an element contains any $ref object without a $value.
// These are references where the value isn't known yet (e.g., from PKL translation).
func (m *propertyMerger) hasUnresolvedRef(elem gjson.Result) bool {
	if !elem.IsObject() {
		return false
	}

	hasUnresolved := false
	elem.ForEach(func(key, val gjson.Result) bool {
		if m.isUnresolvedRef(val) {
			hasUnresolved = true
			return false // stop iteration
		}
		return true
	})
	return hasUnresolved
}

// isUnresolvedRef reports whether val is a $ref object that has no $value yet.
func (m *propertyMerger) isUnresolvedRef(val gjson.Result) bool {
	return val.IsObject() && val.Get("$ref").Exists() && !val.Get("$value").Exists()
}

// hasConcreteField reports whether elem has at least one direct field that is not an
// unresolved $ref (a literal or a resolved $ref) — i.e. a field usable as a structural identity.
func (m *propertyMerger) hasConcreteField(elem gjson.Result) bool {
	if !elem.IsObject() {
		return false
	}
	found := false
	elem.ForEach(func(_, val gjson.Result) bool {
		if m.isUnresolvedRef(val) {
			return true // skip unresolved-$ref fields
		}
		found = true
		return false // stop iteration
	})
	return found
}

// concreteFieldsMatchPlugin reports whether every concrete (non-unresolved-$ref) direct field of
// userElem equals the corresponding field of pluginElem. Unresolved-$ref fields are ignored
// because their value isn't known yet.
func (m *propertyMerger) concreteFieldsMatchPlugin(userElem, pluginElem gjson.Result) bool {
	if !userElem.IsObject() || !pluginElem.IsObject() {
		return false
	}
	allMatch := true
	userElem.ForEach(func(key, userVal gjson.Result) bool {
		if m.isUnresolvedRef(userVal) {
			return true // ignore unresolved-$ref fields
		}
		pluginVal := pluginElem.Get(escapePathKey(key.String()))
		if !pluginVal.Exists() || !m.valuesMatch(userVal, pluginVal) {
			allMatch = false
			return false
		}
		return true
	})
	return allMatch
}

// flattenRefValue extracts the actual value from a potential $ref object
// If the value is a $ref object, returns the $value; otherwise returns the string value
func (m *propertyMerger) flattenRefValue(val gjson.Result) string {
	if val.IsObject() && val.Get("$ref").Exists() {
		return val.Get("$value").String()
	}
	return val.String()
}

// userElementMatchesPlugin checks if a user array element matches a plugin array element
// by comparing only the fields that exist in the user element. The plugin element may have
// additional fields (e.g., fingerprint, kind, name) that the user didn't specify.
// For $ref objects in user element, we compare the $value against the plugin's plain value.
func (m *propertyMerger) userElementMatchesPlugin(userElem, pluginElem gjson.Result) bool {
	if !userElem.IsObject() || !pluginElem.IsObject() {
		// For non-objects, do direct comparison after flattening $refs
		return m.flattenRefValue(userElem) == m.flattenRefValue(pluginElem)
	}

	// For objects, check that every field in userElem matches the corresponding field in pluginElem
	allFieldsMatch := true
	userElem.ForEach(func(key, userVal gjson.Result) bool {
		keyStr := key.String()
		pluginVal := pluginElem.Get(escapePathKey(keyStr))

		// If plugin doesn't have this field, it's not a match
		if !pluginVal.Exists() {
			allFieldsMatch = false
			return false
		}

		// Compare the values (flattening $refs as needed)
		if !m.valuesMatch(userVal, pluginVal) {
			allFieldsMatch = false
			return false
		}

		return true
	})

	return allFieldsMatch
}

// valuesMatch compares two values, handling $ref objects by comparing their $value
// against the plugin's plain value. Recursively handles nested objects.
func (m *propertyMerger) valuesMatch(userVal, pluginVal gjson.Result) bool {
	// If user value is a $ref object, compare its $value against plugin value
	if userVal.IsObject() && userVal.Get("$ref").Exists() {
		userValue := userVal.Get("$value")
		// If plugin is also a $ref object, compare $values
		if pluginVal.IsObject() && pluginVal.Get("$ref").Exists() {
			pluginValue := pluginVal.Get("$value")
			return userValue.String() == pluginValue.String()
		}
		// Plugin is a plain value - compare against user's $value
		return userValue.String() == pluginVal.String()
	}

	// If both are objects (but user is not a $ref), recursively compare fields
	if userVal.IsObject() && pluginVal.IsObject() {
		return m.userElementMatchesPlugin(userVal, pluginVal)
	}

	// If both are arrays, compare element by element
	if userVal.IsArray() && pluginVal.IsArray() {
		userArray := userVal.Array()
		pluginArray := pluginVal.Array()
		if len(userArray) != len(pluginArray) {
			return false
		}
		for i, userItem := range userArray {
			if !m.valuesMatch(userItem, pluginArray[i]) {
				return false
			}
		}
		return true
	}

	// For primitives, compare string representation
	return userVal.String() == pluginVal.String()
}

// mergePrimitive handles merging of primitive values
// Primitive values from user are kept only if they don't exist in the plugin response
func (m *propertyMerger) mergePrimitive(path string, userVal gjson.Result) {
	cleanPath := m.cleanPath(path)
	pluginValue := m.pluginRoot.Get(cleanPath)

	// Only set user value if plugin doesn't have this field
	if !pluginValue.Exists() {
		*m.result, _ = sjson.SetRaw(*m.result, cleanPath, userVal.Raw)
	}
}

// buildChildPath constructs a JSON path for a child field. The field name is a
// literal JSON key, so path-special characters in it must be escaped — K8s
// label/annotation keys like "app.kubernetes.io/name" would otherwise be
// interpreted as nested paths and exploded into object trees on write.
func (m *propertyMerger) buildChildPath(parentPath, fieldName string) string {
	escaped := escapePathKey(fieldName)
	if parentPath == "" {
		return escaped
	}
	return parentPath + "." + escaped
}

// escapePathKey escapes a literal JSON key for use in a gjson/sjson path.
var pathKeyEscaper = strings.NewReplacer(`\`, `\\`, `.`, `\.`, `*`, `\*`, `?`, `\?`, `:`, `\:`)

func escapePathKey(key string) string {
	return pathKeyEscaper.Replace(key)
}

// cleanPath removes leading dot from a path if present
func (m *propertyMerger) cleanPath(path string) string {
	if path != "" && path[0] == '.' {
		return path[1:]
	}
	return path
}
