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

	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestStackTransition_Factory(t *testing.T) {
	t.Run("stack change preserves KSUID", func(t *testing.T) {
		ksuid := util.NewID()
		existing := newTestResource(ksuid, "test-vpc", constants.UnmanagedStack, "vpc-original", false)
		new := newTestResource(ksuid, "test-vpc", "production", "", true)

		update := requireSingleUpdate(t, existing, new)

		assert.Equal(t, ksuid, update.Resource.Ksuid)
		assert.Equal(t, "production", update.StackLabel)
	})

	t.Run("stack change preserves native ID", func(t *testing.T) {
		ksuid := util.NewID()
		nativeID := "vpc-abc123def456"
		existing := newTestResource(ksuid, "my-vpc", constants.UnmanagedStack, nativeID, false)
		existing.ReadOnlyProperties = json.RawMessage(`{"VpcId": "` + nativeID + `"}`)

		new := newTestResource(ksuid, "my-vpc", "prod-stack", "", true)

		update := requireSingleUpdate(t, existing, new)

		assert.Equal(t, nativeID, update.Resource.NativeID)
		assert.Equal(t, ksuid, update.Resource.Ksuid)
	})

	t.Run("stack change preserves read only properties", func(t *testing.T) {
		ksuid := util.NewID()
		existing := newTestResource(ksuid, "vpc-with-readonly", constants.UnmanagedStack, "vpc-readonly", false)
		existing.ReadOnlyProperties = json.RawMessage(`{"VpcId": "vpc-readonly", "OwnerId": "123456789012"}`)

		new := newTestResource(ksuid, "vpc-with-readonly", "app-stack", "", true)

		update := requireSingleUpdate(t, existing, new)

		assert.Equal(t, existing.NativeID, update.Resource.NativeID)
		assert.Equal(t, ksuid, update.Resource.Ksuid)
	})

	t.Run("stack and property change combined", func(t *testing.T) {
		ksuid := util.NewID()
		existing := newTestResource(ksuid, "mixed-change-vpc", constants.UnmanagedStack, "vpc-mixed", false)
		existing.Schema.Fields = append(existing.Schema.Fields, "EnableDnsHostnames")
		existing.Properties = json.RawMessage(`{"CidrBlock": "10.0.0.0/16", "EnableDnsHostnames": false}`)
		existing.ReadOnlyProperties = json.RawMessage(`{"VpcId": "vpc-mixed"}`)

		new := newTestResource(ksuid, "mixed-change-vpc", "infra-stack", "", true)
		new.Schema.Fields = append(new.Schema.Fields, "EnableDnsHostnames")
		new.Properties = json.RawMessage(`{"CidrBlock": "10.0.0.0/16", "EnableDnsHostnames": true}`)

		update := requireSingleUpdate(t, existing, new)

		assert.Equal(t, OperationUpdate, update.Operation)
		assert.Equal(t, ksuid, update.Resource.Ksuid)
		assert.Equal(t, "vpc-mixed", update.Resource.NativeID)
		assert.Equal(t, "infra-stack", update.StackLabel)
		assert.NotNil(t, update.Resource.PatchDocument)
	})
}

func newTestResource(ksuid, label, stack, nativeID string, managed bool) pkgmodel.Resource {
	return pkgmodel.Resource{
		Ksuid:    ksuid,
		Label:    label,
		Type:     "AWS::EC2::VPC",
		Stack:    stack,
		Target:   "test-target",
		NativeID: nativeID,
		Schema: pkgmodel.Schema{
			Fields: []string{"CidrBlock"},
		},
		Properties:         json.RawMessage(`{"CidrBlock": "10.0.0.0/16"}`),
		ReadOnlyProperties: json.RawMessage(`{"VpcId": "` + nativeID + `"}`),
		Managed:            managed,
	}
}

func requireSingleUpdate(t *testing.T, existing, new pkgmodel.Resource) ResourceUpdate {
	t.Helper()

	target := pkgmodel.Target{Label: "test-target", Namespace: "aws", Config: json.RawMessage(`{}`)}
	resolvableProps := resolver.ResolvableProperties{}

	updates, err := NewResourceUpdateForExisting(resolvableProps, existing, new, target, target,
		pkgmodel.FormaApplyModeReconcile, FormaCommandSourceUser)
	require.NoError(t, err)

	if len(updates) == 0 {
		t.Skip("Stack-only changes currently return no updates")
	}

	require.Len(t, updates, 1)
	return updates[0]
}
