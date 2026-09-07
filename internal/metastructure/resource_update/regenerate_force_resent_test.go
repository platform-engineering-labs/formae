// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Execution-time patch regeneration must never strip a requiredOnUpdate
// field's force-resent op, even when it is the ONLY op the regenerated diff
// would otherwise produce. This update is already in flight (Operation is
// OperationUpdate): dropping the op here — rather than at planning, before
// the update was ever created — would send the plugin an Update whose
// PatchDocument is missing a field the provider mandates in every update
// payload.
func TestResolveValue_RegeneratesForceResentFieldEvenWhenNothingElseChanged(t *testing.T) {
	schema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "ParentRef", "Token"},
		Hints: map[string]pkgmodel.FieldHint{
			"Token": {RequiredOnUpdate: true},
		},
	}

	ru := ResourceUpdate{
		Operation: OperationUpdate,
		PriorState: pkgmodel.Resource{
			Label: "consumer", Type: "Test::Config::Entry", Schema: schema,
			Properties: json.RawMessage(`{"Name": "c1", "ParentRef": "hello", "Token": "t1"}`),
		},
		DesiredState: pkgmodel.Resource{
			Label: "consumer", Type: "Test::Config::Entry", Schema: schema,
			Properties: json.RawMessage(`{"Name": "c1", "ParentRef": {"$ref": "formae://k-src#/Value"}, "Token": "t1"}`),
		},
		RemainingResolvables: []pkgmodel.FormaeURI{"formae://k-src#/Value"},
	}

	// The reference resolves back to exactly the stored value: nothing about
	// this resource actually changes. Token is unchanged too. The only op a
	// regenerated diff can produce is Token's force-resent "add".
	require.NoError(t, ru.ResolveValue("formae://k-src#/Value", "hello", pkgmodel.FormaApplyModeReconcile))

	assert.NotEmpty(t, ru.DesiredState.PatchDocument,
		"regeneration must not overwrite an in-flight update's patch with empty when only the force-resent op remains")
	assert.Contains(t, string(ru.DesiredState.PatchDocument), "Token")
	assert.Contains(t, string(ru.DesiredState.PatchDocument), "t1")
}
