// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"fmt"
	"testing"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// coOwnedSetSchema declares one field, "Tags", as a co-owned Set: the shape
// TestCoOwnedSetShrinkDrainsExactlyTheRemovedMember (patch package) also
// uses, so identities are canonical-JSON-encoded array elements.
func coOwnedSetSchema() pkgmodel.Schema {
	return pkgmodel.Schema{
		Fields: []string{"Tags"},
		Hints: map[string]pkgmodel.FieldHint{
			"Tags": {UpdateMethod: pkgmodel.FieldUpdateMethodSet, CoOwned: &pkgmodel.CoOwnership{}},
		},
	}
}

// plainSchema declares no co-owned fields at all.
func plainSchema() pkgmodel.Schema {
	return pkgmodel.Schema{Fields: []string{"Tags"}}
}

func ownershipTestTarget() pkgmodel.Target {
	return pkgmodel.Target{Label: "test-target", Namespace: "aws", Config: json.RawMessage(`{}`)}
}

func newResourceForOwnershipTest(schema pkgmodel.Schema, label, stack string, props json.RawMessage, owned pkgmodel.OwnedMembers) pkgmodel.Resource {
	return pkgmodel.Resource{
		Ksuid:        "ksuid-" + label,
		Label:        label,
		Type:         "FakeAWS::EC2::SecurityGroup",
		Stack:        stack,
		Target:       "test-target",
		Schema:       schema,
		Properties:   props,
		Managed:      true,
		OwnedMembers: owned,
	}
}

// Carry: a real update (a non-co-owned field changes) must carry the
// existing ownership record forward onto DesiredState untouched — the echo
// recompute at execution time is what replaces it, not planning.
func TestNewResourceUpdateForExisting_OwnershipRecord_CarriedOnRealUpdate(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Tags"},
		Hints: map[string]pkgmodel.FieldHint{
			"Tags": {UpdateMethod: pkgmodel.FieldUpdateMethodSet, CoOwned: &pkgmodel.CoOwnership{}},
		},
	}
	record := pkgmodel.OwnedMembers{"Tags": {Rule: "Set", Members: []string{`"a"`, `"b"`}}}

	existing := newResourceForOwnershipTest(schema, "sg", "default", json.RawMessage(`{"Name":"old","Tags":["a","b"]}`), record)
	newRes := newResourceForOwnershipTest(schema, "sg", "default", json.RawMessage(`{"Name":"new","Tags":["a","b"]}`), nil)
	newRes.Ksuid = "" // desired declarations never carry a KSUID of their own

	updates, err := NewResourceUpdateForExisting(resolver.ResolvableProperties{}, nil, existing, newRes,
		ownershipTestTarget(), ownershipTestTarget(), pkgmodel.FormaApplyModeReconcile, FormaCommandSourceUser, false, false)
	require.NoError(t, err)
	require.Len(t, updates, 1)

	update := updates[0]
	assert.False(t, update.RecordOnly)
	assert.True(t, pkgmodel.OwnedMembersEqual(record, update.DesiredState.OwnedMembers),
		"the existing record must be carried onto DesiredState")
	assert.True(t, pkgmodel.OwnedMembersEqual(record, update.PriorState.OwnedMembers),
		"PriorState is the existing resource verbatim, so it must still carry the record too")
}

// Bootstrap through the identical-resources return: existing and desired are
// byte-identical (no record yet), but the declared and live values on a
// co-owned path intersect non-emptily. The claim check must run BEFORE the
// identical-resources early return, or this legacy-bootstrap case is
// swallowed and the record never gets written.
func TestNewResourceUpdateForExisting_OwnershipRecord_BootstrapThroughIdenticalResourcesReturn(t *testing.T) {
	schema := coOwnedSetSchema()
	props := json.RawMessage(`{"Tags":["a","b"]}`)

	existing := newResourceForOwnershipTest(schema, "sg", "default", props, nil)
	newRes := existing // byte-for-byte identical, including nil OwnedMembers

	updates, err := NewResourceUpdateForExisting(resolver.ResolvableProperties{}, nil, existing, newRes,
		ownershipTestTarget(), ownershipTestTarget(), pkgmodel.FormaApplyModeReconcile, FormaCommandSourceUser, false, false)
	require.NoError(t, err)
	require.Len(t, updates, 1, "the ownership delta must still produce exactly one update")

	update := updates[0]
	assert.True(t, update.RecordOnly)
	assert.JSONEq(t, `[]`, string(update.DesiredState.PatchDocument))
	assert.JSONEq(t, string(props), string(update.DesiredState.Properties))
	want := pkgmodel.OwnedMembers{"Tags": {Rule: "Set", Members: []string{`"a"`, `"b"`}}}
	assert.True(t, pkgmodel.OwnedMembersEqual(want, update.DesiredState.OwnedMembers))
	assert.Nil(t, update.RemainingResolvables)
}

// Bootstrap through the force-resent-only return: hasChanges is true (a
// resolved reference's byte shape differs even though the value it resolves
// to did not move), but the patch that produces suppresses entirely to a
// single requiredOnUpdate force-resent op — the third early return inside
// the hasChanges branch. A live ownership delta must still be routed to a
// record-only update here rather than dropped, exactly as the two simpler
// early returns are. This reproduces the exact state
// TestGenerateResourceUpdates_RequiredOnUpdateConsumer_BehavesIdentically's
// "unchanged root plans nothing" scenario puts the consumer in (hasChanges
// true, patch onlyForceResent — confirmed by tracing that scenario), with a
// co-owned field and an existing (nil) ownership record added on top.
func TestNewResourceUpdateForExisting_OwnershipRecord_ForceResentOnlyReturnRoutesToRecordOnly(t *testing.T) {
	producerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Value"},
		Hints:      map[string]pkgmodel.FieldHint{"Name": {CreateOnly: true}},
	}
	consumerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "ParentRef", "Token", "Tags"},
		Hints: map[string]pkgmodel.FieldHint{
			"Token": {RequiredOnUpdate: true},
			"Tags":  {UpdateMethod: pkgmodel.FieldUpdateMethodSet, CoOwned: &pkgmodel.CoOwnership{}},
		},
	}

	producerKsuid := util.NewID()
	consumerKsuid := util.NewID()

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label: "parent", Type: "FakeAWS::Occurrence::Parent",
				Stack: "test-stack", Target: "test-target",
				Schema: producerSchema, Ksuid: producerKsuid,
				Properties: json.RawMessage(`{"Name": "parent-1", "Value": "hello"}`),
			},
			{
				Label: "consumer", Type: "FakeAWS::Occurrence::Consumer",
				Stack: "test-stack", Target: "test-target",
				Schema: consumerSchema, Ksuid: consumerKsuid,
				Properties: json.RawMessage(fmt.Sprintf(
					`{"Name": "consumer-1", "Token": "t1", "Tags": ["a","b"], "ParentRef": {"$ref": "formae://%s#/Value", "$value": "hello"}}`,
					producerKsuid)),
				// No ownership record yet.
			},
		},
	}

	ds, _ := GetDeps(t)
	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	existingTargets := []*pkgmodel.Target{
		{Label: "test-target", Config: json.RawMessage(`{"Region": "us-east-1"}`), Namespace: "test"},
	}
	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Targets: []pkgmodel.Target{
			{Label: "test-target", Config: json.RawMessage(`{"Region": "us-east-1"}`), Namespace: "test"},
		},
		Resources: []pkgmodel.Resource{
			{
				// Unchanged: the parent must not itself produce an update.
				Label: "parent", Type: "FakeAWS::Occurrence::Parent",
				Stack: "test-stack", Target: "test-target",
				Schema:     producerSchema,
				Properties: json.RawMessage(`{"Name": "parent-1", "Value": "hello"}`),
			},
			{
				Label: "consumer", Type: "FakeAWS::Occurrence::Consumer",
				Stack: "test-stack", Target: "test-target",
				Schema: consumerSchema,
				Properties: json.RawMessage(`{
					"Name": "consumer-1",
					"Token": "t1",
					"Tags": ["a","b"],
					"ParentRef": {
						"$res":      true,
						"$label":    "parent",
						"$type":     "FakeAWS::Occurrence::Parent",
						"$stack":    "test-stack",
						"$property": "Value"
					}
				}`),
			},
		},
	}

	updates, err := GenerateResourceUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
		FormaCommandSourceUser, existingTargets, ds, nil, nil, false)
	require.NoError(t, err)

	var consumerUpdates []ResourceUpdate
	for i := range updates {
		if updates[i].DesiredState.Label == "consumer" {
			consumerUpdates = append(consumerUpdates, updates[i])
		}
	}
	require.Len(t, consumerUpdates, 1,
		"the force-resent-only return must not silently drop an owed ownership-record commit")

	update := consumerUpdates[0]
	assert.True(t, update.RecordOnly)
	assert.JSONEq(t, `[]`, string(update.DesiredState.PatchDocument))
	want := pkgmodel.OwnedMembers{"Tags": {Rule: "Set", Members: []string{`"a"`, `"b"`}}}
	assert.True(t, pkgmodel.OwnedMembersEqual(want, update.DesiredState.OwnedMembers))
}

// A stored record already equal to what would be claimed produces no update
// at all — the ownership delta is the only thing that can turn "nothing else
// changed" into an update, and here there is no delta either.
func TestNewResourceUpdateForExisting_OwnershipRecord_AlreadyEqualToClaim_NoUpdate(t *testing.T) {
	schema := coOwnedSetSchema()
	props := json.RawMessage(`{"Tags":["a","b"]}`)
	record := pkgmodel.OwnedMembers{"Tags": {Rule: "Set", Members: []string{`"a"`, `"b"`}}}

	existing := newResourceForOwnershipTest(schema, "sg", "default", props, record)
	newRes := existing

	updates, err := NewResourceUpdateForExisting(resolver.ResolvableProperties{}, nil, existing, newRes,
		ownershipTestTarget(), ownershipTestTarget(), pkgmodel.FormaApplyModeReconcile, FormaCommandSourceUser, false, false)
	require.NoError(t, err)
	assert.Empty(t, updates)
}

// Legacy no-op: a resource with no co-owned fields at all produces a nil
// claim regardless of content, so the pre-existing "nothing changed, skip"
// behavior for a resource with no ownership machinery is unaffected.
func TestNewResourceUpdateForExisting_OwnershipRecord_LegacyNoCoOwnedFields_NoUpdate(t *testing.T) {
	schema := plainSchema()
	props := json.RawMessage(`{"Tags":["a","b"]}`)

	existing := newResourceForOwnershipTest(schema, "sg", "default", props, nil)
	newRes := existing

	updates, err := NewResourceUpdateForExisting(resolver.ResolvableProperties{}, nil, existing, newRes,
		ownershipTestTarget(), ownershipTestTarget(), pkgmodel.FormaApplyModeReconcile, FormaCommandSourceUser, false, false)
	require.NoError(t, err)
	assert.Empty(t, updates)
}

// A declared member that is an unresolved reference (no $value) contributes
// nothing to the claim. Here both sides carry the identical unresolved
// envelope, so there is no other delta either: the claim is nil on both
// sides and the update is dropped exactly like the legacy no-op case,
// proving claimedMembers does not mistake the raw envelope object for a
// member identity.
func TestNewResourceUpdateForExisting_OwnershipRecord_DeferredRefContributesNothing_NoUpdate(t *testing.T) {
	schema := coOwnedSetSchema()
	props := json.RawMessage(`{"Tags":[{"$res":true,"$type":"String"}]}`)

	existing := newResourceForOwnershipTest(schema, "sg", "default", props, nil)
	newRes := existing

	updates, err := NewResourceUpdateForExisting(resolver.ResolvableProperties{}, nil, existing, newRes,
		ownershipTestTarget(), ownershipTestTarget(), pkgmodel.FormaApplyModeReconcile, FormaCommandSourceUser, false, false)
	require.NoError(t, err)
	assert.Empty(t, updates)
}

// A field that is both CoOwned and Opaque records no claim at all: its
// member identities would be its secret values, not names, and recording
// them would persist a secret unhashed. PKL rejects this combination at
// authoring time (see formae.pkl's FieldHint validation), but claimedMembers
// applies the same rule defensively for a schema that reaches it some other
// way. Declared and live agree here (an ordinary claim would otherwise be
// nonempty), proving the skip is specifically about the Opaque hint.
func TestNewResourceUpdateForExisting_OwnershipRecord_OpaqueCoOwnedFieldSkipped_NoUpdate(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Secrets"},
		Hints: map[string]pkgmodel.FieldHint{
			"Secrets": {UpdateMethod: pkgmodel.FieldUpdateMethodSet, CoOwned: &pkgmodel.CoOwnership{}, Opaque: true},
		},
	}
	props := json.RawMessage(`{"Secrets":["s1","s2"]}`)

	existing := newResourceForOwnershipTest(schema, "sg", "default", props, nil)
	newRes := existing

	updates, err := NewResourceUpdateForExisting(resolver.ResolvableProperties{}, nil, existing, newRes,
		ownershipTestTarget(), ownershipTestTarget(), pkgmodel.FormaApplyModeReconcile, FormaCommandSourceUser, false, false)
	require.NoError(t, err)
	assert.Empty(t, updates, "an opaque co-owned field must never produce an ownership-delta update")
}

// Echo recompute: a successful Create progress whose echo contains the
// declared member "a" verbatim but respells "b" (declared "b", live "B")
// must commit a record naming only "a" — the intersection of what was
// declared going into the write with what is actually live now.
func TestRecordProgress_OwnershipRecord_EchoRecomputeKeepsOnlyVerbatimMember(t *testing.T) {
	ru := &ResourceUpdate{
		Operation: OperationCreate,
		DesiredState: pkgmodel.Resource{
			Properties: json.RawMessage(`{"Tags":["a","b"]}`),
			Schema:     coOwnedSetSchema(),
		},
	}

	progress := &plugin.TrackedProgress{
		ProgressResult: resource.ProgressResult{
			Operation:          resource.OperationCreate,
			OperationStatus:    resource.OperationStatusSuccess,
			ResourceProperties: json.RawMessage(`{"Tags":["a","B"]}`),
		},
		Attempts:    1,
		MaxAttempts: 1,
	}

	err := ru.RecordProgress(progress)
	require.NoError(t, err)

	want := pkgmodel.OwnedMembers{"Tags": {Rule: "Set", Members: []string{`"a"`}}}
	assert.True(t, pkgmodel.OwnedMembersEqual(want, ru.DesiredState.OwnedMembers),
		"only the member echoed back verbatim should be claimed, got %#v", ru.DesiredState.OwnedMembers)
}

// The echo recompute must apply the same Opaque skip claimedMembers does at
// planning: a co-owned opaque field's identities are secret values, so a
// successful write must never populate OwnedMembers with them, even though
// declared and echoed values agree perfectly (an ordinary field would
// produce a claim here).
func TestRecordProgress_OwnershipRecord_OpaqueCoOwnedFieldEchoRecordsNoClaim(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Secrets"},
		Hints: map[string]pkgmodel.FieldHint{
			"Secrets": {UpdateMethod: pkgmodel.FieldUpdateMethodSet, CoOwned: &pkgmodel.CoOwnership{}, Opaque: true},
		},
	}
	ru := &ResourceUpdate{
		Operation: OperationCreate,
		DesiredState: pkgmodel.Resource{
			Properties: json.RawMessage(`{"Secrets":["s1","s2"]}`),
			Schema:     schema,
		},
	}

	progress := &plugin.TrackedProgress{
		ProgressResult: resource.ProgressResult{
			Operation:          resource.OperationCreate,
			OperationStatus:    resource.OperationStatusSuccess,
			ResourceProperties: json.RawMessage(`{"Secrets":["s1","s2"]}`),
		},
		Attempts:    1,
		MaxAttempts: 1,
	}

	err := ru.RecordProgress(progress)
	require.NoError(t, err)

	assert.Empty(t, ru.DesiredState.OwnedMembers,
		"an opaque co-owned field must never be recorded, got %#v", ru.DesiredState.OwnedMembers)
}

// A read-shaped merge (writeOrigin false — sync/discovery observing state
// nobody here caused) must never move the ownership record, even when the
// read reports live content that would otherwise change the claim.
func TestRecordProgress_OwnershipRecord_ReadOriginLeavesRecordUntouched(t *testing.T) {
	preset := pkgmodel.OwnedMembers{"Tags": {Rule: "Set", Members: []string{`"a"`, `"b"`}}}
	ru := &ResourceUpdate{
		Operation: OperationRead,
		DesiredState: pkgmodel.Resource{
			Properties:   json.RawMessage(`{"Tags":["a","b"]}`),
			Schema:       coOwnedSetSchema(),
			OwnedMembers: preset,
		},
	}

	progress := &plugin.TrackedProgress{
		ProgressResult: resource.ProgressResult{
			Operation:          resource.OperationRead,
			OperationStatus:    resource.OperationStatusSuccess,
			ResourceProperties: json.RawMessage(`{"Tags":["a","b","c"]}`),
		},
		Attempts:    1,
		MaxAttempts: 1,
	}

	err := ru.RecordProgress(progress)
	require.NoError(t, err)

	assert.True(t, pkgmodel.OwnedMembersEqual(preset, ru.DesiredState.OwnedMembers),
		"a read-origin merge must leave OwnedMembers exactly as it was")
}

// recordOnlyGuardProcess drives update() through the RecordOnly synthetic
// path while recording every message sent through proc.Call — including any
// plugin-spawn request, which must never appear for a RecordOnly update — and
// answers persister-shaped calls the way persistingProcess (opaque_log_redaction_test.go)
// does, so handleProgressUpdate's persist/progress calls succeed.
type recordOnlyGuardProcess struct {
	*stubUpdaterProcess
	log   *capturingLog
	calls []any
}

func (p *recordOnlyGuardProcess) Log() gen.Log { return p.log }

func (p *recordOnlyGuardProcess) Call(_ any, msg any) (any, error) {
	p.calls = append(p.calls, msg)
	return "resource-version-1", nil
}

func (p *recordOnlyGuardProcess) reachedPluginSpawn() bool {
	for _, c := range p.calls {
		if _, ok := c.(messages.SpawnPluginOperator); ok {
			return true
		}
	}
	return false
}

// Executor predicate: a RecordOnly update must skip the plugin call entirely
// and synthesize a successful Update progress, the same way isLabelOnlyChange
// does. There is no existing unit seam that exercises isLabelOnlyChange
// itself (no test in this package drives update() through that branch), so
// this test drives update() directly — the narrowest seam that exists for
// exercising the executor's provider-skip predicates at all.
//
// Stored Tags carries "x", a co-actor's member this forma never declared and
// never claimed (the planning-time claim, preset on DesiredState.OwnedMembers,
// names only "a"). The synthesized success progress echoes the stored
// properties verbatim (completeProperties merges PriorState.Properties/
// ReadOnlyProperties), which is exactly the shape that used to make the echo
// recompute treat "declared" and "live" as the same document and claim every
// live member — "x" included. The post-update record must still equal the
// planning-time claim, not the live set.
func TestUpdate_RecordOnly_SkipsPluginCallAndSynthesizesSuccess(t *testing.T) {
	schema := coOwnedSetSchema()
	storedProps := json.RawMessage(`{"Tags":["a","x"]}`)
	planningClaim := pkgmodel.OwnedMembers{"Tags": {Rule: "Set", Members: []string{`"a"`}}}

	ru := &ResourceUpdate{
		Operation:  OperationUpdate,
		RecordOnly: true,
		PriorState: pkgmodel.Resource{
			Label: "sg", Type: "FakeAWS::EC2::SecurityGroup", Stack: "default", Target: "test-target",
			Schema:             schema,
			Properties:         storedProps,
			ReadOnlyProperties: json.RawMessage(`{"GroupId":"sg-1"}`),
		},
		PreviousProperties: storedProps,
		DesiredState: pkgmodel.Resource{
			Ksuid: "ksuid-sg",
			Label: "sg", Type: "FakeAWS::EC2::SecurityGroup", Stack: "default", Target: "test-target",
			Schema:        schema,
			Properties:    storedProps,
			PatchDocument: json.RawMessage(`[]`),
			// The planning-time claim (this forma declares only "a"; "x" is
			// live but a co-actor's), exactly as NewResourceUpdateForExisting
			// would have stamped it.
			OwnedMembers: planningClaim,
		},
	}
	data := ResourceUpdateData{resourceUpdate: ru, commandID: "cmd-1"}
	proc := &recordOnlyGuardProcess{stubUpdaterProcess: &stubUpdaterProcess{}, log: &capturingLog{}}

	state, _, _, err := update(StateUpdating, data, proc)
	require.NoError(t, err)

	assert.False(t, proc.reachedPluginSpawn(), "a RecordOnly update must never spawn a plugin operator")
	assert.Equal(t, StateFinishedSuccessfully, state)
	assert.Equal(t, ResourceUpdateStateSuccess, data.resourceUpdate.State)
	assert.True(t, pkgmodel.OwnedMembersEqual(planningClaim, data.resourceUpdate.DesiredState.OwnedMembers),
		"the committed record must equal the planning-time claim, not the live set; got %#v",
		data.resourceUpdate.DesiredState.OwnedMembers)
}

// The same over-claim risk exists for a label-only rename: it too synthesizes
// a successful Update progress from the stored properties with no plugin
// call, and it too must not let that synthetic echo repopulate OwnedMembers
// from the full live set. RecordOnly is false here (this update is really a
// rename), so the fix cannot rely on ru.RecordOnly alone — it must hold
// because declaredDoc equals PreviousProperties (nothing about the
// properties changed), the general condition the executor's synthetic path
// guarantees whenever it fires.
func TestUpdate_LabelOnlyRename_DoesNotOverClaimCoActorMember(t *testing.T) {
	schema := coOwnedSetSchema()
	storedProps := json.RawMessage(`{"Tags":["a","x"]}`)
	planningClaim := pkgmodel.OwnedMembers{"Tags": {Rule: "Set", Members: []string{`"a"`}}}

	ru := &ResourceUpdate{
		Operation: OperationUpdate,
		PriorState: pkgmodel.Resource{
			Label: "sg-old", Type: "FakeAWS::EC2::SecurityGroup", Stack: "default", Target: "test-target",
			Schema:     schema,
			Properties: storedProps,
		},
		PreviousProperties: storedProps,
		DesiredState: pkgmodel.Resource{
			Ksuid: "ksuid-sg",
			// Label differs from PriorState.Label; Stack/Target match — the
			// isLabelOnlyChange shape.
			Label: "sg-new", Type: "FakeAWS::EC2::SecurityGroup", Stack: "default", Target: "test-target",
			Schema:        schema,
			Properties:    storedProps,
			PatchDocument: json.RawMessage(`[]`),
			OwnedMembers:  planningClaim,
		},
	}
	data := ResourceUpdateData{resourceUpdate: ru, commandID: "cmd-1"}
	proc := &recordOnlyGuardProcess{stubUpdaterProcess: &stubUpdaterProcess{}, log: &capturingLog{}}

	state, _, _, err := update(StateUpdating, data, proc)
	require.NoError(t, err)

	assert.False(t, proc.reachedPluginSpawn(), "a label-only rename must never spawn a plugin operator")
	assert.Equal(t, StateFinishedSuccessfully, state)
	assert.True(t, pkgmodel.OwnedMembersEqual(planningClaim, data.resourceUpdate.DesiredState.OwnedMembers),
		"a label-only rename must not over-claim a co-actor's live member; got %#v",
		data.resourceUpdate.DesiredState.OwnedMembers)
}
