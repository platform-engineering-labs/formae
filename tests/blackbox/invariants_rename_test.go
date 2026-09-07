// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"testing"

	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/tests/testcontrol"
)

// RFC-0041: CheckInvariants must flag duplicate inventory rows for the same
// NativeID. This is the symptom of a rename that minted a fresh KSUID
// instead of preserving the existing one — the resource ends up under two
// labels in the latest visible view. Property/integration runs catch the
// regression even if no explicit OpRename exists yet, because any code
// path that produces the bad state will fail this invariant.
func TestCheckInvariants_FlagsDuplicateNativeID(t *testing.T) {
	cloudState := map[string]testcontrol.CloudStateEntry{
		"i-0abc1234": {ResourceType: "AWS::EC2::Instance"},
	}

	inventory := []pkgmodel.Resource{
		{Ksuid: "K_A", NativeID: "i-0abc1234", Label: "web-server", Type: "AWS::EC2::Instance"},
		{Ksuid: "K_B", NativeID: "i-0abc1234", Label: "new-vpc", Type: "AWS::EC2::Instance"},
	}

	violations := CheckInvariants(inventory, cloudState, nil)

	var foundDup bool
	for _, v := range violations {
		if v.Kind == ViolationDuplicateNativeID {
			foundDup = true
			break
		}
	}
	if !foundDup {
		t.Fatalf("expected ViolationDuplicateNativeID, got: %+v", violations)
	}
}

// Sanity: a single inventory row per NativeID produces no duplicate violation.
func TestCheckInvariants_SingleRowPerNativeID_OK(t *testing.T) {
	cloudState := map[string]testcontrol.CloudStateEntry{
		"i-0abc1234": {ResourceType: "AWS::EC2::Instance"},
	}

	inventory := []pkgmodel.Resource{
		{Ksuid: "K_A", NativeID: "i-0abc1234", Label: "app-server", Type: "AWS::EC2::Instance"},
	}

	violations := CheckInvariants(inventory, cloudState, nil)

	for _, v := range violations {
		if v.Kind == ViolationDuplicateNativeID {
			t.Fatalf("unexpected duplicate-NativeID violation: %s", v.Message)
		}
	}
}

// After a rename, a managed inventory row at the slot's previous label is a
// violation only when it belongs to that slot's lineage (same NativeID or
// KSUID). A different slot legitimately renamed onto the freed label must
// not be flagged.
func TestCheckRenameInvariants_OldLabelIdentityGuard(t *testing.T) {
	model := NewStateModel(1, 3)
	model.ApplyCreated(0, []int{1}, "")
	model.SetNativeID(0, 1, "test-42")
	model.SetKsuid(0, 1, "K_B")
	model.RecordRename(0, 1, "renamed-0")
	model.RecordRename(0, 1, "renamed-1") // PreviousLabel is now "renamed-0"

	// Another lineage's row occupies the freed label: legal, not flagged.
	reused := []pkgmodel.Resource{
		{Stack: "stack-0", Type: "Test::Generic::Resource", Label: "renamed-0", NativeID: "test-77", Ksuid: "K_OTHER", Managed: true},
		{Stack: "stack-0", Type: "Test::Generic::Resource", Label: "renamed-1", NativeID: "test-42", Ksuid: "K_B", Managed: true},
	}
	require.Empty(t, CheckRenameInvariants(model, reused),
		"a different lineage on the freed label must not be flagged")

	// The slot's own lineage still on the old label: the rename leaked it.
	// Fires the old-label check and, because the tracked NativeID carries
	// the wrong label, the positive label-drift check as well.
	leaked := []pkgmodel.Resource{
		{Stack: "stack-0", Type: "Test::Generic::Resource", Label: "renamed-0", NativeID: "test-42", Ksuid: "K_B", Managed: true},
	}
	violations := CheckRenameInvariants(model, leaked)
	kinds := make(map[ViolationKind]bool, len(violations))
	for _, v := range violations {
		kinds[v.Kind] = true
	}
	require.True(t, kinds[ViolationRenameOldLabelStillPresent], "leaked old-label row must be flagged: %v", violations)
}

// For a tracked NativeID the inventory row must carry both the current
// label and the tracked KSUID — a fresh KSUID under the same NativeID means
// the rename minted a new resource identity.
func TestCheckRenameInvariants_KsuidStability(t *testing.T) {
	model := NewStateModel(1, 3)
	model.ApplyCreated(0, []int{1}, "")
	model.SetNativeID(0, 1, "test-42")
	model.SetKsuid(0, 1, "K_B")
	model.RecordRename(0, 1, "renamed-0")

	inventory := []pkgmodel.Resource{
		{Stack: "stack-0", Type: "Test::Generic::Resource", Label: "renamed-0", NativeID: "test-42", Ksuid: "K_FRESH", Managed: true},
	}
	violations := CheckRenameInvariants(model, inventory)
	require.Len(t, violations, 1)
	require.Equal(t, ViolationRenameIdentityChanged, violations[0].Kind)

	inventory[0].Ksuid = "K_B"
	require.Empty(t, CheckRenameInvariants(model, inventory))
}
