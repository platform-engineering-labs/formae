// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"testing"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestAcceptanceRecoveryRemainsSuccessfulWithoutProviderProgress(t *testing.T) {
	for _, op := range []OperationType{OperationAccept, OperationAcceptDelete, OperationWithdraw} {
		t.Run(string(op), func(t *testing.T) {
			ru := ResourceUpdate{Operation: op, State: ResourceUpdateStateSuccess, Version: "reviewed"}
			ru.UpdateState()
			if ru.State != ResourceUpdateStateSuccess {
				t.Fatalf("recovered acceptance state = %s", ru.State)
			}
			if len(ru.requiredOperations()) != 0 {
				t.Fatal("acceptance requires provider operations")
			}
			if ru.Version != "reviewed" {
				t.Fatal("recovery changed reviewed observation")
			}
		})
	}
}

func TestAcceptanceDoesNotSynthesizeTargetResolution(t *testing.T) {
	updates := []ResourceUpdate{{Operation: OperationAccept, DesiredState: pkgmodel.Resource{Target: "accepted-only"}}, {Operation: OperationUpdate, DesiredState: pkgmodel.Resource{Target: "written"}}}
	got := ReferencedTargetLabels(updates)
	if len(got) != 1 || got[0] != "written" {
		t.Fatalf("target resolution includes acceptance: %v", got)
	}
}

func TestAcceptanceDoesNotDrawGenerators(t *testing.T) {
	updates := []ResourceUpdate{{Operation: OperationAccept, DesiredState: pkgmodel.Resource{Properties: []byte(`{"password":{"$gen":true,"$generator":"secret","$output":"value","$visibility":"Opaque"}}`)}}}
	if got := GeneratorsNeedingDraw(updates); len(got) != 0 {
		t.Fatalf("acceptance requests generator draws: %v", got)
	}
}
