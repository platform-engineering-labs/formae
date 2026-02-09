// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package target_update

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestTargetUpdate_HasChange_NewTarget(t *testing.T) {
	target := pkgmodel.Target{
		Label:        "new-target",
		Namespace:    "default",
		Discoverable: true,
	}

	update := TargetUpdate{
		Target:         target,
		ExistingTarget: nil, // New target
		Operation:      TargetOperationCreate,
		State:          TargetUpdateStateNotStarted,
	}

	assert.True(t, update.HasChange())
}

func TestTargetUpdate_HasChange_DiscoverableChanged(t *testing.T) {
	existing := &pkgmodel.Target{
		Label:        "existing-target",
		Namespace:    "default",
		Discoverable: false,
	}

	newTarget := pkgmodel.Target{
		Label:        "existing-target",
		Namespace:    "default",
		Discoverable: true, // Changed from false to true
	}

	update := TargetUpdate{
		Target:         newTarget,
		ExistingTarget: existing,
		Operation:      TargetOperationUpdate,
		State:          TargetUpdateStateNotStarted,
	}

	assert.True(t, update.HasChange())
}

func TestTargetUpdate_HasChange_NoChange(t *testing.T) {
	existing := &pkgmodel.Target{
		Label:        "unchanged-target",
		Namespace:    "default",
		Discoverable: true,
	}

	newTarget := pkgmodel.Target{
		Label:        "unchanged-target",
		Namespace:    "default",
		Discoverable: true, // Same as existing
	}

	update := TargetUpdate{
		Target:         newTarget,
		ExistingTarget: existing,
		Operation:      TargetOperationUpdate,
		State:          TargetUpdateStateNotStarted,
	}

	assert.False(t, update.HasChange())
}

func TestValidateImmutableFields_Success_IdenticalFields(t *testing.T) {
	config := json.RawMessage(`{"region": "us-east-1", "account": "123456789"}`)

	existing := &pkgmodel.Target{
		Label:     "test-target",
		Namespace: "production",
		Config:    config,
	}

	new := &pkgmodel.Target{
		Label:     "test-target",
		Namespace: "production",
		Config:    config,
	}

	err := ValidateImmutableFields(existing, new)
	assert.NoError(t, err)
}

func TestValidateImmutableFields_Success_EquivalentConfig(t *testing.T) {
	existing := &pkgmodel.Target{
		Label:     "test-target",
		Namespace: "production",
		Config:    json.RawMessage(`{"region": "us-east-1", "account": "123"}`),
	}

	new := &pkgmodel.Target{
		Label:     "test-target",
		Namespace: "production",
		Config:    json.RawMessage(`{"account": "123", "region": "us-east-1"}`), // Different order
	}

	err := ValidateImmutableFields(existing, new)
	assert.NoError(t, err)
}

func TestValidateImmutableFields_Error_NamespaceMismatch(t *testing.T) {
	config := json.RawMessage(`{"region": "us-east-1"}`)

	existing := &pkgmodel.Target{
		Label:     "test-target",
		Namespace: "production",
		Config:    config,
	}

	new := &pkgmodel.Target{
		Label:     "test-target",
		Namespace: "development", // Different namespace
		Config:    config,
	}

	err := ValidateImmutableFields(existing, new)

	require.Error(t, err)

	var targetErr model.TargetAlreadyExistsError
	require.ErrorAs(t, err, &targetErr)
	assert.Equal(t, "test-target", targetErr.TargetLabel)
	assert.Equal(t, "production", targetErr.ExistingNamespace)
	assert.Equal(t, "development", targetErr.FormaNamespace)
	assert.Equal(t, "namespace", targetErr.MismatchType)
}

func TestValidateImmutableFields_Error_ConfigMismatch(t *testing.T) {
	existing := &pkgmodel.Target{
		Label:     "test-target",
		Namespace: "production",
		Config:    json.RawMessage(`{"region": "us-east-1", "account": "123"}`),
	}

	new := &pkgmodel.Target{
		Label:     "test-target",
		Namespace: "production",
		Config:    json.RawMessage(`{"region": "us-west-2", "account": "456"}`), // Different config
	}

	err := ValidateImmutableFields(existing, new)

	require.Error(t, err)

	var targetErr model.TargetAlreadyExistsError
	require.ErrorAs(t, err, &targetErr)
	assert.Equal(t, "test-target", targetErr.TargetLabel)
	assert.Equal(t, json.RawMessage(`{"region": "us-east-1", "account": "123"}`), targetErr.ExistingConfig)
	assert.Equal(t, json.RawMessage(`{"region": "us-west-2", "account": "456"}`), targetErr.FormaConfig)
	assert.Equal(t, "config", targetErr.MismatchType)
}

func TestValidateImmutableFields_Error_ConfigMismatch_EmptyVsNonEmpty(t *testing.T) {
	existing := &pkgmodel.Target{
		Label:     "test-target",
		Namespace: "production",
		Config:    json.RawMessage(`{}`),
	}

	new := &pkgmodel.Target{
		Label:     "test-target",
		Namespace: "production",
		Config:    json.RawMessage(`{"region": "us-east-1"}`),
	}

	err := ValidateImmutableFields(existing, new)

	require.Error(t, err)

	var targetErr model.TargetAlreadyExistsError
	require.ErrorAs(t, err, &targetErr)
	assert.Equal(t, "config", targetErr.MismatchType)
}

func TestValidateImmutableFields_Error_ConfigMismatch_NilVsNonNil(t *testing.T) {
	existing := &pkgmodel.Target{
		Label:     "test-target",
		Namespace: "production",
		Config:    nil,
	}

	new := &pkgmodel.Target{
		Label:     "test-target",
		Namespace: "production",
		Config:    json.RawMessage(`{"region": "us-east-1"}`),
	}

	err := ValidateImmutableFields(existing, new)

	require.Error(t, err)

	var targetErr model.TargetAlreadyExistsError
	require.ErrorAs(t, err, &targetErr)
	assert.Equal(t, "config", targetErr.MismatchType)
}

func TestValidateImmutableFields_Success_NilVsEmptyObject(t *testing.T) {
	existing := &pkgmodel.Target{
		Label:     "test-target",
		Namespace: "production",
		Config:    nil,
	}

	new := &pkgmodel.Target{
		Label:     "test-target",
		Namespace: "production",
		Config:    json.RawMessage(`{}`),
	}

	err := ValidateImmutableFields(existing, new)
	assert.NoError(t, err) // nil and {} should be treated as equivalent
}

func TestValidateImmutableFields_Success_EmptyObjectVsNil(t *testing.T) {
	existing := &pkgmodel.Target{
		Label:     "test-target",
		Namespace: "production",
		Config:    json.RawMessage(`{}`),
	}

	new := &pkgmodel.Target{
		Label:     "test-target",
		Namespace: "production",
		Config:    nil,
	}

	err := ValidateImmutableFields(existing, new)
	assert.NoError(t, err) // {} and nil should be treated as equivalent
}

func TestShouldTriggerDiscovery_Create_Discoverable(t *testing.T) {
	update := TargetUpdate{
		Target: pkgmodel.Target{
			Label:        "test",
			Discoverable: true,
		},
		Operation: TargetOperationCreate,
	}

	assert.True(t, ShouldTriggerDiscovery(&update))
}

func TestShouldTriggerDiscovery_Create_NotDiscoverable(t *testing.T) {
	update := TargetUpdate{
		Target: pkgmodel.Target{
			Label:        "test",
			Discoverable: false,
		},
		Operation: TargetOperationCreate,
	}

	assert.False(t, ShouldTriggerDiscovery(&update))
}

func TestShouldTriggerDiscovery_Update_BecomesDiscoverable(t *testing.T) {
	update := TargetUpdate{
		Target: pkgmodel.Target{
			Label:        "test",
			Discoverable: true,
		},
		ExistingTarget: &pkgmodel.Target{
			Label:        "test",
			Discoverable: false,
		},
		Operation: TargetOperationUpdate,
	}

	assert.True(t, ShouldTriggerDiscovery(&update))
}

func TestShouldTriggerDiscovery_Update_AlreadyDiscoverable(t *testing.T) {
	update := TargetUpdate{
		Target: pkgmodel.Target{
			Label:        "test",
			Discoverable: true,
		},
		ExistingTarget: &pkgmodel.Target{
			Label:        "test",
			Discoverable: true,
		},
		Operation: TargetOperationUpdate,
	}

	assert.False(t, ShouldTriggerDiscovery(&update))
}

func TestShouldTriggerDiscovery_Update_BecomesNotDiscoverable(t *testing.T) {
	update := TargetUpdate{
		Target: pkgmodel.Target{
			Label:        "test",
			Discoverable: false,
		},
		ExistingTarget: &pkgmodel.Target{
			Label:        "test",
			Discoverable: true,
		},
		Operation: TargetOperationUpdate,
	}

	assert.False(t, ShouldTriggerDiscovery(&update))
}

func TestShouldTriggerDiscovery_Update_StaysNotDiscoverable(t *testing.T) {
	update := TargetUpdate{
		Target: pkgmodel.Target{
			Label:        "test",
			Discoverable: false,
		},
		ExistingTarget: &pkgmodel.Target{
			Label:        "test",
			Discoverable: false,
		},
		Operation: TargetOperationUpdate,
	}

	assert.False(t, ShouldTriggerDiscovery(&update))
}

func TestShouldTriggerDiscovery_Update_NilExistingTarget(t *testing.T) {
	update := TargetUpdate{
		Target: pkgmodel.Target{
			Label:        "test",
			Discoverable: true,
		},
		ExistingTarget: nil,
		Operation:      TargetOperationUpdate,
	}

	assert.True(t, ShouldTriggerDiscovery(&update))
}
