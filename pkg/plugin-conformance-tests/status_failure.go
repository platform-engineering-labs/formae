// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"fmt"
	"strings"

	"github.com/platform-engineering-labs/formae/pkg/api/model"
)

// commandFailureError builds the error for a command that reached a terminal
// failure state, quoting the reason each update failed.
//
// The status response already carries an ErrorMessage on every failed resource
// and target update. Reporting only the terminal state throws that away and
// leaves a red run saying nothing about what went wrong, which is the
// difference between a diagnosable and an undiagnosable conformance failure.
func commandFailureError(cmd model.Command) error {
	var lines []string

	for _, tu := range cmd.TargetUpdates {
		if tu.State != stateFailed {
			continue
		}
		lines = append(lines, fmt.Sprintf("  target %s (%s): %s",
			tu.TargetLabel, tu.Operation, reasonOrUnreported(tu.ErrorMessage)))
	}

	for _, ru := range cmd.ResourceUpdates {
		if ru.State != stateFailed {
			continue
		}
		lines = append(lines, fmt.Sprintf("  %s %s (%s): %s",
			ru.ResourceType, ru.ResourceLabel, ru.Operation, reasonOrUnreported(ru.ErrorMessage)))
	}

	if len(lines) == 0 {
		return fmt.Errorf("command reached terminal state: %s", cmd.State)
	}
	return fmt.Errorf("command reached terminal state: %s\n%s", cmd.State, strings.Join(lines, "\n"))
}

const stateFailed = "Failed"

// A failed update with no ErrorMessage still has to be named: the fact that it
// failed while reporting no reason is itself the finding.
func reasonOrUnreported(msg string) string {
	if strings.TrimSpace(msg) == "" {
		return "(no error reported)"
	}
	return msg
}
