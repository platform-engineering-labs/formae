// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func TestPlugin_Create_StoresInCloudState(t *testing.T) {
	cs := NewCloudState()
	p := &TestPlugin{cloudState: cs}

	props := json.RawMessage(`{"Name":"my-resource","Value":"hello"}`)
	result, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: "Test::Generic::Resource",
		Properties:   props,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if result == nil || result.ProgressResult == nil {
		t.Fatal("expected non-nil CreateResult with ProgressResult")
	}
	if result.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("OperationStatus: got %q, want %q", result.ProgressResult.OperationStatus, resource.OperationStatusSuccess)
	}

	nativeID := result.ProgressResult.NativeID
	if nativeID == "" {
		t.Fatal("expected non-empty NativeID")
	}

	// Verify the resource was stored in cloud state
	entry, ok := cs.Get(nativeID)
	if !ok {
		t.Fatalf("expected resource %q to exist in cloud state after Create", nativeID)
	}
	if entry.ResourceType != "Test::Generic::Resource" {
		t.Errorf("ResourceType: got %q, want %q", entry.ResourceType, "Test::Generic::Resource")
	}
	if entry.Properties != string(props) {
		t.Errorf("Properties: got %q, want %q", entry.Properties, string(props))
	}
}

func TestPlugin_Read_ReturnsFromCloudState(t *testing.T) {
	cs := NewCloudState()
	p := &TestPlugin{cloudState: cs}

	// Pre-populate cloud state
	props := `{"Name":"existing-resource","Value":"world"}`
	cs.Put("native-42", "Test::Generic::Resource", props)

	result, err := p.Read(context.Background(), &resource.ReadRequest{
		NativeID:     "native-42",
		ResourceType: "Test::Generic::Resource",
	})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil ReadResult for existing resource")
	}
	if result.ResourceType != "Test::Generic::Resource" {
		t.Errorf("ResourceType: got %q, want %q", result.ResourceType, "Test::Generic::Resource")
	}
	if result.Properties != props {
		t.Errorf("Properties: got %q, want %q", result.Properties, props)
	}
}

func TestPlugin_Read_NotFound_ReturnsNil(t *testing.T) {
	cs := NewCloudState()
	p := &TestPlugin{cloudState: cs}

	result, err := p.Read(context.Background(), &resource.ReadRequest{
		NativeID:     "does-not-exist",
		ResourceType: "Test::Generic::Resource",
	})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil ReadResult for non-existent resource, got %+v", result)
	}
}

func TestPlugin_Delete_RemovesFromCloudState(t *testing.T) {
	cs := NewCloudState()
	p := &TestPlugin{cloudState: cs}

	// Create a resource first
	props := json.RawMessage(`{"Name":"to-delete","Value":"bye"}`)
	createResult, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: "Test::Generic::Resource",
		Properties:   props,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	nativeID := createResult.ProgressResult.NativeID

	// Verify it exists
	_, ok := cs.Get(nativeID)
	if !ok {
		t.Fatalf("expected resource %q to exist after Create", nativeID)
	}

	// Delete it
	deleteResult, err := p.Delete(context.Background(), &resource.DeleteRequest{
		NativeID:     nativeID,
		ResourceType: "Test::Generic::Resource",
	})
	if err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if deleteResult == nil || deleteResult.ProgressResult == nil {
		t.Fatal("expected non-nil DeleteResult with ProgressResult")
	}
	if deleteResult.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("OperationStatus: got %q, want %q", deleteResult.ProgressResult.OperationStatus, resource.OperationStatusSuccess)
	}
	if deleteResult.ProgressResult.NativeID != nativeID {
		t.Errorf("NativeID: got %q, want %q", deleteResult.ProgressResult.NativeID, nativeID)
	}

	// Verify it's gone from cloud state
	_, ok = cs.Get(nativeID)
	if ok {
		t.Fatalf("expected resource %q to be gone after Delete", nativeID)
	}
}

func TestPlugin_Create_WithInjectedError(t *testing.T) {
	cs := NewCloudState()
	inj := NewInjectionState()
	inj.AddErrorRule(ErrorRule{
		Operation: "Create",
		Error:     "simulated cloud failure",
		Count:     1,
	})
	p := &TestPlugin{cloudState: cs, injections: inj}

	_, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: "Test::Generic::Resource",
		Properties:   []byte(`{"Name":"bucket-1"}`),
	})
	if err == nil {
		t.Fatal("expected error from injected failure, got nil")
	}
	if err.Error() != "simulated cloud failure" {
		t.Errorf("error message: got %q, want %q", err.Error(), "simulated cloud failure")
	}

	// Second call should succeed (count=1 was exhausted)
	result, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: "Test::Generic::Resource",
		Properties:   []byte(`{"Name":"bucket-2"}`),
	})
	if err != nil {
		t.Fatalf("expected success after exhaustion, got %v", err)
	}
	if result.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("OperationStatus: got %q, want %q", result.ProgressResult.OperationStatus, resource.OperationStatusSuccess)
	}
}

func TestPlugin_Read_WithInjectedError(t *testing.T) {
	cs := NewCloudState()
	cs.Put("native-1", "Test::Generic::Resource", `{"Name":"a"}`)
	inj := NewInjectionState()
	inj.AddErrorRule(ErrorRule{
		Operation: "Read",
		Error:     "read failure",
		Count:     1,
	})
	p := &TestPlugin{cloudState: cs, injections: inj}

	_, err := p.Read(context.Background(), &resource.ReadRequest{
		NativeID:     "native-1",
		ResourceType: "Test::Generic::Resource",
	})
	if err == nil {
		t.Fatal("expected error from injected failure, got nil")
	}
}

func TestPlugin_OperationLog_RecordsOps(t *testing.T) {
	cs := NewCloudState()
	ol := NewOperationLog()
	p := &TestPlugin{cloudState: cs, opLog: ol}

	// Create
	result, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: "Test::Generic::Resource",
		Properties:   []byte(`{"Name":"logged"}`),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Read
	_, err = p.Read(context.Background(), &resource.ReadRequest{
		NativeID:     result.ProgressResult.NativeID,
		ResourceType: "Test::Generic::Resource",
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	snap := ol.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 log entries, got %d", len(snap))
	}
	if snap[0].Operation != "Create" {
		t.Errorf("entry 0: got %q, want Create", snap[0].Operation)
	}
	if snap[1].Operation != "Read" {
		t.Errorf("entry 1: got %q, want Read", snap[1].Operation)
	}
}

func TestPlugin_List_ReturnsNativeIDs(t *testing.T) {
	cs := NewCloudState()
	p := &TestPlugin{cloudState: cs}

	// Pre-populate cloud state with multiple resources
	cs.Put("res-1", "Test::Generic::Resource", `{"Name":"r1"}`)
	cs.Put("res-2", "Test::Generic::Resource", `{"Name":"r2"}`)
	cs.Put("res-3", "Test::Special::Resource", `{"Name":"s1"}`)

	result, err := p.List(context.Background(), &resource.ListRequest{
		ResourceType: "Test::Generic::Resource",
	})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil ListResult")
	}
	if len(result.NativeIDs) != 2 {
		t.Fatalf("expected 2 NativeIDs, got %d: %v", len(result.NativeIDs), result.NativeIDs)
	}

	// Check both are present (order not guaranteed)
	found := map[string]bool{}
	for _, id := range result.NativeIDs {
		found[id] = true
	}
	if !found["res-1"] || !found["res-2"] {
		t.Errorf("expected res-1 and res-2 in results, got %v", result.NativeIDs)
	}
}