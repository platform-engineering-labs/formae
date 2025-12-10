// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"time"
)

type SubmitCommandResponse struct {
	CommandID   string      `json:"CommandId"`
	Description Description `json:"Description"`
	Simulation  Simulation  `json:"Simulation"`
}

type Description struct {
	Text    string `json:"Text,omitempty"`
	Confirm bool   `json:"Confirm,omitempty"`
}

type Simulation struct {
	ChangesRequired bool    `json:"ChangesRequired"`
	Command         Command `json:"Command"`
}

type ListCommandStatusResponse struct {
	Commands []Command `json:"Commands"`
}

type Command struct {
	CommandID       string           `json:"CommandId"`
	Command         string           `json:"Command"`
	State           string           `json:"State"`
	StartTs         time.Time        `json:"StartTs,omitempty"`
	EndTs           time.Time        `json:"EndTs,omitempty"`
	ResourceUpdates []ResourceUpdate `json:"ResourceUpdates,omitempty"`
	TargetUpdates   []TargetUpdate   `json:"TargetUpdates,omitempty"`
}

// wrapper for machine-readable output
type CommandID struct {
	CommandID string `json:"CommandId"`
}

type CancelCommandResponse struct {
	CommandIDs []string `json:"CommandIds"`
}

type ResourceUpdate struct {
	ResourceID      string            `json:"ResourceId"`
	ResourceType    string            `json:"ResourceType"`
	ResourceLabel   string            `json:"ResourceLabel,omitempty"`
	StackName       string            `json:"StackName,omitempty"`
	OldStackName    string            `json:"OldStackName,omitempty"`
	Operation       string            `json:"Operation"`
	PatchDocument   json.RawMessage   `json:"PatchDocument,omitempty"`
	State           string            `json:"State"`
	Duration        int64             `json:"Duration,omitempty"` // milliseconds
	CurrentAttempt  int               `json:"CurrentAttempt,omitempty"`
	MaxAttempts     int               `json:"MaxAttempts,omitempty"`
	ErrorMessage    string            `json:"ErrorMessage,omitempty"`
	StatusMessage   string            `json:"StateMessage,omitempty"`
	Properties      json.RawMessage   `json:"Properties,omitempty"`
	OldProperties   json.RawMessage   `json:"OldProperties,omitempty"`
	GroupID         string            `json:"GroupId,omitempty"`
	ReferenceLabels map[string]string `json:"ReferenceLabels,omitempty"`
}

const (
	OperationCreate  = "create"
	OperationUpdate  = "update"
	OperationDelete  = "delete"
	OperationRead    = "read"
	OperationReplace = "replace" // delete + create
)

const (
	ResourceUpdateStateUnknown    = "Unknown"
	ResourceUpdateStateNotStarted = "NotStarted"
	ResourceUpdateStatePending    = "Pending"
	ResourceUpdateStateInProgress = "InProgress"
	ResourceUpdateStateFailed     = "Failed"
	ResourceUpdateStateSuccess    = "Success"
	ResourceUpdateStateCanceled   = "Canceled"
	ResourceUpdateStateRejected   = "Rejected"
)

type TargetUpdate struct {
	TargetLabel  string    `json:"TargetLabel"`
	Operation    string    `json:"Operation"`
	State        string    `json:"State"`
	Duration     int64     `json:"Duration,omitempty"` // milliseconds
	ErrorMessage string    `json:"ErrorMessage,omitempty"`
	Discoverable bool      `json:"Discoverable"`
	StartTs      time.Time `json:"StartTs,omitempty"`
	ModifiedTs   time.Time `json:"ModifiedTs,omitempty"`
}

type Stats struct {
	Version            string         `json:"Version"`
	AgentID            string         `json:"AgentId"`
	Clients            int            `json:"Clients"`
	Commands           map[string]int `json:"Commands"`
	States             map[string]int `json:"States"`
	Stacks             int            `json:"Stacks"`
	ManagedResources   int            `json:"Resources"`
	UnmanagedResources int            `json:"UnmanagedResources"`
	Targets            int            `json:"Targets"`
	ResourceTypes      map[string]int `json:"ResourceTypes"`
	ResourceErrors     map[string]int `json:"ResourceErrors"`
	Plugins            []PluginInfo   `json:"Plugins"`
}

// PluginInfo represents information about a registered plugin
type PluginInfo struct {
	Namespace            string `json:"Namespace"`
	NodeName             string `json:"NodeName"`
	MaxRequestsPerSecond int    `json:"MaxRequestsPerSecond"`
	ResourceCount        int    `json:"ResourceCount"`
}
