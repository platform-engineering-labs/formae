// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"errors"
	"testing"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// A createOnly diff that only appears when a reference resolves at execution
// time means plan-time classification missed a replacement. The update must
// fail with a typed error naming the field: never silently dropped, never an
// undeclared replacement.
func TestResolveValue_LateCreateOnlyDiff_FailsLoudly(t *testing.T) {
	schema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Anchor"},
		Hints:      map[string]pkgmodel.FieldHint{"Anchor": {CreateOnly: true}},
	}
	ru := ResourceUpdate{
		Operation: OperationUpdate,
		PriorState: pkgmodel.Resource{
			Label: "consumer", Type: "Test::Config::Entry", Schema: schema,
			Properties: json.RawMessage(`{"Name": "c1", "Anchor": "old-anchor"}`),
		},
		DesiredState: pkgmodel.Resource{
			Label: "consumer", Type: "Test::Config::Entry", Schema: schema,
			Properties: json.RawMessage(`{"Name": "c1", "Anchor": {"$ref": "formae://k-src#/Anchor"}}`),
		},
		RemainingResolvables: []pkgmodel.FormaeURI{"formae://k-src#/Anchor"},
	}

	err := ru.ResolveValue("formae://k-src#/Anchor", "new-anchor", pkgmodel.FormaApplyModeReconcile)
	require.Error(t, err)
	var late LateCreateOnlyChangeError
	require.True(t, errors.As(err, &late), "the failure must be the typed late-createOnly error")
	assert.Contains(t, late.Fields, "Anchor")
	assert.Equal(t, "consumer", late.ResourceLabel)
}

// The state machine's resolve-completion handler must not just fail the
// update: an operator reading the resource update's status afterward has to
// be able to see WHY it failed. This drives the same late-createOnly diff
// through resourceResolved (the StateResolving message handler that calls
// ResolveValue) and asserts the reason lands on the surface the status query
// reads — MostRecentFailureMessage — naming both the category and the field.
func TestResourceResolved_LateCreateOnlyDiff_PersistsReason(t *testing.T) {
	schema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Anchor"},
		Hints:      map[string]pkgmodel.FieldHint{"Anchor": {CreateOnly: true}},
	}
	ru := &ResourceUpdate{
		Operation: OperationUpdate,
		PriorState: pkgmodel.Resource{
			Label: "consumer", Type: "Test::Config::Entry", Schema: schema,
			Properties: json.RawMessage(`{"Name": "c1", "Anchor": "old-anchor"}`),
		},
		DesiredState: pkgmodel.Resource{
			Label: "consumer", Type: "Test::Config::Entry", Schema: schema,
			Properties: json.RawMessage(`{"Name": "c1", "Anchor": {"$ref": "formae://k-src#/Anchor"}}`),
		},
		RemainingResolvables: []pkgmodel.FormaeURI{"formae://k-src#/Anchor"},
	}
	data := ResourceUpdateData{
		resourceUpdate:           ru,
		commandID:                "cmd-1",
		applyMode:                pkgmodel.FormaApplyModeReconcile,
		originalResourceKsuidURI: ru.DesiredState.URI(),
	}
	proc := newOperationCapturingProcess()

	state, _, _, err := resourceResolved(gen.PID{}, StateResolving, data,
		messages.ValueResolved{ResourceURI: "formae://k-src#/Anchor", Value: "new-anchor"}, proc)
	require.NoError(t, err, "resourceResolved reports the failure on the resource update, not as a Go error")
	assert.Equal(t, StateFinishedWithError, state)

	message := ru.MostRecentFailureMessage()
	require.NotEmpty(t, message, "a late createOnly diff must still surface a reason")
	assert.Contains(t, message, "createOnly")
	assert.Contains(t, message, "Anchor")
}

// spokeShapedUpdate builds the two-reference shape that exercises judgment
// timing: a top-level createOnly reference plus a reference nested inside a
// createOnly sub-resource, with prior state holding the currently applied
// values. References resolve one at a time at execution, so the patch is
// re-derived while sibling references are still pending.
func spokeShapedUpdate() ResourceUpdate {
	schema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Description", "Hub", "LinkedNetwork"},
		Hints: map[string]pkgmodel.FieldHint{
			"Hub":           {CreateOnly: true},
			"LinkedNetwork": {CreateOnly: true},
		},
	}
	return ResourceUpdate{
		Operation: OperationUpdate,
		PriorState: pkgmodel.Resource{
			Label: "spoke", Type: "Test::Network::Spoke", Schema: schema,
			Properties: json.RawMessage(`{"Name": "s1", "Description": "old", "Hub": "hub-1", "LinkedNetwork": {"Uri": "https://net-1"}}`),
		},
		DesiredState: pkgmodel.Resource{
			Label: "spoke", Type: "Test::Network::Spoke", Schema: schema,
			Properties: json.RawMessage(`{"Name": "s1", "Description": "new", "Hub": {"$ref": "formae://k-hub#/Name"}, "LinkedNetwork": {"Uri": {"$ref": "formae://k-net#/SelfLink"}}}`),
		},
		RemainingResolvables: []pkgmodel.FormaeURI{"formae://k-hub#/Name", "formae://k-net#/SelfLink"},
	}
}

// A createOnly path whose reference has not delivered its value yet cannot be
// judged: re-deriving the patch after a SIBLING reference resolves must not
// refuse the update over the still-pending path.
func TestResolveValue_SiblingDeliveryLeavesPendingCreateOnlyRefUnjudged(t *testing.T) {
	ru := spokeShapedUpdate()

	err := ru.ResolveValue("formae://k-hub#/Name", "hub-1", pkgmodel.FormaApplyModePatch)

	require.NoError(t, err, "a pending reference is not judgeable; only delivered values are")
}

// Once every reference has delivered a value equal to the applied one, the
// update proceeds and the re-derived patch carries only the real change.
func TestResolveValue_AllRefsResolveUnchanged_UpdateProceeds(t *testing.T) {
	ru := spokeShapedUpdate()

	require.NoError(t, ru.ResolveValue("formae://k-hub#/Name", "hub-1", pkgmodel.FormaApplyModePatch))
	require.NoError(t, ru.ResolveValue("formae://k-net#/SelfLink", "https://net-1", pkgmodel.FormaApplyModePatch))

	var ops []struct {
		Path string `json:"path"`
	}
	require.NoError(t, json.Unmarshal(ru.DesiredState.PatchDocument, &ops))
	require.Len(t, ops, 1)
	assert.Equal(t, "/Description", ops[0].Path)
}

// A reference that delivers a genuinely different value on a createOnly path
// is still refused — at its own delivery, with the full path of the changed
// member, not just the top-level property.
func TestResolveValue_ChangedNestedCreateOnlyRef_RefusedWithFullPath(t *testing.T) {
	ru := spokeShapedUpdate()

	require.NoError(t, ru.ResolveValue("formae://k-hub#/Name", "hub-1", pkgmodel.FormaApplyModePatch))
	err := ru.ResolveValue("formae://k-net#/SelfLink", "https://net-2", pkgmodel.FormaApplyModePatch)

	require.Error(t, err)
	var late LateCreateOnlyChangeError
	require.True(t, errors.As(err, &late), "the failure must be the typed late-createOnly error")
	assert.Contains(t, late.Fields, "LinkedNetwork.Uri")
}

// A pending reference nested under a property key containing a JSON Pointer
// special character ('/' — RFC 6901-escaped as ~1 in op paths) is still
// recognized as pending: sibling deliveries must not judge it just because
// its op path spells the key differently than the document does.
func TestResolveValue_PendingRefUnderSlashedKeyStaysUnjudged(t *testing.T) {
	schema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Description", "Hub", "Config"},
		Hints: map[string]pkgmodel.FieldHint{
			"Hub":    {CreateOnly: true},
			"Config": {CreateOnly: true},
		},
	}
	ru := ResourceUpdate{
		Operation: OperationUpdate,
		PriorState: pkgmodel.Resource{
			Label: "consumer", Type: "Test::Config::Entry", Schema: schema,
			Properties: json.RawMessage(`{"Name": "c1", "Description": "old", "Hub": "hub-1", "Config": {"example.com/role": "value-1"}}`),
		},
		DesiredState: pkgmodel.Resource{
			Label: "consumer", Type: "Test::Config::Entry", Schema: schema,
			Properties: json.RawMessage(`{"Name": "c1", "Description": "new", "Hub": {"$ref": "formae://k-hub#/Name"}, "Config": {"example.com/role": {"$ref": "formae://k-src#/Role"}}}`),
		},
		RemainingResolvables: []pkgmodel.FormaeURI{"formae://k-hub#/Name", "formae://k-src#/Role"},
	}

	err := ru.ResolveValue("formae://k-hub#/Name", "hub-1", pkgmodel.FormaApplyModePatch)

	require.NoError(t, err, "a pending reference under an escaped key is still pending")
}
