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
