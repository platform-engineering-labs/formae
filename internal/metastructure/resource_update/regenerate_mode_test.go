// © 2025 Platform Engineering Labs Inc.
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

// Execution-time patch regeneration must derive the patch under the same
// apply-mode semantics the command was planned with: under reconcile, an
// EntitySet member present in the prior state and absent from the desired
// state produces a remove op; under patch mode, absence means "leave
// unchanged" and no remove op may appear.
//
// A plain scalar field dropped from the desired state does not diff under
// either strategy (jsonpatch only emits removes for EntitySet-hinted
// collections — see jsonpatch.diff), so it cannot distinguish the two modes.
// The fixture uses an EntitySet-hinted Tags field losing a member instead,
// alongside a still-unresolved Endpoint reference, to exercise regeneration
// after a resolve while keeping the mode-sensitive removal observable.
func TestResolveValue_RegeneratesUnderCommandMode(t *testing.T) {
	newUpdate := func() ResourceUpdate {
		schema := pkgmodel.Schema{
			Identifier: "Name",
			Fields:     []string{"Name", "Endpoint", "Tags"},
			Hints: map[string]pkgmodel.FieldHint{
				"Tags": {UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Key"},
			},
		}
		return ResourceUpdate{
			Operation: OperationUpdate,
			PriorState: pkgmodel.Resource{
				Label: "consumer", Type: "Test::Config::Entry", Schema: schema,
				Properties: json.RawMessage(`{"Name": "c1", "Endpoint": "old-endpoint", "Tags": [{"Key": "env", "Value": "prod"}, {"Key": "legacy", "Value": "true"}]}`),
			},
			DesiredState: pkgmodel.Resource{
				Label: "consumer", Type: "Test::Config::Entry", Schema: schema,
				Properties: json.RawMessage(`{"Name": "c1", "Endpoint": {"$ref": "formae://k-src#/Endpoint"}, "Tags": [{"Key": "env", "Value": "prod"}]}`),
			},
			RemainingResolvables: []pkgmodel.FormaeURI{"formae://k-src#/Endpoint"},
		}
	}

	reconcile := newUpdate()
	require.NoError(t, reconcile.ResolveValue("formae://k-src#/Endpoint", "new-endpoint", pkgmodel.FormaApplyModeReconcile))
	assert.Contains(t, string(reconcile.DesiredState.PatchDocument), "remove",
		"reconcile regeneration must keep the remove op for the dropped Tags member")
	assert.Contains(t, string(reconcile.DesiredState.PatchDocument), "Tags")

	patchMode := newUpdate()
	require.NoError(t, patchMode.ResolveValue("formae://k-src#/Endpoint", "new-endpoint", pkgmodel.FormaApplyModePatch))
	assert.NotContains(t, string(patchMode.DesiredState.PatchDocument), "remove",
		"patch-mode regeneration must leave the undeclared Tags member unchanged")
}
