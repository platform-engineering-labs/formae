// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"fmt"
	"io"
	"os"
	"sync"
	"testing"
	"time"
)

// StepStatus represents the outcome of a single test phase.
type StepStatus int

const (
	StepNotRun  StepStatus = iota
	StepPassed
	StepFailed
	StepSkipped
)

// CRUDPhase enumerates the phases of a CRUD lifecycle test.
type CRUDPhase int

const (
	PhaseCreate  CRUDPhase = iota
	PhaseVerify
	PhaseExtract
	PhaseSync
	PhaseUpdate
	PhaseReplace
	PhaseDestroy
	PhaseOOBDelete
)

// crudPhaseCount is the total number of CRUD phases.
const crudPhaseCount = 8

// String returns the human-readable name of a CRUDPhase.
func (p CRUDPhase) String() string {
	switch p {
	case PhaseCreate:
		return "Create"
	case PhaseVerify:
		return "Verify"
	case PhaseExtract:
		return "Extract"
	case PhaseSync:
		return "Sync"
	case PhaseUpdate:
		return "Update"
	case PhaseReplace:
		return "Replace"
	case PhaseDestroy:
		return "Destroy"
	case PhaseOOBDelete:
		return "OOB Del"
	default:
		return "Unknown"
	}
}

// DiscoveryPhase enumerates the phases of a discovery lifecycle test.
type DiscoveryPhase int

const (
	PhaseCreateOOB      DiscoveryPhase = iota
	PhaseRegister
	PhaseDiscover
	PhaseDiscoveryVerify
)

// discoveryPhaseCount is the total number of discovery phases.
const discoveryPhaseCount = 4

// String returns the human-readable name of a DiscoveryPhase.
func (p DiscoveryPhase) String() string {
	switch p {
	case PhaseCreateOOB:
		return "CreateOOB"
	case PhaseRegister:
		return "Register"
	case PhaseDiscover:
		return "Discover"
	case PhaseDiscoveryVerify:
		return "Verify"
	default:
		return "Unknown"
	}
}

// PhaseError records an error that occurred during a specific phase.
type PhaseError struct {
	Phase string
	Msg   string
}

// TestResult holds the outcome of a single resource's test lifecycle.
type TestResult struct {
	Name     string
	Phases   map[int]StepStatus
	Errors   []PhaseError
	Duration time.Duration
}

// ResultCollector accumulates test results across resources in a thread-safe manner.
type ResultCollector struct {
	mu               sync.Mutex
	crudResults      []TestResult
	discoveryResults []TestResult
}

// NewResultCollector creates a new, empty ResultCollector.
func NewResultCollector() *ResultCollector {
	return &ResultCollector{}
}

// NewCRUDResult registers a new CRUD test resource and returns its index.
func (rc *ResultCollector) NewCRUDResult(name string) int {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	phases := make(map[int]StepStatus, crudPhaseCount)
	for i := 0; i < crudPhaseCount; i++ {
		phases[i] = StepNotRun
	}
	rc.crudResults = append(rc.crudResults, TestResult{
		Name:   name,
		Phases: phases,
	})
	return len(rc.crudResults) - 1
}

// NewDiscoveryResult registers a new discovery test resource and returns its index.
func (rc *ResultCollector) NewDiscoveryResult(name string) int {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	phases := make(map[int]StepStatus, discoveryPhaseCount)
	for i := 0; i < discoveryPhaseCount; i++ {
		phases[i] = StepNotRun
	}
	rc.discoveryResults = append(rc.discoveryResults, TestResult{
		Name:   name,
		Phases: phases,
	})
	return len(rc.discoveryResults) - 1
}

// SetCRUDPhase sets the status of a specific CRUD phase for the given resource.
func (rc *ResultCollector) SetCRUDPhase(idx int, phase CRUDPhase, status StepStatus) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.crudResults[idx].Phases[int(phase)] = status
}

// SetDiscoveryPhase sets the status of a specific discovery phase for the given resource.
func (rc *ResultCollector) SetDiscoveryPhase(idx int, phase DiscoveryPhase, status StepStatus) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.discoveryResults[idx].Phases[int(phase)] = status
}

// AddCRUDError records an error for a specific CRUD phase.
func (rc *ResultCollector) AddCRUDError(idx int, phase CRUDPhase, msg string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.crudResults[idx].Errors = append(rc.crudResults[idx].Errors, PhaseError{
		Phase: phase.String(),
		Msg:   msg,
	})
}

// AddDiscoveryError records an error for a specific discovery phase.
func (rc *ResultCollector) AddDiscoveryError(idx int, phase DiscoveryPhase, msg string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.discoveryResults[idx].Errors = append(rc.discoveryResults[idx].Errors, PhaseError{
		Phase: phase.String(),
		Msg:   msg,
	})
}

// recordCRUDError formats and records an error for a CRUD phase, setting it to StepFailed.
func (rc *ResultCollector) recordCRUDError(idx int, phase CRUDPhase, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.crudResults[idx].Phases[int(phase)] = StepFailed
	rc.crudResults[idx].Errors = append(rc.crudResults[idx].Errors, PhaseError{
		Phase: phase.String(),
		Msg:   msg,
	})
}

// recordDiscoveryError formats and records an error for a discovery phase, setting it to StepFailed.
func (rc *ResultCollector) recordDiscoveryError(idx int, phase DiscoveryPhase, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.discoveryResults[idx].Phases[int(phase)] = StepFailed
	rc.discoveryResults[idx].Errors = append(rc.discoveryResults[idx].Errors, PhaseError{
		Phase: phase.String(),
		Msg:   msg,
	})
}

// CRUDFatalf records a CRUD phase error and calls t.Fatalf.
func (rc *ResultCollector) CRUDFatalf(t *testing.T, idx int, phase CRUDPhase, format string, args ...any) {
	rc.recordCRUDError(idx, phase, format, args...)
	t.Fatalf(format, args...)
}

// CRUDErrorf records a CRUD phase error and calls t.Errorf.
func (rc *ResultCollector) CRUDErrorf(t *testing.T, idx int, phase CRUDPhase, format string, args ...any) {
	rc.recordCRUDError(idx, phase, format, args...)
	t.Errorf(format, args...)
}

// DiscoveryFatalf records a discovery phase error and calls t.Fatalf.
func (rc *ResultCollector) DiscoveryFatalf(t *testing.T, idx int, phase DiscoveryPhase, format string, args ...any) {
	rc.recordDiscoveryError(idx, phase, format, args...)
	t.Fatalf(format, args...)
}

// DiscoveryErrorf records a discovery phase error and calls t.Errorf.
func (rc *ResultCollector) DiscoveryErrorf(t *testing.T, idx int, phase DiscoveryPhase, format string, args ...any) {
	rc.recordDiscoveryError(idx, phase, format, args...)
	t.Errorf(format, args...)
}

// SetCRUDDuration sets the total duration for a CRUD test resource.
func (rc *ResultCollector) SetCRUDDuration(idx int, d time.Duration) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.crudResults[idx].Duration = d
}

// SetDiscoveryDuration sets the total duration for a discovery test resource.
func (rc *ResultCollector) SetDiscoveryDuration(idx int, d time.Duration) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.discoveryResults[idx].Duration = d
}

// MarkRemainingCRUDSkipped marks all StepNotRun phases as StepSkipped for the given CRUD resource.
func (rc *ResultCollector) MarkRemainingCRUDSkipped(idx int) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	for i := 0; i < crudPhaseCount; i++ {
		if rc.crudResults[idx].Phases[i] == StepNotRun {
			rc.crudResults[idx].Phases[i] = StepSkipped
		}
	}
}

// MarkRemainingDiscoverySkipped marks all StepNotRun phases as StepSkipped for the given discovery resource.
func (rc *ResultCollector) MarkRemainingDiscoverySkipped(idx int) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	for i := 0; i < discoveryPhaseCount; i++ {
		if rc.discoveryResults[idx].Phases[i] == StepNotRun {
			rc.discoveryResults[idx].Phases[i] = StepSkipped
		}
	}
}

// countResults returns resource-level counts for a slice of results.
// A resource is failed if any phase is StepFailed, skipped if ALL phases are StepSkipped,
// and passed otherwise. Does not acquire the mutex — caller must hold it.
func countResults(results []TestResult, phaseCount int) (passed, failed, skipped int) {
	for _, r := range results {
		hasFailed := false
		allSkipped := true
		for i := 0; i < phaseCount; i++ {
			if r.Phases[i] == StepFailed {
				hasFailed = true
			}
			if r.Phases[i] != StepSkipped {
				allSkipped = false
			}
		}
		switch {
		case hasFailed:
			failed++
		case allSkipped:
			skipped++
		default:
			passed++
		}
	}
	return
}

// crudCounts returns resource-level counts: passed, failed, skipped.
func (rc *ResultCollector) crudCounts() (passed, failed, skipped int) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return countResults(rc.crudResults, crudPhaseCount)
}

// discoveryCounts returns resource-level counts: passed, failed, skipped.
func (rc *ResultCollector) discoveryCounts() (passed, failed, skipped int) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return countResults(rc.discoveryResults, discoveryPhaseCount)
}

// statusMarker returns the display string for a StepStatus.
func statusMarker(s StepStatus) string {
	switch s {
	case StepPassed:
		return "[+]"
	case StepFailed:
		return "[X]"
	case StepSkipped:
		return "[~]"
	default:
		return "[-]"
	}
}

// renderSummary writes the full conformance test result matrix to w.
func (rc *ResultCollector) renderSummary(w io.Writer) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	banner := "================================================================================"
	fmt.Fprintf(w, "%s\n", banner)
	fmt.Fprintf(w, "%s\n", "                        CONFORMANCE TEST RESULTS")
	fmt.Fprintf(w, "%s\n\n", banner)

	// CRUD section
	if len(rc.crudResults) > 0 {
		crudHeaders := []string{"Create", "Verify", "Extract", "Sync", "Update", "Replace", "Destroy", "OOB Del"}

		nameWidth := 30
		for _, r := range rc.crudResults {
			if len(r.Name) > nameWidth {
				nameWidth = len(r.Name)
			}
		}

		fmt.Fprintf(w, "CRUD Tests:\n")
		fmt.Fprintf(w, "%-*s", nameWidth+2, "Resource")
		for _, h := range crudHeaders {
			fmt.Fprintf(w, "  %-8s", h)
		}
		fmt.Fprintf(w, "  Duration\n")

		for _, r := range rc.crudResults {
			fmt.Fprintf(w, "%-*s", nameWidth+2, r.Name)
			for i := 0; i < crudPhaseCount; i++ {
				fmt.Fprintf(w, "  %-8s", statusMarker(r.Phases[i]))
			}
			fmt.Fprintf(w, "  %s\n", formatDuration(r.Duration))
		}
		fmt.Fprintln(w)
	}

	// Discovery section
	if len(rc.discoveryResults) > 0 {
		discHeaders := []string{"Create OOB", "Register", "Discover", "Verify"}

		nameWidth := 30
		for _, r := range rc.discoveryResults {
			if len(r.Name) > nameWidth {
				nameWidth = len(r.Name)
			}
		}

		fmt.Fprintf(w, "Discovery Tests:\n")
		fmt.Fprintf(w, "%-*s", nameWidth+2, "Resource")
		for _, h := range discHeaders {
			fmt.Fprintf(w, "  %-10s", h)
		}
		fmt.Fprintf(w, "  Duration\n")

		for _, r := range rc.discoveryResults {
			fmt.Fprintf(w, "%-*s", nameWidth+2, r.Name)
			for i := 0; i < discoveryPhaseCount; i++ {
				fmt.Fprintf(w, "  %-10s", statusMarker(r.Phases[i]))
			}
			fmt.Fprintf(w, "  %s\n", formatDuration(r.Duration))
		}
		fmt.Fprintln(w)
	}

	// Legend
	fmt.Fprintf(w, "Legend: [+] passed  [X] FAILED  [-] not run  [~] skipped\n\n")

	// Failure details
	allResults := make([]TestResult, 0, len(rc.crudResults)+len(rc.discoveryResults))
	allResults = append(allResults, rc.crudResults...)
	allResults = append(allResults, rc.discoveryResults...)

	hasErrors := false
	for _, r := range allResults {
		if len(r.Errors) > 0 {
			hasErrors = true
			break
		}
	}

	if hasErrors {
		fmt.Fprintf(w, "Failure Details:\n")
		for _, r := range allResults {
			if len(r.Errors) == 0 {
				continue
			}
			fmt.Fprintf(w, "  %s\n", r.Name)
			for _, e := range r.Errors {
				fmt.Fprintf(w, "    [%s] %s\n", e.Phase, e.Msg)
			}
		}
		fmt.Fprintln(w)
	}

	// Summary line — count CRUD and discovery results separately to avoid
	// needing to infer the phase count from the Phases map length.
	var totalDuration time.Duration
	for _, r := range allResults {
		totalDuration += r.Duration
	}
	cp, cf, cs := countResults(rc.crudResults, crudPhaseCount)
	dp, df, ds := countResults(rc.discoveryResults, discoveryPhaseCount)
	totalPassed := cp + dp
	totalFailed := cf + df
	totalSkipped := cs + ds

	mins := int(totalDuration.Minutes())
	secs := int(totalDuration.Seconds()) % 60
	fmt.Fprintf(w, "%d passed, %d failed, %d skipped (%dm %02ds)\n", totalPassed, totalFailed, totalSkipped, mins, secs)
	fmt.Fprintf(w, "%s\n", banner)
}

// formatDuration formats a duration as "Xm XXs".
func formatDuration(d time.Duration) string {
	mins := int(d.Minutes())
	secs := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm %02ds", mins, secs)
}

// PrintSummary writes the conformance test result matrix to stderr.
func (rc *ResultCollector) PrintSummary() {
	rc.renderSummary(os.Stderr)
}

// compareCRUDProperties runs compareProperties with a collectingReporter to capture
// individual property errors, then records them in the result collector.
// On failure, sets the phase to StepFailed and records each error.
// On success, does NOT set StepPassed — the caller must do that, because other
// checks in the same phase (e.g. type/NativeID validation) may have already set StepFailed.
func (rc *ResultCollector) compareCRUDProperties(t *testing.T, idx int, phase CRUDPhase, expected map[string]any, actual map[string]any, context string, providerDefaults map[string]providerDefault) bool {
	cr := &collectingReporter{t: t}
	if !compareProperties(cr, expected, actual, context, providerDefaults) {
		rc.SetCRUDPhase(idx, phase, StepFailed)
		for _, msg := range cr.errors {
			rc.AddCRUDError(idx, phase, msg)
		}
		return false
	}
	return true
}
