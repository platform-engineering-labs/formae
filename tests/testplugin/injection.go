// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"fmt"
	"sync"
	"time"
)

// ErrorRule defines a fault injection rule that causes a CRUD operation to return an error.
// If Count is 0, the error fires on every matching call (permanent).
// If Count > 0, the error fires that many times and is then removed.
// If ResourceType is empty, the rule matches all resource types.
type ErrorRule struct {
	Operation    string // "Create", "Read", "Update", "Delete", "List"
	ResourceType string // empty matches all
	Error        string // error message to return
	Count        int    // 0 = permanent, >0 = fire N times then remove
}

// LatencyRule defines a latency injection rule that causes a CRUD operation to sleep.
// If ResourceType is empty, the rule matches all resource types.
type LatencyRule struct {
	Operation    string        // "Create", "Read", "Update", "Delete", "List"
	ResourceType string        // empty matches all
	Duration     time.Duration // how long to sleep
}

// InjectionState holds error and latency rules for fault injection.
// It is thread-safe and shared between the TestController actor (which writes rules)
// and the plugin CRUD methods (which read rules).
type InjectionState struct {
	mu           sync.Mutex
	errorRules   []ErrorRule
	latencyRules []LatencyRule
}

// NewInjectionState creates a new, empty InjectionState.
func NewInjectionState() *InjectionState {
	return &InjectionState{}
}

// AddErrorRule adds a new error injection rule.
func (is *InjectionState) AddErrorRule(rule ErrorRule) {
	is.mu.Lock()
	defer is.mu.Unlock()
	is.errorRules = append(is.errorRules, rule)
}

// AddLatencyRule adds a new latency injection rule.
func (is *InjectionState) AddLatencyRule(rule LatencyRule) {
	is.mu.Lock()
	defer is.mu.Unlock()
	is.latencyRules = append(is.latencyRules, rule)
}

// CheckError checks if any error rule matches the given operation and resource type.
// Returns an error if a rule matches, or nil if no rule matches.
// For rules with Count > 0, decrements the count and removes the rule when exhausted.
func (is *InjectionState) CheckError(operation, resourceType string) error {
	is.mu.Lock()
	defer is.mu.Unlock()

	for i := range is.errorRules {
		r := &is.errorRules[i]
		if r.Operation != operation {
			continue
		}
		if r.ResourceType != "" && r.ResourceType != resourceType {
			continue
		}
		err := fmt.Errorf("%s", r.Error)
		if r.Count > 0 {
			r.Count--
			if r.Count == 0 {
				is.errorRules = append(is.errorRules[:i], is.errorRules[i+1:]...)
			}
		}
		return err
	}
	return nil
}

// CheckLatency checks if any latency rule matches the given operation and resource type.
// Returns the duration to sleep, or 0 if no rule matches.
func (is *InjectionState) CheckLatency(operation, resourceType string) time.Duration {
	is.mu.Lock()
	defer is.mu.Unlock()

	for _, r := range is.latencyRules {
		if r.Operation != operation {
			continue
		}
		if r.ResourceType != "" && r.ResourceType != resourceType {
			continue
		}
		return r.Duration
	}
	return 0
}

// Clear removes all error and latency rules.
func (is *InjectionState) Clear() {
	is.mu.Lock()
	defer is.mu.Unlock()
	is.errorRules = nil
	is.latencyRules = nil
}