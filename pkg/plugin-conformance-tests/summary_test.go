// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestResultCollector_NewCRUDResult(t *testing.T) {
	rc := NewResultCollector()
	idx0 := rc.NewCRUDResult("K8S::Core::Namespace")
	idx1 := rc.NewCRUDResult("K8S::Core::ConfigMap")

	if idx0 != 0 {
		t.Errorf("first index should be 0, got %d", idx0)
	}
	if idx1 != 1 {
		t.Errorf("second index should be 1, got %d", idx1)
	}
}

func TestResultCollector_SetCRUDPhase(t *testing.T) {
	rc := NewResultCollector()
	idx := rc.NewCRUDResult("K8S::Core::Namespace")

	rc.SetCRUDPhase(idx, PhaseCreate, StepPassed)
	rc.SetCRUDPhase(idx, PhaseVerify, StepFailed)

	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.crudResults[idx].Phases[int(PhaseCreate)] != StepPassed {
		t.Error("Create phase should be StepPassed")
	}
	if rc.crudResults[idx].Phases[int(PhaseVerify)] != StepFailed {
		t.Error("Verify phase should be StepFailed")
	}
}

func TestResultCollector_AddCRUDError(t *testing.T) {
	rc := NewResultCollector()
	idx := rc.NewCRUDResult("K8S::Core::Service")

	rc.AddCRUDError(idx, PhaseVerify, "Property spec.clusterIP not expected")
	rc.AddCRUDError(idx, PhaseExtract, "Property spec.clusterIP not expected")

	rc.mu.Lock()
	defer rc.mu.Unlock()
	if len(rc.crudResults[idx].Errors) != 2 {
		t.Errorf("expected 2 errors, got %d", len(rc.crudResults[idx].Errors))
	}
	if rc.crudResults[idx].Errors[0].Phase != "Verify" {
		t.Errorf("first error phase should be 'Verify', got %q", rc.crudResults[idx].Errors[0].Phase)
	}
}

func TestResultCollector_MarkRemainingCRUDSkipped(t *testing.T) {
	rc := NewResultCollector()
	idx := rc.NewCRUDResult("K8S::Core::Namespace")

	rc.SetCRUDPhase(idx, PhaseCreate, StepPassed)
	rc.SetCRUDPhase(idx, PhaseVerify, StepPassed)
	// Remaining phases are StepNotRun
	rc.MarkRemainingCRUDSkipped(idx)

	rc.mu.Lock()
	defer rc.mu.Unlock()
	// Create and Verify should still be Passed
	if rc.crudResults[idx].Phases[int(PhaseCreate)] != StepPassed {
		t.Error("Create should remain StepPassed")
	}
	if rc.crudResults[idx].Phases[int(PhaseVerify)] != StepPassed {
		t.Error("Verify should remain StepPassed")
	}
	// All others should be Skipped
	for _, phase := range []CRUDPhase{PhaseExtract, PhaseSync, PhaseUpdate, PhaseReplace, PhaseDestroy, PhaseOOBDelete} {
		if rc.crudResults[idx].Phases[int(phase)] != StepSkipped {
			t.Errorf("phase %d should be StepSkipped", phase)
		}
	}
}

func TestResultCollector_Discovery(t *testing.T) {
	rc := NewResultCollector()
	idx := rc.NewDiscoveryResult("K8S::Core::ConfigMap")

	rc.SetDiscoveryPhase(idx, PhaseCreateOOB, StepPassed)
	rc.SetDiscoveryPhase(idx, PhaseRegister, StepPassed)
	rc.SetDiscoveryPhase(idx, PhaseDiscover, StepPassed)
	rc.SetDiscoveryPhase(idx, PhaseDiscoveryVerify, StepPassed)
	rc.SetDiscoveryDuration(idx, 23*time.Second)

	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.discoveryResults[idx].Duration != 23*time.Second {
		t.Error("duration should be 23s")
	}
	if rc.discoveryResults[idx].Phases[int(PhaseDiscoveryVerify)] != StepPassed {
		t.Error("verify phase should be passed")
	}
}

func TestResultCollector_ResourceLevelCounts(t *testing.T) {
	rc := NewResultCollector()

	// Resource 1: all pass
	idx0 := rc.NewCRUDResult("Resource1")
	rc.SetCRUDPhase(idx0, PhaseCreate, StepPassed)
	rc.SetCRUDPhase(idx0, PhaseVerify, StepPassed)
	rc.SetCRUDPhase(idx0, PhaseDestroy, StepPassed)

	// Resource 2: one failure
	idx1 := rc.NewCRUDResult("Resource2")
	rc.SetCRUDPhase(idx1, PhaseCreate, StepPassed)
	rc.SetCRUDPhase(idx1, PhaseVerify, StepFailed)

	// Resource 3: all skipped
	idx2 := rc.NewCRUDResult("Resource3")
	rc.MarkRemainingCRUDSkipped(idx2)

	// Resource 4: partially run (fatal mid-test — Create passed, rest NotRun)
	idx3 := rc.NewCRUDResult("Resource4")
	rc.SetCRUDPhase(idx3, PhaseCreate, StepPassed)
	// Remaining phases stay StepNotRun (simulates t.Fatalf after Create)

	passed, failed, skipped := rc.crudCounts()
	if passed != 2 {
		// Resource1 (all pass) and Resource4 (partial, no failures) both count as passed
		t.Errorf("expected 2 passed, got %d", passed)
	}
	if failed != 1 {
		t.Errorf("expected 1 failed, got %d", failed)
	}
	if skipped != 1 {
		t.Errorf("expected 1 skipped, got %d", skipped)
	}
}

func TestResultCollector_RecordCRUDError(t *testing.T) {
	rc := NewResultCollector()
	idx := rc.NewCRUDResult("Test")

	rc.recordCRUDError(idx, PhaseVerify, "property %s missing", "name")

	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.crudResults[idx].Phases[int(PhaseVerify)] != StepFailed {
		t.Error("phase should be StepFailed after recordCRUDError")
	}
	if len(rc.crudResults[idx].Errors) != 1 {
		t.Fatal("expected 1 error")
	}
	if rc.crudResults[idx].Errors[0].Msg != "property name missing" {
		t.Errorf("unexpected error message: %s", rc.crudResults[idx].Errors[0].Msg)
	}
}

func TestResultCollector_PrintSummary(t *testing.T) {
	rc := NewResultCollector()

	// Set up a passing CRUD test
	idx0 := rc.NewCRUDResult("K8S::Core::Namespace")
	rc.SetCRUDPhase(idx0, PhaseCreate, StepPassed)
	rc.SetCRUDPhase(idx0, PhaseVerify, StepPassed)
	rc.SetCRUDPhase(idx0, PhaseExtract, StepPassed)
	rc.SetCRUDPhase(idx0, PhaseSync, StepPassed)
	rc.SetCRUDPhase(idx0, PhaseUpdate, StepSkipped)
	rc.SetCRUDPhase(idx0, PhaseReplace, StepSkipped)
	rc.SetCRUDPhase(idx0, PhaseDestroy, StepPassed)
	rc.SetCRUDPhase(idx0, PhaseOOBDelete, StepPassed)
	rc.SetCRUDDuration(idx0, 12*time.Second)

	// Set up a failing CRUD test
	idx1 := rc.NewCRUDResult("K8S::Core::Service")
	rc.SetCRUDPhase(idx1, PhaseCreate, StepPassed)
	rc.SetCRUDPhase(idx1, PhaseVerify, StepFailed)
	rc.AddCRUDError(idx1, PhaseVerify, "Property spec.clusterIP is not expected")
	rc.SetCRUDDuration(idx1, 34*time.Second)

	// Capture output by writing to a buffer
	var buf strings.Builder
	rc.renderSummary(&buf)
	output := buf.String()

	// Verify key elements are present
	if !strings.Contains(output, "CONFORMANCE TEST RESULTS") {
		t.Error("output should contain header")
	}
	if !strings.Contains(output, "K8S::Core::Namespace") {
		t.Error("output should contain test name")
	}
	if !strings.Contains(output, "[+]") {
		t.Error("output should contain pass marker")
	}
	if !strings.Contains(output, "[X]") {
		t.Error("output should contain fail marker")
	}
	if !strings.Contains(output, "[~]") {
		t.Error("output should contain skip marker")
	}
	if !strings.Contains(output, "Failure Details") {
		t.Error("output should contain failure details section")
	}
	if !strings.Contains(output, "spec.clusterIP") {
		t.Error("output should contain error message")
	}
	if !strings.Contains(output, "1 passed") {
		t.Error("output should show 1 passed")
	}
	if !strings.Contains(output, "1 failed") {
		t.Error("output should show 1 failed")
	}

	// Also add discovery results and verify both sections render
	didx := rc.NewDiscoveryResult("Plugin::Resource::TypeA")
	rc.SetDiscoveryPhase(didx, PhaseCreateOOB, StepPassed)
	rc.SetDiscoveryPhase(didx, PhaseRegister, StepPassed)
	rc.SetDiscoveryPhase(didx, PhaseDiscover, StepPassed)
	rc.SetDiscoveryPhase(didx, PhaseDiscoveryVerify, StepPassed)
	rc.SetDiscoveryDuration(didx, 5*time.Second)

	var buf2 strings.Builder
	rc.renderSummary(&buf2)
	output2 := buf2.String()

	if !strings.Contains(output2, "Discovery Tests:") {
		t.Error("output should contain discovery section")
	}
	if !strings.Contains(output2, "Plugin::Resource::TypeA") {
		t.Error("output should contain discovery resource name")
	}
	if !strings.Contains(output2, "Create OOB") {
		t.Error("output should contain discovery column headers")
	}
}

func TestResultCollector_CRUDErrorf(t *testing.T) {
	rc := NewResultCollector()
	idx := rc.NewCRUDResult("Test")

	// CRUDErrorf records the error in the collector AND calls t.Errorf.
	// We verify via recordCRUDError (the shared logic) since testing t.Errorf
	// would require a subtest that intentionally fails.
	rc.recordCRUDError(idx, PhaseVerify, "expected %d got %d", 42, 99)

	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.crudResults[idx].Phases[int(PhaseVerify)] != StepFailed {
		t.Error("phase should be StepFailed")
	}
	if len(rc.crudResults[idx].Errors) != 1 {
		t.Fatal("expected 1 error")
	}
	if rc.crudResults[idx].Errors[0].Msg != "expected 42 got 99" {
		t.Errorf("unexpected error message: %q", rc.crudResults[idx].Errors[0].Msg)
	}
}

func TestResultCollector_ConcurrentAccess(t *testing.T) {
	rc := NewResultCollector()
	const n = 50

	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			idx := rc.NewCRUDResult("Resource" + string(rune('A'+i%26)))
			rc.SetCRUDPhase(idx, PhaseCreate, StepPassed)
			rc.SetCRUDPhase(idx, PhaseVerify, StepFailed)
			rc.AddCRUDError(idx, PhaseVerify, fmt.Sprintf("error from goroutine %d", i))
			rc.SetCRUDDuration(idx, time.Duration(i)*time.Second)
		}(i)
	}
	wg.Wait()

	passed, failed, _ := rc.crudCounts()
	if passed+failed != n {
		t.Errorf("expected %d total results, got %d", n, passed+failed)
	}
	if failed != n {
		t.Errorf("expected %d failed, got %d", n, failed)
	}
}

func TestResultCollector_EmptyOutput(t *testing.T) {
	rc := NewResultCollector()
	var buf strings.Builder
	rc.renderSummary(&buf)
	output := buf.String()

	if !strings.Contains(output, "CONFORMANCE TEST RESULTS") {
		t.Error("empty collector should still print header")
	}
	if !strings.Contains(output, "0 passed, 0 failed, 0 skipped") {
		t.Error("empty collector should show zero counts")
	}
}
