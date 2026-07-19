// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package statuswatch implements the live status/watch TUI — the shared view
// run by `formae status command` and handed off to by apply, destroy and
// cancel for their watch phase (RFC-3, PLA-282).
package statuswatch

import (
	"time"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// health classifies a command for urgency sorting and row coloring. Lower
// values are more urgent and sort first.
type health int

const (
	healthRunningFailing health = iota
	healthRunning
	healthFinishedFailed
	healthFinishedOK
)

func mapUpdateState(s string) components.State {
	switch s {
	case apimodel.ResourceUpdateStateSuccess:
		return components.StateDone
	case apimodel.ResourceUpdateStateFailed, apimodel.ResourceUpdateStateRejected:
		return components.StateFailed
	case apimodel.ResourceUpdateStateInProgress:
		return components.StateInProgress
	case apimodel.ResourceUpdateStateCanceled:
		return components.StateSkipped
	default: // Pending, NotStarted, Unknown
		return components.StatePending
	}
}

func isTerminalCommand(state string) bool {
	switch state {
	case "Success", "Failed", "Canceled":
		return true
	}
	return false
}

func commandCounts(c apimodel.Command) map[components.State]int {
	counts := make(map[components.State]int)
	for _, u := range c.TargetUpdates {
		counts[mapUpdateState(u.State)]++
	}
	for _, u := range c.StackUpdates {
		counts[mapUpdateState(u.State)]++
	}
	for _, u := range c.PolicyUpdates {
		counts[mapUpdateState(u.State)]++
	}
	for _, u := range c.ResourceUpdates {
		counts[mapUpdateState(u.State)]++
	}
	return counts
}

func commandHealth(c apimodel.Command, counts map[components.State]int) health {
	failing := counts[components.StateFailed] > 0
	if !isTerminalCommand(c.State) {
		if failing {
			return healthRunningFailing
		}
		return healthRunning
	}
	if failing || c.State == "Failed" || c.State == "Canceled" {
		return healthFinishedFailed
	}
	return healthFinishedOK
}

// doneOf returns completed count and total. Skipped updates count as done,
// matching the ProgressBar component's segment allocation.
func doneOf(counts map[components.State]int) (done, total int) {
	for s, n := range counts {
		total += n
		if s == components.StateDone || s == components.StateSkipped {
			done += n
		}
	}
	return done, total
}

func progressFraction(counts map[components.State]int) float64 {
	done, total := doneOf(counts)
	if total == 0 {
		return 0
	}
	return float64(done) / float64(total)
}

func commandDuration(c apimodel.Command, now time.Time) time.Duration {
	if c.StartTs.IsZero() {
		return 0
	}
	if isTerminalCommand(c.State) && !c.EndTs.IsZero() {
		return c.EndTs.Sub(c.StartTs)
	}
	return now.Sub(c.StartTs)
}

// filterUserCommands drops internal agent commands (sync, and anything whose
// recorded source is not the user) — users cannot obtain their IDs and they
// are bookkeeping noise in a status list. Old rows predate Source and pass
// through unless they are syncs.
func filterUserCommands(cmds []apimodel.Command) []apimodel.Command {
	out := make([]apimodel.Command, 0, len(cmds))
	for _, c := range cmds {
		if c.Command == "sync" {
			continue
		}
		if c.Source != "" && c.Source != "user" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// stateLabel returns a cancel-flow display override for an update's state:
// in-progress updates of a canceling command are "finishing" (they complete
// before the command stops), canceled updates are "canceled" (they will
// never run). Empty string means render the mapped state normally.
func stateLabel(cmdState, updState string) string {
	// Canceled/skipped is already conveyed by the ⊘ glyph — no redundant text
	// label. Only genuinely additive states get a label ("finishing" here;
	// "Abandoned" is set separately for force-canceled rows).
	if cmdState == "Canceling" && updState == apimodel.ResourceUpdateStateInProgress {
		return "finishing"
	}
	return ""
}
