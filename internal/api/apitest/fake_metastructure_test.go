// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package apitest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// TestFakeMetastructure_ListResponsesStickyTail verifies that when the response
// queue has exactly one element left, ListFormaCommandStatus returns it without
// popping, so subsequent calls keep getting the same response (sticky tail).
// This prevents panic on slow CI machines that poll more times than responses provided.
func TestFakeMetastructure_ListResponsesStickyTail(t *testing.T) {
	fake := &FakeMetastructure{
		ListResponses: []WrappedListResponse{
			{
				ListCommandStatusResponse: &apimodel.ListCommandStatusResponse{
					Commands: []apimodel.Command{
						{CommandID: "cmd-1"},
					},
				},
			},
			{
				ListCommandStatusResponse: &apimodel.ListCommandStatusResponse{
					Commands: []apimodel.Command{
						{CommandID: "cmd-2"},
					},
				},
			},
		},
	}

	// Call 1: should return response 1 (and pop it).
	resp1, err1 := fake.ListFormaCommandStatus("", "", 0)
	if err1 != nil {
		t.Fatalf("call 1: unexpected error: %v", err1)
	}
	if resp1 == nil || (len(resp1.Commands) > 0 && resp1.Commands[0].CommandID != "cmd-1") {
		t.Fatalf("call 1: expected cmd-1, got %v", resp1)
	}

	// Call 2: should return response 2 (and pop it, leaving queue length 1).
	resp2, err2 := fake.ListFormaCommandStatus("", "", 0)
	if err2 != nil {
		t.Fatalf("call 2: unexpected error: %v", err2)
	}
	if resp2 == nil || (len(resp2.Commands) > 0 && resp2.Commands[0].CommandID != "cmd-2") {
		t.Fatalf("call 2: expected cmd-2, got %v", resp2)
	}

	// Call 3: queue is empty, should return response 2 again (sticky tail).
	// This should NOT panic; old code would index out of bounds.
	resp3, err3 := fake.ListFormaCommandStatus("", "", 0)
	if err3 != nil {
		t.Fatalf("call 3: unexpected error: %v", err3)
	}
	if resp3 == nil || (len(resp3.Commands) > 0 && resp3.Commands[0].CommandID != "cmd-2") {
		t.Fatalf("call 3: expected cmd-2 (sticky), got %v", resp3)
	}

	// Call 4: should still return response 2 (sticky tail).
	resp4, err4 := fake.ListFormaCommandStatus("", "", 0)
	if err4 != nil {
		t.Fatalf("call 4: unexpected error: %v", err4)
	}
	if resp4 == nil || (len(resp4.Commands) > 0 && resp4.Commands[0].CommandID != "cmd-2") {
		t.Fatalf("call 4: expected cmd-2 (sticky), got %v", resp4)
	}
}

func TestFake_ExtractUnseeded_EmptySuccess(t *testing.T) {
	m := &FakeMetastructure{}
	f, err := m.ExtractResources("")
	require.NoError(t, err)
	assert.Empty(t, f.Resources)
	targets, err := m.ExtractTargets("")
	require.NoError(t, err)
	assert.Empty(t, targets)
	stacks, err := m.ExtractStacks()
	require.NoError(t, err)
	assert.Empty(t, stacks)
	policies, err := m.ExtractPolicies()
	require.NoError(t, err)
	assert.Empty(t, policies)
}

func TestFake_ExtractStacks_FIFOWithStickyTail(t *testing.T) {
	m := &FakeMetastructure{StackResponses: []WrappedStackResponse{
		{Stacks: []*pkgmodel.Stack{{Label: "one"}}},
		{Stacks: []*pkgmodel.Stack{{Label: "two"}}},
	}}
	s1, _ := m.ExtractStacks()
	s2, _ := m.ExtractStacks()
	s3, _ := m.ExtractStacks() // sticky tail repeats
	assert.Equal(t, "one", s1[0].Label)
	assert.Equal(t, "two", s2[0].Label)
	assert.Equal(t, "two", s3[0].Label)
}

// TestFakeMetastructure_ListResponsesEmpty verifies that an empty response queue
// doesn't panic; instead it returns nil response + nil error (safe zero behavior).
func TestFakeMetastructure_ListResponsesEmpty(t *testing.T) {
	fake := &FakeMetastructure{
		ListResponses: []WrappedListResponse{},
	}

	// Calling when empty should not panic.
	resp, err := fake.ListFormaCommandStatus("", "", 0)
	if resp != nil {
		t.Fatalf("expected nil response for empty queue, got %v", resp)
	}
	if err != nil {
		t.Fatalf("expected nil error for empty queue, got %v", err)
	}
}
