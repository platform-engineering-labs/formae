package main

import (
	"testing"
	"time"
)

func TestOperationLog_RecordAndSnapshot(t *testing.T) {
	ol := NewOperationLog()

	ol.Record(OperationLogEntry{
		Operation:    "Create",
		ResourceType: "Test::Generic::Resource",
		NativeID:     "test-1",
		Timestamp:    time.Now(),
	})
	ol.Record(OperationLogEntry{
		Operation:    "Read",
		ResourceType: "Test::Generic::Resource",
		NativeID:     "test-1",
		Timestamp:    time.Now(),
	})

	snap := ol.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 entries in snapshot, got %d", len(snap))
	}
	if snap[0].Operation != "Create" {
		t.Errorf("entry 0 Operation: got %q, want %q", snap[0].Operation, "Create")
	}
	if snap[0].NativeID != "test-1" {
		t.Errorf("entry 0 NativeID: got %q, want %q", snap[0].NativeID, "test-1")
	}
	if snap[1].Operation != "Read" {
		t.Errorf("entry 1 Operation: got %q, want %q", snap[1].Operation, "Read")
	}
}

func TestOperationLog_SnapshotIsIsolated(t *testing.T) {
	ol := NewOperationLog()

	ol.Record(OperationLogEntry{
		Operation:    "Create",
		ResourceType: "Test::Generic::Resource",
		NativeID:     "test-1",
		Timestamp:    time.Now(),
	})

	snap := ol.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 entry in snapshot, got %d", len(snap))
	}

	// Mutate the snapshot
	snap[0].Operation = "MUTATED"
	snap = append(snap, OperationLogEntry{
		Operation:    "Injected",
		ResourceType: "Fake",
		NativeID:     "fake-1",
		Timestamp:    time.Now(),
	})

	// Original should be unaffected
	snap2 := ol.Snapshot()
	if len(snap2) != 1 {
		t.Fatalf("expected 1 entry in original after snapshot mutation, got %d", len(snap2))
	}
	if snap2[0].Operation != "Create" {
		t.Errorf("original entry was mutated: got %q, want %q", snap2[0].Operation, "Create")
	}
}
