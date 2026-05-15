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

	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// TestResourceUpdateForExisting_CreateOnlyResolvableFilteredOut covers the
// regression where a createOnly Resolvable in the NEW resource was added to
// RemainingResolvables even though the setOnce filter had already replaced
// it with the existing value in DesiredState.Properties. ResolvePropertyReferences
// would then fail to find the URI's $ref slot in the patched payload and return
// "failed to set property value for <uri>", marking the update Failed before
// plugin.Update was ever invoked.
//
// Surfaced by: AWS::SES::ConfigurationSetEventDestination conformance test
// — `configurationSet` is `createOnly = true` and the test fixture references
// it via `parentCs.res.name` Resolvable. After fix, the Update completes the
// resolving phase with an empty RemainingResolvables and proceeds to update.
func TestResourceUpdateForExisting_CreateOnlyResolvableFilteredOut(t *testing.T) {
	ksuid := util.NewID()
	parentURI := "formae://3DkParentCsKsuidExample00000000#/Name"

	existing := pkgmodel.Resource{
		Ksuid:    ksuid,
		Label:    "test-event-destination",
		Type:     "AWS::SES::ConfigurationSetEventDestination",
		Stack:    "default",
		Target:   "test-target",
		NativeID: "test-cs|bounces",
		Schema: pkgmodel.Schema{
			Fields: []string{"ConfigurationSetName", "EventDestination"},
		},
		// Existing has the resolved scalar value (the parent CS's actual Name).
		Properties: json.RawMessage(`{"ConfigurationSetName": "test-cs", "EventDestination": {"Enabled": true}}`),
		Managed:    true,
	}

	new := pkgmodel.Resource{
		Ksuid:  ksuid,
		Label:  "test-event-destination",
		Type:   "AWS::SES::ConfigurationSetEventDestination",
		Stack:  "default",
		Target: "test-target",
		Schema: pkgmodel.Schema{
			Fields: []string{"ConfigurationSetName", "EventDestination"},
		},
		// New has the createOnly Resolvable + an actual change on a separate field.
		// `$strategy: SetOnce` is what the PKL→JSON serializer emits for
		// `createOnly = true` fields; it's the marker filterSetOnceProps keys on
		// to replace the new $ref with the existing scalar value.
		Properties: json.RawMessage(`{"ConfigurationSetName": {"$ref": "` + parentURI + `", "$strategy": "SetOnce"}, "EventDestination": {"Enabled": false}}`),
		Managed:    true,
	}

	target := pkgmodel.Target{Label: "test-target", Namespace: "aws", Config: json.RawMessage(`{}`)}
	resolvableProps := resolver.ResolvableProperties{}

	updates, err := NewResourceUpdateForExisting(
		resolvableProps, existing, new, target, target,
		pkgmodel.FormaApplyModeReconcile, FormaCommandSourceUser,
	)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	update := updates[0]

	assert.Equal(t, OperationUpdate, update.Operation,
		"non-createOnly change on EventDestination.Enabled should plan an Update, not a Replace")
	assert.Empty(t, update.RemainingResolvables,
		"createOnly Resolvable was substituted by the setOnce filter — its URI must not appear in RemainingResolvables, since DesiredState.Properties no longer has a $ref slot for it")
}
