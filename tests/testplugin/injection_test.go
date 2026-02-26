// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"testing"
	"time"
)

func TestInjectionState_CheckError_NoRules(t *testing.T) {
	is := NewInjectionState()

	err := is.CheckError("Create", "Test::Generic::Resource")
	if err != nil {
		t.Errorf("expected nil error when no rules, got %v", err)
	}
}

func TestInjectionState_CheckError_MatchesOperation(t *testing.T) {
	is := NewInjectionState()

	is.AddErrorRule(ErrorRule{
		Operation: "Create",
		Error:     "simulated create failure",
		Count:     1,
	})

	err := is.CheckError("Create", "Test::Generic::Resource")
	if err == nil {
		t.Fatal("expected error for matching operation, got nil")
	}
	if err.Error() != "simulated create failure" {
		t.Errorf("error message: got %q, want %q", err.Error(), "simulated create failure")
	}
}

func TestInjectionState_CheckError_CountExhaustion(t *testing.T) {
	is := NewInjectionState()

	is.AddErrorRule(ErrorRule{
		Operation: "Read",
		Error:     "transient failure",
		Count:     1,
	})

	// First call should return the error
	err := is.CheckError("Read", "Test::Generic::Resource")
	if err == nil {
		t.Fatal("expected error on first call, got nil")
	}

	// Second call should return nil (count exhausted)
	err = is.CheckError("Read", "Test::Generic::Resource")
	if err != nil {
		t.Errorf("expected nil after count exhausted, got %v", err)
	}
}

func TestInjectionState_CheckError_PermanentError(t *testing.T) {
	is := NewInjectionState()

	is.AddErrorRule(ErrorRule{
		Operation: "Delete",
		Error:     "permanent failure",
		Count:     0, // 0 means fire every time
	})

	for i := 0; i < 5; i++ {
		err := is.CheckError("Delete", "Test::Generic::Resource")
		if err == nil {
			t.Fatalf("expected error on call %d, got nil", i+1)
		}
		if err.Error() != "permanent failure" {
			t.Errorf("call %d: error message: got %q, want %q", i+1, err.Error(), "permanent failure")
		}
	}
}

func TestInjectionState_CheckError_ResourceTypeFilter(t *testing.T) {
	is := NewInjectionState()

	// Rule with empty ResourceType matches all resource types
	is.AddErrorRule(ErrorRule{
		Operation:    "Create",
		Error:        "applies to all types",
		ResourceType: "",
		Count:        0,
	})

	err := is.CheckError("Create", "Test::Generic::Resource")
	if err == nil {
		t.Fatal("expected error for empty ResourceType filter (matches all), got nil")
	}
	err = is.CheckError("Create", "Test::Special::Resource")
	if err == nil {
		t.Fatal("expected error for empty ResourceType filter (matches all) on different type, got nil")
	}

	// Clear and add a rule for a specific resource type
	is.Clear()

	is.AddErrorRule(ErrorRule{
		Operation:    "Create",
		Error:        "only for generic",
		ResourceType: "Test::Generic::Resource",
		Count:        0,
	})

	err = is.CheckError("Create", "Test::Generic::Resource")
	if err == nil {
		t.Fatal("expected error for matching resource type, got nil")
	}

	err = is.CheckError("Create", "Test::Special::Resource")
	if err != nil {
		t.Errorf("expected nil for non-matching resource type, got %v", err)
	}
}

func TestInjectionState_CheckError_NoMatchForDifferentOperation(t *testing.T) {
	is := NewInjectionState()

	is.AddErrorRule(ErrorRule{
		Operation: "Create",
		Error:     "only for create",
		Count:     0,
	})

	err := is.CheckError("Read", "Test::Generic::Resource")
	if err != nil {
		t.Errorf("expected nil for non-matching operation, got %v", err)
	}
}

func TestInjectionState_CheckLatency_NoRules(t *testing.T) {
	is := NewInjectionState()

	d := is.CheckLatency("Create", "Test::Generic::Resource")
	if d != 0 {
		t.Errorf("expected 0 latency when no rules, got %v", d)
	}
}

func TestInjectionState_CheckLatency_MatchesOperation(t *testing.T) {
	is := NewInjectionState()

	is.AddLatencyRule(LatencyRule{
		Operation: "Read",
		Duration:  500 * time.Millisecond,
	})

	d := is.CheckLatency("Read", "Test::Generic::Resource")
	if d != 500*time.Millisecond {
		t.Errorf("expected 500ms latency, got %v", d)
	}
}

func TestInjectionState_CheckLatency_ResourceTypeFilter(t *testing.T) {
	is := NewInjectionState()

	is.AddLatencyRule(LatencyRule{
		Operation:    "Create",
		Duration:     200 * time.Millisecond,
		ResourceType: "Test::Generic::Resource",
	})

	d := is.CheckLatency("Create", "Test::Generic::Resource")
	if d != 200*time.Millisecond {
		t.Errorf("expected 200ms latency for matching type, got %v", d)
	}

	d = is.CheckLatency("Create", "Test::Special::Resource")
	if d != 0 {
		t.Errorf("expected 0 latency for non-matching type, got %v", d)
	}
}

func TestInjectionState_Clear(t *testing.T) {
	is := NewInjectionState()

	is.AddErrorRule(ErrorRule{
		Operation: "Create",
		Error:     "will be cleared",
		Count:     0,
	})
	is.AddLatencyRule(LatencyRule{
		Operation: "Read",
		Duration:  100 * time.Millisecond,
	})

	is.Clear()

	err := is.CheckError("Create", "Test::Generic::Resource")
	if err != nil {
		t.Errorf("expected nil error after Clear, got %v", err)
	}

	d := is.CheckLatency("Read", "Test::Generic::Resource")
	if d != 0 {
		t.Errorf("expected 0 latency after Clear, got %v", d)
	}
}