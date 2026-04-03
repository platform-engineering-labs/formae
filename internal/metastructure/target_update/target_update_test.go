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

func TestTargetUpdate_ResolvablesReturnsRemainingResolvables(t *testing.T) {
	tu := TargetUpdate{
		RemainingResolvables: []pkgmodel.FormaeURI{
			"formae://abc123#/Endpoint",
			"formae://def456#/CertArn",
		},
	}
	assert.Equal(t, tu.RemainingResolvables, tu.Resolvables())
}

func TestTargetUpdate_ResolvablesEmptyByDefault(t *testing.T) {
	tu := TargetUpdate{}
	assert.Empty(t, tu.Resolvables())
}

func TestTargetUpdate_ResolveValue(t *testing.T) {
	config := json.RawMessage(`{
		"endpoint": {"$ref": "formae://abc123#/Endpoint", "$strategy": "SetOnce", "$visibility": "Clear"},
		"region": "us-east-1"
	}`)

	tu := TargetUpdate{
		Target: pkgmodel.Target{
			Label:  "k8s",
			Config: config,
		},
	}

	err := tu.ResolveValue("formae://abc123#/Endpoint", "https://my-cluster.eks.amazonaws.com")
	require.NoError(t, err)

	assert.Contains(t, string(tu.Target.Config), "https://my-cluster.eks.amazonaws.com")
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

func TestShouldTriggerDiscovery_Update_AlreadyDiscoverable_NoConfigChange(t *testing.T) {
	// Schema-only or format-only update on an already-discoverable target
	// should NOT trigger discovery — nothing discovery cares about changed.
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

func TestShouldTriggerDiscovery_Update_AlreadyDiscoverable_ConfigChanged(t *testing.T) {
	// Mutable config change on an already-discoverable target SHOULD trigger
	// discovery — the config change may affect which resources are visible.
	update := TargetUpdate{
		Target: pkgmodel.Target{
			Label:        "test",
			Discoverable: true,
			Config:       json.RawMessage(`{"Profile":"staging"}`),
		},
		ExistingTarget: &pkgmodel.Target{
			Label:        "test",
			Discoverable: true,
			Config:       json.RawMessage(`{"Profile":"dev"}`),
		},
		Operation: TargetOperationUpdate,
	}

	assert.True(t, ShouldTriggerDiscovery(&update))
}

func TestShouldTriggerDiscovery_Update_AlreadyDiscoverable_RefFormatChange(t *testing.T) {
	// Format-only change ($ref wrapper vs plain value with same resolved value)
	// should NOT trigger discovery — the effective config didn't change.
	update := TargetUpdate{
		Target: pkgmodel.Target{
			Label:        "test",
			Discoverable: true,
			Config:       json.RawMessage(`{"endpoint":{"$ref":"formae://abc#/Endpoint","$value":"https://my-cluster"}}`),
		},
		ExistingTarget: &pkgmodel.Target{
			Label:        "test",
			Discoverable: true,
			Config:       json.RawMessage(`{"endpoint":"https://my-cluster"}`),
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
