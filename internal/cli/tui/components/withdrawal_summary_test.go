//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package components

import (
	"testing"

	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	"github.com/stretchr/testify/require"
)

func TestAcceptanceSummarySeparatesWithdrawalsFromDriftAcceptance(t *testing.T) {
	for _, tc := range []struct {
		name       string
		operations []string
		want       string
	}{
		{"withdrawal", []string{"withdraw"}, "Desired intent withdrawals: 1; provider resource operations: 0"},
		{"mixed", []string{"withdraw", "accept", "accept_delete", "delete"}, "Drift acceptance records: 2; desired intent withdrawals: 1; provider resource operations: 1"},
		{"acceptance", []string{"accept", "accept_delete", "create"}, "Drift acceptance records: 2; provider resource operations: 1"},
		{"provider", []string{"create"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := &apimodel.Command{}
			for _, operation := range tc.operations {
				command.ResourceUpdates = append(command.ResourceUpdates, apimodel.ResourceUpdate{Operation: operation})
			}
			require.Equal(t, tc.want, AcceptanceSummary(command))
		})
	}
}
