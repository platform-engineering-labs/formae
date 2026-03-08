// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package blackbox

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/tests/testcontrol"
)

func TestSmoke_ApplyAndVerify(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 10*time.Second)
		defer h.Cleanup()

		// Apply a simple forma with one resource
		forma := SimpleForma(1)
		commandID := h.ApplyForma(forma, pkgmodel.FormaApplyModeReconcile)

		// Wait for command to complete
		cmd := h.WaitForCommandDone(commandID, 30*time.Second)
		require.NotNil(t, cmd)
		assert.Equal(t, "Success", cmd.State)

		t.Logf("Apply command %s completed with state: %s", commandID, cmd.State)
	})
}

func TestSmoke_TestControllerCommunication(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 10*time.Second)
		defer h.Cleanup()

		// Apply a forma with one resource
		forma := SimpleForma(1)
		commandID := h.ApplyForma(forma, pkgmodel.FormaApplyModeReconcile)
		cmd := h.WaitForCommandDone(commandID, 30*time.Second)
		require.NotNil(t, cmd)
		require.Equal(t, "Success", cmd.State)

		// Query cloud state via TestController (Ergo cross-node call)
		snapshot := h.GetCloudStateSnapshot(t)
		assert.Len(t, snapshot, 1, "should have one resource in cloud state after apply")

		// Query operation log via TestController
		opLog := h.GetOperationLog(t)
		assert.NotEmpty(t, opLog, "operation log should have entries after apply")

		// Verify at least one Create operation was recorded
		hasCreate := false
		for _, entry := range opLog {
			if entry.Operation == "Create" {
				hasCreate = true
				break
			}
		}
		assert.True(t, hasCreate, "operation log should contain a Create operation")

		// Program a response sequence and verify it takes effect
		h.ProgramResponses(t, []testcontrol.PluginOpSequence{
			{
				MatchKey:  "smoke-test",
				Operation: "Read",
				Steps:     []testcontrol.ResponseStep{{ErrorCode: "Throttling"}},
			},
		})

		// Clear response queue by programming empty sequences
		h.ProgramResponses(t, nil)
	})
}

