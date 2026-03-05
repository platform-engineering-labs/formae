// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"encoding/json"
	"testing"

	"github.com/platform-engineering-labs/formae/tests/testcontrol"
	"github.com/stretchr/testify/assert"
)

func TestCloudState_CreateAndGet(t *testing.T) {
	cs := NewCloudState()

	props := `{"Name":"my-resource","Value":"hello"}`
	cs.Put("native-1", "Test::Generic::Resource", props)

	entry, ok := cs.Get("native-1")
	if !ok {
		t.Fatal("expected entry to exist after Put, but Get returned false")
	}
	if entry.NativeID != "native-1" {
		t.Errorf("NativeID: got %q, want %q", entry.NativeID, "native-1")
	}
	if entry.ResourceType != "Test::Generic::Resource" {
		t.Errorf("ResourceType: got %q, want %q", entry.ResourceType, "Test::Generic::Resource")
	}
	if entry.Properties != props {
		t.Errorf("Properties: got %q, want %q", entry.Properties, props)
	}
}

func TestCloudState_GetMissing(t *testing.T) {
	cs := NewCloudState()

	_, ok := cs.Get("does-not-exist")
	if ok {
		t.Fatal("expected Get to return false for non-existent entry")
	}
}

func TestCloudState_Delete(t *testing.T) {
	cs := NewCloudState()

	cs.Put("native-1", "Test::Generic::Resource", `{"Name":"r1"}`)
	cs.Delete("native-1")

	_, ok := cs.Get("native-1")
	if ok {
		t.Fatal("expected entry to be gone after Delete, but Get returned true")
	}
}

func TestCloudState_Modify(t *testing.T) {
	cs := NewCloudState()

	cs.Put("native-1", "Test::Generic::Resource", `{"Name":"r1","Value":"original"}`)
	cs.Put("native-1", "Test::Generic::Resource", `{"Name":"r1","Value":"updated"}`)

	entry, ok := cs.Get("native-1")
	if !ok {
		t.Fatal("expected entry to exist after second Put")
	}

	// Verify the properties were updated
	var got map[string]any
	if err := json.Unmarshal([]byte(entry.Properties), &got); err != nil {
		t.Fatalf("failed to unmarshal properties: %v", err)
	}
	if got["Value"] != "updated" {
		t.Errorf("Value: got %q, want %q", got["Value"], "updated")
	}
}

func TestCloudState_ListByType(t *testing.T) {
	cs := NewCloudState()

	cs.Put("generic-1", "Test::Generic::Resource", `{"Name":"g1"}`)
	cs.Put("generic-2", "Test::Generic::Resource", `{"Name":"g2"}`)
	cs.Put("special-1", "Test::Special::Resource", `{"Name":"s1"}`)

	genericIDs := cs.ListNativeIDs("Test::Generic::Resource")
	if len(genericIDs) != 2 {
		t.Fatalf("expected 2 generic IDs, got %d: %v", len(genericIDs), genericIDs)
	}
	// Check both are present (order not guaranteed)
	found := map[string]bool{}
	for _, id := range genericIDs {
		found[id] = true
	}
	if !found["generic-1"] || !found["generic-2"] {
		t.Errorf("expected generic-1 and generic-2 in results, got %v", genericIDs)
	}

	specialIDs := cs.ListNativeIDs("Test::Special::Resource")
	if len(specialIDs) != 1 {
		t.Fatalf("expected 1 special ID, got %d: %v", len(specialIDs), specialIDs)
	}
	if specialIDs[0] != "special-1" {
		t.Errorf("expected special-1, got %q", specialIDs[0])
	}

	emptyIDs := cs.ListNativeIDs("Test::NonExistent::Resource")
	if len(emptyIDs) != 0 {
		t.Errorf("expected 0 IDs for non-existent type, got %d: %v", len(emptyIDs), emptyIDs)
	}
}

func TestCloudState_Snapshot(t *testing.T) {
	cs := NewCloudState()

	cs.Put("native-1", "Test::Generic::Resource", `{"Name":"r1"}`)
	cs.Put("native-2", "Test::Generic::Resource", `{"Name":"r2"}`)

	snap := cs.Snapshot()

	// Verify snapshot has correct contents
	if len(snap) != 2 {
		t.Fatalf("expected snapshot length 2, got %d", len(snap))
	}
	if snap["native-1"].NativeID != "native-1" {
		t.Errorf("snapshot native-1 NativeID: got %q, want %q", snap["native-1"].NativeID, "native-1")
	}
	if snap["native-2"].NativeID != "native-2" {
		t.Errorf("snapshot native-2 NativeID: got %q, want %q", snap["native-2"].NativeID, "native-2")
	}

	// Mutate the snapshot and verify the original is not affected
	snap["native-1"] = testcontrol.CloudStateEntry{
		NativeID:     "native-1",
		ResourceType: "Test::Generic::Resource",
		Properties:   `{"Name":"MUTATED"}`,
	}
	snap["native-3"] = testcontrol.CloudStateEntry{
		NativeID:     "native-3",
		ResourceType: "Test::Generic::Resource",
		Properties:   `{"Name":"injected"}`,
	}

	// Original should be unaffected
	entry, ok := cs.Get("native-1")
	if !ok {
		t.Fatal("expected native-1 to still exist in original")
	}
	if entry.Properties != `{"Name":"r1"}` {
		t.Errorf("original properties mutated: got %q, want %q", entry.Properties, `{"Name":"r1"}`)
	}

	_, ok = cs.Get("native-3")
	if ok {
		t.Fatal("native-3 should not exist in original after adding to snapshot")
	}
}

func TestCloudState_ListNativeIDsFiltered(t *testing.T) {
	cs := NewCloudState()
	cs.Put("parent-1", "Test::Generic::Resource", `{"Name":"parent-a","Value":"v1"}`)
	cs.Put("child-1", "Test::Generic::ChildResource", `{"Name":"child-a-0","ParentId":"parent-a","Value":"v2"}`)
	cs.Put("child-2", "Test::Generic::ChildResource", `{"Name":"child-b-0","ParentId":"parent-b","Value":"v3"}`)

	// Filter children by ParentId=parent-a
	ids := cs.ListNativeIDsFiltered("Test::Generic::ChildResource", "ParentId", "parent-a")
	assert.Equal(t, []string{"child-1"}, ids)

	// Filter with non-matching value
	ids = cs.ListNativeIDsFiltered("Test::Generic::ChildResource", "ParentId", "nonexistent")
	assert.Empty(t, ids)
}