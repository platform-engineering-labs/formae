// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package target_update

import (
	"testing"

	"github.com/stretchr/testify/assert"

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
