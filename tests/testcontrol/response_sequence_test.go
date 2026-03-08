// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package testcontrol

import (
	"testing"
)

func TestResponseSequenceTypes(t *testing.T) {
	t.Run("construction", func(t *testing.T) {
		step := ResponseStep{ErrorCode: "NotFound"}
		if step.ErrorCode != "NotFound" {
			t.Fatalf("expected ErrorCode 'NotFound', got %q", step.ErrorCode)
		}

		successStep := ResponseStep{}
		if successStep.ErrorCode != "" {
			t.Fatalf("expected empty ErrorCode for success step, got %q", successStep.ErrorCode)
		}

		seq := PluginOpSequence{
			MatchKey:  "my-resource",
			Operation: "Create",
			Steps:     []ResponseStep{step, successStep},
		}
		if seq.MatchKey != "my-resource" {
			t.Fatalf("expected MatchKey 'my-resource', got %q", seq.MatchKey)
		}
		if seq.Operation != "Create" {
			t.Fatalf("expected Operation 'Create', got %q", seq.Operation)
		}
		if len(seq.Steps) != 2 {
			t.Fatalf("expected 2 steps, got %d", len(seq.Steps))
		}

		req := ProgramResponsesRequest{
			Sequences: []PluginOpSequence{seq},
		}
		if len(req.Sequences) != 1 {
			t.Fatalf("expected 1 sequence, got %d", len(req.Sequences))
		}

		// Empty request is valid (clears all sequences).
		emptyReq := ProgramResponsesRequest{}
		if emptyReq.Sequences != nil {
			t.Fatalf("expected nil sequences for empty request, got %v", emptyReq.Sequences)
		}

		// Response type is a simple acknowledgement.
		_ = ProgramResponsesResponse{}
	})

	t.Run("empty steps means always succeed", func(t *testing.T) {
		seq := PluginOpSequence{
			MatchKey:  "native-123",
			Operation: "Read",
			Steps:     nil,
		}
		if seq.Steps != nil {
			t.Fatalf("expected nil steps, got %v", seq.Steps)
		}

		seq2 := PluginOpSequence{
			MatchKey:  "native-123",
			Operation: "Read",
			Steps:     []ResponseStep{},
		}
		if len(seq2.Steps) != 0 {
			t.Fatalf("expected 0 steps, got %d", len(seq2.Steps))
		}
	})
}

func TestResponseSequenceEDFRegistration(t *testing.T) {
	// RegisterEDFTypes should succeed without error.
	// Calling it twice should also succeed (ErrTaken is tolerated).
	if err := RegisterEDFTypes(); err != nil {
		t.Fatalf("first RegisterEDFTypes() failed: %v", err)
	}
	if err := RegisterEDFTypes(); err != nil {
		t.Fatalf("second RegisterEDFTypes() failed (idempotency): %v", err)
	}
}
