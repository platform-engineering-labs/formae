package main

import (
	"time"
)

// OperationLogEntry records a single plugin CRUD operation.
type OperationLogEntry struct {
	Operation    string    // "Create", "Read", "Update", "Delete", "List"
	ResourceType string    // the resource type acted upon
	NativeID     string    // the native ID (may be empty for Create/List)
	Timestamp    time.Time // when the operation occurred
}

// OperationLog is a thread-safe append-only log of plugin operations.
// CRUD methods record entries; the test harness queries it via TestController.
type OperationLog struct{}

// NewOperationLog creates a new, empty OperationLog.
func NewOperationLog() *OperationLog {
	return &OperationLog{}
}

// Record appends an entry to the log.
func (ol *OperationLog) Record(entry OperationLogEntry) {}

// Snapshot returns a deep copy of all log entries. Mutating the returned slice
// does not affect the OperationLog.
func (ol *OperationLog) Snapshot() []OperationLogEntry {
	return nil
}
