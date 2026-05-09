// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/platform-engineering-labs/formae/tests/testcontrol"
)

// ResponseQueue holds per-key, per-operation queues of programmed responses.
// When a CRUD method is called, it checks this queue first. If a step is found
// it is popped (FIFO) and returned. When the queue is exhausted (or no queue
// exists for the key), nil is returned so the caller can fall through to the
// existing global injection logic.
//
// The queue is keyed by "matchKey:operation" where matchKey is the resource
// Name (for Create) or NativeID (for Read/Update/Delete).
type ResponseQueue struct {
	mu     sync.Mutex
	queues map[string][]testcontrol.ResponseStep
}

// NewResponseQueue creates a new, empty ResponseQueue.
func NewResponseQueue() *ResponseQueue {
	return &ResponseQueue{
		queues: make(map[string][]testcontrol.ResponseStep),
	}
}

// Program appends the given sequences to the existing queues. Sequences for
// the same matchKey:operation pair are appended in order, preserving any
// unconsumed steps from previous calls. This is essential when concurrent
// commands each program their own failure injection — earlier responses must
// not be wiped.
//
// Passing nil clears all queues (used between test iterations).
func (rq *ResponseQueue) Program(sequences []testcontrol.PluginOpSequence) {
	rq.mu.Lock()
	defer rq.mu.Unlock()

	if sequences == nil {
		rq.queues = make(map[string][]testcontrol.ResponseStep)
		return
	}

	for _, seq := range sequences {
		key := queueKey(seq.MatchKey, seq.Operation)
		// Copy the steps slice so the caller can't mutate our internal state.
		steps := make([]testcontrol.ResponseStep, len(seq.Steps))
		copy(steps, seq.Steps)
		rq.queues[key] = append(rq.queues[key], steps...)
	}
}

// Unprogram removes the responses that were previously added by Program for
// the given sequences. For each sequence, it removes the last N steps from the
// corresponding queue (where N = len(seq.Steps)). This is used to roll back
// responses when a command is rejected after programming.
func (rq *ResponseQueue) Unprogram(sequences []testcontrol.PluginOpSequence) {
	rq.mu.Lock()
	defer rq.mu.Unlock()

	for _, seq := range sequences {
		key := queueKey(seq.MatchKey, seq.Operation)
		existing := rq.queues[key]
		removeCount := len(seq.Steps)
		if removeCount > len(existing) {
			removeCount = len(existing)
		}
		rq.queues[key] = existing[:len(existing)-removeCount]
	}
}

// CheckCreate extracts the Name field from the properties JSON and looks up
// the queue for "Name:Create". Returns nil if no queue or queue exhausted.
func (rq *ResponseQueue) CheckCreate(properties json.RawMessage) *testcontrol.ResponseStep {
	name, err := extractName(properties)
	if err != nil || name == "" {
		return nil
	}
	return rq.pop(name, "Create")
}

// CheckRead looks up the queue for "nativeID:Read".
func (rq *ResponseQueue) CheckRead(nativeID string) *testcontrol.ResponseStep {
	return rq.pop(nativeID, "Read")
}

// CheckUpdate looks up the queue for "nativeID:Update".
func (rq *ResponseQueue) CheckUpdate(nativeID string) *testcontrol.ResponseStep {
	return rq.pop(nativeID, "Update")
}

// CheckDelete looks up the queue for "nativeID:Delete".
func (rq *ResponseQueue) CheckDelete(nativeID string) *testcontrol.ResponseStep {
	return rq.pop(nativeID, "Delete")
}

// pop removes and returns the next step for the given matchKey and operation.
// Returns nil if no queue exists or the queue is exhausted.
func (rq *ResponseQueue) pop(matchKey, operation string) *testcontrol.ResponseStep {
	rq.mu.Lock()
	defer rq.mu.Unlock()

	key := queueKey(matchKey, operation)
	steps, ok := rq.queues[key]
	if !ok || len(steps) == 0 {
		return nil
	}

	step := steps[0]
	rq.queues[key] = steps[1:]
	return &step
}

// queueKey builds the map key from matchKey and operation.
func queueKey(matchKey, operation string) string {
	return fmt.Sprintf("%s:%s", matchKey, operation)
}

// extractName parses the "Name" field from a JSON properties blob.
func extractName(properties json.RawMessage) (string, error) {
	var parsed struct {
		Name string `json:"Name"`
	}
	if err := json.Unmarshal(properties, &parsed); err != nil {
		return "", err
	}
	return parsed.Name, nil
}
