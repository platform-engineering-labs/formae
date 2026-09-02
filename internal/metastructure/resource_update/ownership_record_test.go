// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"testing"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
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
func TestUpdate_RecordOnly_SkipsPluginCallAndSynthesizesSuccess(t *testing.T) {
	ru := &ResourceUpdate{
		Operation:  OperationUpdate,
		RecordOnly: true,
		PriorState: pkgmodel.Resource{
			Label: "sg", Type: "FakeAWS::EC2::SecurityGroup", Stack: "default", Target: "test-target",
			Properties:         json.RawMessage(`{"Tags":["a","b"]}`),
			ReadOnlyProperties: json.RawMessage(`{"GroupId":"sg-1"}`),
		},
		DesiredState: pkgmodel.Resource{
			Ksuid: "ksuid-sg",
			Label: "sg", Type: "FakeAWS::EC2::SecurityGroup", Stack: "default", Target: "test-target",
			Properties:    json.RawMessage(`{"Tags":["a","b"]}`),
			PatchDocument: json.RawMessage(`[]`),
			OwnedMembers:  pkgmodel.OwnedMembers{"Tags": {Rule: "Set", Members: []string{`"a"`}}},
		},
	}
	data := ResourceUpdateData{resourceUpdate: ru, commandID: "cmd-1"}
	proc := &recordOnlyGuardProcess{stubUpdaterProcess: &stubUpdaterProcess{}, log: &capturingLog{}}

	state, _, _, err := update(StateUpdating, data, proc)
	require.NoError(t, err)

	assert.False(t, proc.reachedPluginSpawn(), "a RecordOnly update must never spawn a plugin operator")
	assert.Equal(t, StateFinishedSuccessfully, state)
	assert.Equal(t, ResourceUpdateStateSuccess, data.resourceUpdate.State)
}
