// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import "testing"

// A suite that never ran is not a suite that passed. When setup fails before
// the first phase (an unresolvable Pkl project, a plugin that will not start),
// every phase stays NotRun, and counting that as a pass makes the summary
// report success for a run that tested nothing.
func TestCountResults_ASuiteThatNeverRanIsNotAPass(t *testing.T) {
	results := []TestResult{{
		Name:   "PLUGIN::thing",
		Phases: map[int]StepStatus{0: StepNotRun, 1: StepNotRun, 2: StepNotRun},
	}}

	passed, failed, skipped := countResults(results, 3)

	if passed != 0 {
		t.Errorf("a suite with no phase run must not count as passed, got passed=%d", passed)
	}
	if failed+skipped != 1 {
		t.Errorf("the suite must be accounted for as failed or skipped, got failed=%d skipped=%d", failed, skipped)
	}
}

// A genuine pass is still a pass.
func TestCountResults_AllPhasesPassedCountsAsPassed(t *testing.T) {
	results := []TestResult{{
		Name:   "PLUGIN::thing",
		Phases: map[int]StepStatus{0: StepPassed, 1: StepPassed, 2: StepPassed},
	}}

	if passed, failed, skipped := countResults(results, 3); passed != 1 || failed != 0 || skipped != 0 {
		t.Errorf("want passed=1 failed=0 skipped=0, got passed=%d failed=%d skipped=%d", passed, failed, skipped)
	}
}

// A suite that got partway and then failed is a failure, not a partial pass.
func TestCountResults_AFailedPhaseCountsAsFailed(t *testing.T) {
	results := []TestResult{{
		Name:   "PLUGIN::thing",
		Phases: map[int]StepStatus{0: StepPassed, 1: StepFailed, 2: StepNotRun},
	}}

	if passed, failed, _ := countResults(results, 3); failed != 1 || passed != 0 {
		t.Errorf("want failed=1 passed=0, got passed=%d failed=%d", passed, failed)
	}
}

// A deliberately skipped suite stays skipped, distinct from one that never ran.
func TestCountResults_AllPhasesSkippedCountsAsSkipped(t *testing.T) {
	results := []TestResult{{
		Name:   "PLUGIN::thing",
		Phases: map[int]StepStatus{0: StepSkipped, 1: StepSkipped, 2: StepSkipped},
	}}

	if passed, failed, skipped := countResults(results, 3); skipped != 1 || passed != 0 || failed != 0 {
		t.Errorf("want skipped=1, got passed=%d failed=%d skipped=%d", passed, failed, skipped)
	}
}

// A suite that ran and passed the phases it exercised still counts as passed
// even though later phases were never reached. Suites legitimately exercise a
// subset, so only a suite where nothing ran at all is treated as a failure.
func TestCountResults_SuiteThatRanAPhaseStillCountsAsPassed(t *testing.T) {
	results := []TestResult{{
		Name:   "PLUGIN::thing",
		Phases: map[int]StepStatus{0: StepPassed, 1: StepNotRun, 2: StepNotRun},
	}}

	if passed, failed, _ := countResults(results, 3); passed != 1 || failed != 0 {
		t.Errorf("want passed=1 failed=0, got passed=%d failed=%d", passed, failed)
	}
}
