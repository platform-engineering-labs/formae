package main

import (
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
type InjectionState struct{}

// NewInjectionState creates a new, empty InjectionState.
func NewInjectionState() *InjectionState {
	return &InjectionState{}
}

// AddErrorRule adds a new error injection rule.
func (is *InjectionState) AddErrorRule(rule ErrorRule) {}

// AddLatencyRule adds a new latency injection rule.
func (is *InjectionState) AddLatencyRule(rule LatencyRule) {}

// CheckError checks if any error rule matches the given operation and resource type.
// Returns an error if a rule matches, or nil if no rule matches.
// For rules with Count > 0, decrements the count and removes the rule when exhausted.
func (is *InjectionState) CheckError(operation, resourceType string) error {
	return nil
}

// CheckLatency checks if any latency rule matches the given operation and resource type.
// Returns the duration to sleep, or 0 if no rule matches.
func (is *InjectionState) CheckLatency(operation, resourceType string) time.Duration {
	return 0
}

// Clear removes all error and latency rules.
func (is *InjectionState) Clear() {}
