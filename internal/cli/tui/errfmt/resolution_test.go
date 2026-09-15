//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package errfmt

import (
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRecordedDriftErrorGuidance(t *testing.T) {
	text, err := Render(&apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]{Data: apimodel.FormaReconcileRejectedError{ObservationID: "obs", ModifiedStacks: map[string]apimodel.ModifiedStack{"prod": {ModifiedResources: []apimodel.ResourceModification{{ResourceID: "r", Label: "bad\x1b[2J", Operation: "delete"}}}}}})
	require.NoError(t, err)
	require.Contains(t, text, "--resolution")
	require.Contains(t, text, "--output-consumer machine")
	require.NotContains(t, text, "--force")
	require.NotContains(t, text, "\x1b[2J")
}
func TestFailedCreateDiagnosticKeepsIdentityAndRecovery(t *testing.T) {
	text, err := Render(&apimodel.ErrorResponse[apimodel.DriftResolutionError]{Data: apimodel.DriftResolutionError{Code: "desired-intent-unavailable", Reason: "cannot omit desired resource", ResourceID: "resource", CommandID: "failed-command"}})
	require.NoError(t, err)
	for _, want := range []string{"cannot omit desired resource", "resource", "failed-command", "recover or reapply", "desired extraction"} {
		require.Contains(t, text, want)
	}
}
