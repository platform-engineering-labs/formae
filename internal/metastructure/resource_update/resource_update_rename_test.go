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

	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// RFC-0041: pure label rename — alias matches an existing row by old label,
// labels differ, properties are identical. The factory must emit a single
// OperationUpdate carrying PriorState (old label) and DesiredState (new label).
func TestRename_PureLabelChange_AliasDriven(t *testing.T) {
	ksuid := util.NewID()
	nativeID := "i-0abc1234"

	existing := pkgmodel.Resource{
		Ksuid:    ksuid,
		Label:    "web-server",
		Type:     "AWS::EC2::Instance",
		Stack:    "prod",
		Target:   "test-target",
		NativeID: nativeID,
		Schema:   pkgmodel.Schema{Fields: []string{"InstanceType"}},
		Properties: json.RawMessage(`{"InstanceType": "t3.small"}`),
		Managed:    true,
	}
	newResource := existing
	newResource.Label = "app-server"
	newResource.Alias = "web-server"

	target := pkgmodel.Target{Label: "test-target", Namespace: "aws", Config: json.RawMessage(`{}`)}
	updates, err := NewResourceUpdateForExisting(resolver.ResolvableProperties{}, existing, newResource,
		target, target, pkgmodel.FormaApplyModeReconcile, FormaCommandSourceUser)
	require.NoError(t, err)
	require.Len(t, updates, 1, "pure label rename must emit one update, got %d", len(updates))

	u := updates[0]
	assert.Equal(t, OperationUpdate, u.Operation)
	assert.Equal(t, "web-server", u.PriorState.Label, "PriorState carries old label")
	assert.Equal(t, "app-server", u.DesiredState.Label, "DesiredState carries new label")
	assert.Equal(t, ksuid, u.DesiredState.Ksuid, "KSUID preserved")
	assert.Equal(t, nativeID, u.DesiredState.NativeID, "NativeID preserved")
}

// Combined: label changes AND properties change in the same apply.
func TestRename_LabelAndProperties_AliasDriven(t *testing.T) {
	ksuid := util.NewID()

	existing := pkgmodel.Resource{
		Ksuid:      ksuid,
		Label:      "web-server",
		Type:       "AWS::EC2::Instance",
		Stack:      "prod",
		Target:     "test-target",
		NativeID:   "i-0abc1234",
		Schema:     pkgmodel.Schema{Fields: []string{"InstanceType"}},
		Properties: json.RawMessage(`{"InstanceType": "t3.small"}`),
		Managed:    true,
	}
	newResource := existing
	newResource.Label = "app-server"
	newResource.Alias = "web-server"
	newResource.Properties = json.RawMessage(`{"InstanceType": "t3.medium"}`)

	target := pkgmodel.Target{Label: "test-target", Namespace: "aws", Config: json.RawMessage(`{}`)}
	updates, err := NewResourceUpdateForExisting(resolver.ResolvableProperties{}, existing, newResource,
		target, target, pkgmodel.FormaApplyModeReconcile, FormaCommandSourceUser)
	require.NoError(t, err)
	require.Len(t, updates, 1)

	u := updates[0]
	assert.Equal(t, OperationUpdate, u.Operation)
	assert.Equal(t, "web-server", u.PriorState.Label)
	assert.Equal(t, "app-server", u.DesiredState.Label)
	assert.NotNil(t, u.DesiredState.PatchDocument, "patch document expected for property change")
}

// Label mismatch with no alias is still rejected as a generator bug.
func TestRename_LabelMismatchWithoutAlias_Rejected(t *testing.T) {
	existing := pkgmodel.Resource{
		Label:      "web-server",
		Type:       "AWS::EC2::Instance",
		Stack:      "prod",
		Target:     "test-target",
		Schema:     pkgmodel.Schema{Fields: []string{"InstanceType"}},
		Properties: json.RawMessage(`{"InstanceType": "t3.small"}`),
	}
	newResource := existing
	newResource.Label = "app-server"
	// no Alias

	target := pkgmodel.Target{Label: "test-target", Namespace: "aws", Config: json.RawMessage(`{}`)}
	_, err := NewResourceUpdateForExisting(resolver.ResolvableProperties{}, existing, newResource,
		target, target, pkgmodel.FormaApplyModeReconcile, FormaCommandSourceUser)
	require.Error(t, err, "label mismatch without alias must be a generator bug")
}

// Alias that does not match the existing row's label is also rejected.
func TestRename_AliasDoesNotMatchExisting_Rejected(t *testing.T) {
	existing := pkgmodel.Resource{
		Label:      "web-server",
		Type:       "AWS::EC2::Instance",
		Stack:      "prod",
		Target:     "test-target",
		Schema:     pkgmodel.Schema{Fields: []string{"InstanceType"}},
		Properties: json.RawMessage(`{"InstanceType": "t3.small"}`),
	}
	newResource := existing
	newResource.Label = "app-server"
	newResource.Alias = "some-unrelated-name"

	target := pkgmodel.Target{Label: "test-target", Namespace: "aws", Config: json.RawMessage(`{}`)}
	_, err := NewResourceUpdateForExisting(resolver.ResolvableProperties{}, existing, newResource,
		target, target, pkgmodel.FormaApplyModeReconcile, FormaCommandSourceUser)
	require.Error(t, err, "alias mismatch is a generator bug")
}

// RFC-0041 edge case: bringing a resource under management AND renaming it in
// the same apply.
//
// Discovery has assigned a default label (e.g. "i-0abc1234") to an unmanaged
// resource. The user authors a forma with the human label and an alias to the
// discovery default. The apply must rename and assign-to-stack atomically —
// no destroy+create, no two-apply workaround. The existing "bringing under
// management without property changes" path handles this once the factory
// allows the label change via the alias.
func TestRename_BringingUnderManagementAndRename(t *testing.T) {
	ksuid := util.NewID()
	nativeID := "i-0abc1234"

	existing := pkgmodel.Resource{
		Ksuid:      ksuid,
		Label:      "i-0abc1234", // discovery default
		Type:       "AWS::EC2::Instance",
		Stack:      "$unmanaged",
		Target:     "test-target",
		NativeID:   nativeID,
		Schema:     pkgmodel.Schema{Fields: []string{"InstanceType"}},
		Properties: json.RawMessage(`{"InstanceType": "t3.small"}`),
		Managed:    false,
	}
	newResource := existing
	newResource.Label = "web-server"
	newResource.Stack = "prod"
	newResource.Alias = "i-0abc1234"
	newResource.Managed = true

	target := pkgmodel.Target{Label: "test-target", Namespace: "aws", Config: json.RawMessage(`{}`)}
	updates, err := NewResourceUpdateForExisting(resolver.ResolvableProperties{}, existing, newResource,
		target, target, pkgmodel.FormaApplyModeReconcile, FormaCommandSourceUser)
	require.NoError(t, err)
	require.Len(t, updates, 1, "combined import+rename must emit one update")

	u := updates[0]
	assert.Equal(t, OperationUpdate, u.Operation)
	assert.Equal(t, "i-0abc1234", u.PriorState.Label, "PriorState carries discovery default label")
	assert.Equal(t, "$unmanaged", u.PriorState.Stack, "PriorState in $unmanaged")
	assert.Equal(t, "web-server", u.DesiredState.Label, "DesiredState carries new human label")
	assert.Equal(t, "prod", u.DesiredState.Stack, "DesiredState in target stack")
	assert.Equal(t, ksuid, u.DesiredState.Ksuid, "KSUID preserved across import+rename")
	assert.Equal(t, nativeID, u.DesiredState.NativeID, "NativeID preserved")
}

// Generator-level: matchExistingForDesired falls back to alias on tuple miss.
func TestMatchExistingForDesired_AliasFallback(t *testing.T) {
	existing := []*pkgmodel.Resource{
		{Label: "web-server", Type: "AWS::EC2::Instance"},
		{Label: "db-server", Type: "AWS::EC2::Instance"},
	}

	t.Run("current tuple matches first", func(t *testing.T) {
		desired := pkgmodel.Resource{Label: "web-server", Type: "AWS::EC2::Instance"}
		m := matchExistingForDesired(existing, desired)
		require.NotNil(t, m)
		assert.Equal(t, "web-server", m.Label)
	})

	t.Run("miss + alias hits", func(t *testing.T) {
		desired := pkgmodel.Resource{Label: "app-server", Type: "AWS::EC2::Instance", Alias: "web-server"}
		m := matchExistingForDesired(existing, desired)
		require.NotNil(t, m, "alias should match the existing web-server row")
		assert.Equal(t, "web-server", m.Label)
	})

	t.Run("miss + no alias returns nil", func(t *testing.T) {
		desired := pkgmodel.Resource{Label: "app-server", Type: "AWS::EC2::Instance"}
		m := matchExistingForDesired(existing, desired)
		assert.Nil(t, m)
	})

	t.Run("miss + alias also misses", func(t *testing.T) {
		desired := pkgmodel.Resource{Label: "app-server", Type: "AWS::EC2::Instance", Alias: "nothing"}
		m := matchExistingForDesired(existing, desired)
		assert.Nil(t, m)
	})

	t.Run("type must match too", func(t *testing.T) {
		desired := pkgmodel.Resource{Label: "app-server", Type: "AWS::S3::Bucket", Alias: "web-server"}
		m := matchExistingForDesired(existing, desired)
		assert.Nil(t, m, "alias must match within the same type")
	})
}
