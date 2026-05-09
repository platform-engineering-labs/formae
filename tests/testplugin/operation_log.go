// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"sync"

	"github.com/platform-engineering-labs/formae/tests/testcontrol"
)

// OperationLog is a thread-safe append-only log of plugin operations.
// CRUD methods record entries; the test harness queries it via TestController.
type OperationLog struct {
	mu      sync.Mutex
	entries []testcontrol.OperationLogEntry
}

// NewOperationLog creates a new, empty OperationLog.
func NewOperationLog() *OperationLog {
	return &OperationLog{}
}

// Record appends an entry to the log.
func (ol *OperationLog) Record(entry testcontrol.OperationLogEntry) {
	ol.mu.Lock()
	defer ol.mu.Unlock()
	ol.entries = append(ol.entries, entry)
}

// Snapshot returns a deep copy of all log entries. Mutating the returned slice
// does not affect the OperationLog.
func (ol *OperationLog) Snapshot() []testcontrol.OperationLogEntry {
	ol.mu.Lock()
	defer ol.mu.Unlock()
	cp := make([]testcontrol.OperationLogEntry, len(ol.entries))
	copy(cp, ol.entries)
	return cp
}