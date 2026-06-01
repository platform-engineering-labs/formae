// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"testing"

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

	violations := CheckInvariants(inventory, cloudState, nil, nil)

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

	violations := CheckInvariants(inventory, cloudState, nil, nil)

	for _, v := range violations {
		if v.Kind == ViolationDuplicateNativeID {
			t.Fatalf("unexpected duplicate-NativeID violation: %s", v.Message)
		}
	}
}
