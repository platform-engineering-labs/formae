// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build blackbox

package blackbox

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
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
		assert.Equal(t, forma_command.CommandStateSuccess, cmd.State)

		t.Logf("Apply command %s completed with state: %s", commandID, cmd.State)
	})
}