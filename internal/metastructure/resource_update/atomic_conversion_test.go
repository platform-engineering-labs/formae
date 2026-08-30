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

	"github.com/platform-engineering-labs/formae/internal/metastructure/util"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Plugin-bound conversion preserves empty collections inside a
// preserveEmptyValues-hinted field, in both the write converter and the
// Read-context converter; unhinted fields keep the rendering-noise strip.
func TestConvertResourceForPlugin_PreserveEmptyFieldSurvives(t *testing.T) {
	res := pkgmodel.Resource{
		Label: "cr", Type: "Test::Custom",
		Schema: pkgmodel.Schema{
			Identifier: "Name",
			Fields:     []string{"Name", "Spec", "Other"},
			Hints: map[string]pkgmodel.FieldHint{
				"Spec": {UpdateMethod: pkgmodel.FieldUpdateMethodAtomic, PreserveEmptyValues: true},
			},
		},
		Properties: json.RawMessage(`{"Name":"cr","Spec":{"selfSigned":{}},"Other":{"empty":{}}}`),
	}

	converted, err := convertResourceForPlugin(res)
	require.NoError(t, err)
	assert.JSONEq(t, `{"selfSigned":{}}`, extractField(t, converted.Properties, "Spec"),
		"write payload keeps the hinted field verbatim")
	assert.JSONEq(t, `{}`, extractField(t, converted.Properties, "Other"),
		"unhinted fields keep the strip")

	readConverted, err := convertResourceForPluginRead(res)
	require.NoError(t, err)
	assert.JSONEq(t, `{"selfSigned":{}}`, extractField(t, readConverted.Properties, "Spec"),
		"Read/sync/delete context carries the true value too")
}

func extractField(t *testing.T, props json.RawMessage, field string) string {
	t.Helper()
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(props, &m))
	return string(m[field])
}

// The persist merge keeps empty collections under a preserveEmptyValues root
// even when the plugin echoes nothing; elsewhere the leaf-only walk drops
// them as before.
func TestMerge_PreserveEmptyRootSurvivesEmptyPluginEcho(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"ApiVersion", "Spec", "Other"},
		Hints:  map[string]pkgmodel.FieldHint{"Spec": {UpdateMethod: pkgmodel.FieldUpdateMethodAtomic, PreserveEmptyValues: true}},
	}
	user := json.RawMessage(`{"ApiVersion":"v1","Spec":{"selfSigned":{"crl":[]}},"Other":{"e":{}}}`)

	merged, err := mergeRefsPreservingUserRefs(user, json.RawMessage(`{}`), schema, true, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"selfSigned":{"crl":[]}}`, extractField(t, merged, "Spec"),
		"the hinted subtree persists verbatim, nested empty list included")
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(merged, &m))
	assert.NotContains(t, m, "Other", "unhinted empty-leaved objects keep today's drop")
}

// The whole-resource change gate must plan an update when the only difference
// is an empty-shaped member inside a preserveEmptyValues root: the stored side
// lost the member to the old normalization, and repairing it IS a change. An
// unhinted field with the same shape keeps the gate's empty tolerance.
func TestGenerateResourceUpdates_PreservedRootEmptyOnlyDifference_Plans(t *testing.T) {
	ds, _ := GetDeps(t)
	crSchema := pkgmodel.Schema{
		Identifier: "FormaeId",
		Fields:     []string{"ApiVersion", "Kind", "FormaeId", "Spec"},
		Hints: map[string]pkgmodel.FieldHint{
			"Spec": {UpdateMethod: pkgmodel.FieldUpdateMethodAtomic, PreserveEmptyValues: true},
		},
	}
	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{{
			Label: "cr", Type: "Test::Custom", Stack: "test-stack", Target: "test-target",
			Schema: crSchema, Ksuid: util.NewID(),
			Properties: json.RawMessage(`{"ApiVersion":"v1","Kind":"W","FormaeId":"x","Spec":{}}`),
		}},
	}
	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	forma := &pkgmodel.Forma{
		Stacks:  []pkgmodel.Stack{{Label: "test-stack"}},
		Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test", Config: json.RawMessage(`{}`)}},
		Resources: []pkgmodel.Resource{{
			Label: "cr", Type: "Test::Custom", Stack: "test-stack", Target: "test-target",
			Schema:     crSchema,
			Properties: json.RawMessage(`{"ApiVersion":"v1","Kind":"W","FormaeId":"x","Spec":{"selfSigned":{}}}`),
		}},
	}
	existingTargets := []*pkgmodel.Target{{Label: "test-target", Namespace: "test", Config: json.RawMessage(`{}`)}}
	updates, err := GenerateResourceUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile, FormaCommandSourceUser, existingTargets, ds, nil, nil, false)
	require.NoError(t, err)
	require.Len(t, updates, 1, "the empty-only difference under a preserved root must plan")
	assert.Contains(t, string(updates[0].DesiredState.PatchDocument), "selfSigned")
}
