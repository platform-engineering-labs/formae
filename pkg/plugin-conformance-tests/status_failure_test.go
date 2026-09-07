// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"strings"
	"testing"

	"github.com/platform-engineering-labs/formae/pkg/api/model"
)

// A failed command carries the reason each resource update failed. The error
// must name the failing resource and quote its reason, so a red conformance
// run says why it went red rather than only that it did.
func TestCommandFailureError_QuotesTheResourceError(t *testing.T) {
	cmd := model.Command{
		State: "Failed",
		ResourceUpdates: []model.ResourceUpdate{
			{
				ResourceType:  "PAGERDUTY::User",
				ResourceLabel: "formae-conformance-user",
				Operation:     "create",
				State:         "Failed",
				ErrorMessage:  "POST /users: 401 Unauthorized",
			},
		},
	}

	got := commandFailureError(cmd).Error()

	for _, want := range []string{
		"command reached terminal state: Failed",
		"PAGERDUTY::User",
		"formae-conformance-user",
		"create",
		"POST /users: 401 Unauthorized",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("error missing %q\ngot: %s", want, got)
		}
	}
}

// Successful updates in the same command are noise when diagnosing a failure.
func TestCommandFailureError_OmitsSucceededUpdates(t *testing.T) {
	cmd := model.Command{
		State: "Failed",
		ResourceUpdates: []model.ResourceUpdate{
			{ResourceType: "PAGERDUTY::Team", ResourceLabel: "team-a", Operation: "create", State: "Success"},
			{ResourceType: "PAGERDUTY::User", ResourceLabel: "user-a", Operation: "create", State: "Failed", ErrorMessage: "boom"},
		},
	}

	got := commandFailureError(cmd).Error()

	if strings.Contains(got, "team-a") {
		t.Errorf("error should not mention the succeeded update\ngot: %s", got)
	}
	if !strings.Contains(got, "boom") {
		t.Errorf("error missing the failure reason\ngot: %s", got)
	}
}

// A failed update whose ErrorMessage the agent never populated must still be
// named: that it failed while reporting no reason is itself the finding.
func TestCommandFailureError_NamesFailedUpdateWithoutAnErrorMessage(t *testing.T) {
	cmd := model.Command{
		State: "Failed",
		ResourceUpdates: []model.ResourceUpdate{
			{ResourceType: "PAGERDUTY::User", ResourceLabel: "user-a", Operation: "create", State: "Failed"},
		},
	}

	got := commandFailureError(cmd).Error()

	if !strings.Contains(got, "user-a") {
		t.Errorf("error missing the failed resource\ngot: %s", got)
	}
}

// Target updates fail too, and a target failure explains why every resource on
// it never got off the ground.
func TestCommandFailureError_QuotesTheTargetError(t *testing.T) {
	cmd := model.Command{
		State: "Failed",
		TargetUpdates: []model.TargetUpdate{
			{TargetLabel: "pagerduty-sandbox", Operation: "create", State: "Failed", ErrorMessage: "resolve config: no such secret"},
		},
	}

	got := commandFailureError(cmd).Error()

	for _, want := range []string{"pagerduty-sandbox", "resolve config: no such secret"} {
		if !strings.Contains(got, want) {
			t.Errorf("error missing %q\ngot: %s", want, got)
		}
	}
}

// With no failed update to report, the error stays exactly the terminal-state
// message rather than gaining a dangling, detail-free suffix.
func TestCommandFailureError_TerminalStateOnlyWhenNothingFailed(t *testing.T) {
	cmd := model.Command{
		State: "Canceled",
		ResourceUpdates: []model.ResourceUpdate{
			{ResourceType: "PAGERDUTY::Team", ResourceLabel: "team-a", Operation: "create", State: "Success"},
		},
	}

	if got := commandFailureError(cmd).Error(); got != "command reached terminal state: Canceled" {
		t.Errorf("want the bare terminal-state message, got: %s", got)
	}
}
