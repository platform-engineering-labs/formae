// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"time"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
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
	ChangesRequired bool     `json:"ChangesRequired"`
	Command         Command  `json:"Command"`
	Warnings        []string `json:"Warnings,omitempty"`
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
	StackUpdates    []StackUpdate    `json:"StackUpdates,omitempty"`
	PolicyUpdates   []PolicyUpdate   `json:"PolicyUpdates,omitempty"`
}

// wrapper for machine-readable output
type CommandID struct {
	CommandID string `json:"CommandId"`
}

type CancelCommandResponse struct {
	CommandIDs           []string                       `json:"CommandIds"`
	ResourceUpdateStates map[string]CancelResourceState `json:"ResourceUpdateStates,omitempty"`
}

// CancelResourceState represents the state of a resource update at cancel time.
type CancelResourceState struct {
	State string `json:"State"` // "Canceled", "InProgress", "Success", "Failed"
}

type ResourceUpdate struct {
	ResourceID      string            `json:"ResourceId"`
	ResourceType    string            `json:"ResourceType"`
	ResourceLabel   string            `json:"ResourceLabel,omitempty"`
	StackName       string            `json:"StackName,omitempty"`
	OldStackName    string            `json:"OldStackName,omitempty"`
	Operation       string            `json:"Operation"`
	PatchDocument   json.RawMessage   `json:"PatchDocument,omitempty"`
	// CreateOnlyPatch is a JSON-patch document (same format as PatchDocument)
	// listing only the ops against createOnly fields that triggered a
	// resource replacement. Populated on the delete half of a replace pair
	// so the CLI can render which immutable properties forced the replace.
	// Never sent to resource plugins — the replace executes as a plain
	// destroy + create.
	CreateOnlyPatch json.RawMessage   `json:"CreateOnlyPatch,omitempty"`
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
	NativeID        string            `json:"NativeId,omitempty"`
	IsCascade       bool              `json:"IsCascade,omitempty"`
	CascadeSource   string            `json:"CascadeSource,omitempty"`
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
	TargetLabel    string              `json:"TargetLabel"`
	Operation      string              `json:"Operation"`
	State          string              `json:"State"`
	Duration       int64               `json:"Duration,omitempty"` // milliseconds
	ErrorMessage   string              `json:"ErrorMessage,omitempty"`
	Discoverable   bool                `json:"Discoverable"`
	ExistingConfig json.RawMessage     `json:"ExistingConfig,omitempty"`
	DesiredConfig  json.RawMessage     `json:"DesiredConfig,omitempty"`
	StartTs        time.Time           `json:"StartTs,omitempty"`
	ModifiedTs     time.Time           `json:"ModifiedTs,omitempty"`
	IsCascade      bool                `json:"IsCascade,omitempty"`
	CascadeSource  string              `json:"CascadeSource,omitempty"`
}

type StackUpdate struct {
	StackLabel   string    `json:"StackLabel"`
	Operation    string    `json:"Operation"`
	State        string    `json:"State"`
	Duration     int64     `json:"Duration,omitempty"` // milliseconds
	ErrorMessage string    `json:"ErrorMessage,omitempty"`
	Description  string    `json:"Description"`
	StartTs      time.Time `json:"StartTs,omitempty"`
	ModifiedTs   time.Time `json:"ModifiedTs,omitempty"`
}

type PolicyUpdate struct {
	PolicyLabel       string          `json:"PolicyLabel"`
	PolicyType        string          `json:"PolicyType"` // "ttl", etc.
	StackLabel        string          `json:"StackLabel,omitempty"`
	Operation         string          `json:"Operation"`
	State             string          `json:"State"`
	Duration          int64           `json:"Duration,omitempty"` // milliseconds
	ErrorMessage      string          `json:"ErrorMessage,omitempty"`
	PolicyConfig      json.RawMessage `json:"PolicyConfig,omitempty"`    // Current policy configuration
	OldPolicyConfig   json.RawMessage `json:"OldPolicyConfig,omitempty"` // Previous policy configuration (for updates)
	ReferencingStacks []string        `json:"ReferencingStacks,omitempty"` // For skip operations - stacks still referencing this policy
	StartTs           time.Time       `json:"StartTs,omitempty"`
	ModifiedTs        time.Time       `json:"ModifiedTs,omitempty"`
}

// PolicyInventoryItem represents a standalone policy in the inventory
type PolicyInventoryItem struct {
	Label          string          `json:"Label"`
	Type           string          `json:"Type"`
	Config         json.RawMessage `json:"Config"`
	AttachedStacks []string        `json:"AttachedStacks,omitempty"`
}

type Stats struct {
	Version            string         `json:"Version"`
	AgentID            string         `json:"AgentId"`
	Clients            int            `json:"Clients"`
	Commands           map[string]int `json:"Commands"`
	States             map[string]int `json:"States"`
	Stacks             int            `json:"Stacks"`
	ManagedResources   map[string]int `json:"Resources"`          // key: namespace (e.g., "AWS", "Azure")
	UnmanagedResources map[string]int `json:"UnmanagedResources"` // key: namespace
	Targets            map[string]int `json:"Targets"`            // key: namespace
	ResourceTypes      map[string]int `json:"ResourceTypes"`      // key: resource type (e.g., "AWS::S3::Bucket")
	ResourceErrors     map[string]int `json:"ResourceErrors"`     // key: resource type
	Plugins            []PluginInfo   `json:"Plugins"`
}

// PluginInfo represents information about a registered plugin
// including the merged config (plugin defaults + user overrides).
type PluginInfo struct {
	Namespace               string           `json:"Namespace"`
	Version                 string           `json:"Version"`
	NodeName                string           `json:"NodeName"`
	MaxRequestsPerSecond    int              `json:"MaxRequestsPerSecond"`
	ResourceCount           int              `json:"ResourceCount"`
	ResourceTypesToDiscover []string         `json:"ResourceTypesToDiscover,omitempty"`
	RetryConfig             *pkgmodel.RetryConfig    `json:"RetryConfig,omitempty"`
	LabelConfig             *pkgmodel.LabelConfig    `json:"LabelConfig,omitempty"`
	DiscoveryFilters        []pkgmodel.MatchFilter   `json:"DiscoveryFilters,omitempty"`
}

type ForceReconcileResponse struct {
	CommandID string `json:"command_id,omitempty"`
	Message   string `json:"message,omitempty"`
}

type ForceCheckTTLResponse struct {
	ExpiredStacks []string `json:"expired_stacks"`
	CommandIDs    []string `json:"command_ids,omitempty"`
}

// Plugin describes a single plugin, used by the list and info endpoints.
type Plugin struct {
	Name              string                       `json:"name"`
	Type              string                       `json:"type"`
	Namespace         string                       `json:"namespace,omitempty"`
	Category          string                       `json:"category,omitempty"`
	Summary           string                       `json:"summary,omitempty"`
	Description       string                       `json:"description,omitempty"`
	Publisher         string                       `json:"publisher,omitempty"`
	License           string                       `json:"license,omitempty"`
	InstalledVersion  string                       `json:"installedVersion,omitempty"`
	AvailableVersions []string                     `json:"availableVersions,omitempty"`
	Channel           string                       `json:"channel,omitempty"`
	Frozen            bool                         `json:"frozen,omitempty"`
	ManagedBy         string                       `json:"managedBy,omitempty"`
	LoadStatus        string                       `json:"loadStatus,omitempty"`
	Metadata          map[string]map[string]string `json:"metadata,omitempty"`
}

// PluginOperation describes a single operation performed on a plugin.
type PluginOperation struct {
	Name    string `json:"name"`
	Type    string `json:"type,omitempty"`
	Version string `json:"version,omitempty"`
	Action  string `json:"action"` // "install" | "remove" | "noop"
}

// PackageRef identifies a plugin package, optionally at a specific version.
type PackageRef struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type ListPluginsResponse struct {
	Plugins []Plugin `json:"plugins"`
}

type GetPluginResponse struct {
	Plugin Plugin `json:"plugin"`
}

type InstallPluginsRequest struct {
	Packages []PackageRef `json:"packages"`
}

type InstallPluginsResponse struct {
	Operations      []PluginOperation `json:"operations"`
	RequiresRestart bool              `json:"requiresRestart"`
	Warnings        []string          `json:"warnings,omitempty"`
}

type UninstallPluginsRequest struct {
	Packages []PackageRef `json:"packages"`
}

type UninstallPluginsResponse struct {
	Operations      []PluginOperation `json:"operations"`
	RequiresRestart bool              `json:"requiresRestart"`
	Warnings        []string          `json:"warnings,omitempty"`
}

type UpgradePluginsRequest struct {
	Packages []PackageRef `json:"packages,omitempty"`
}

type UpgradePluginsResponse struct {
	Operations      []PluginOperation `json:"operations"`
	RequiresRestart bool              `json:"requiresRestart"`
	Warnings        []string          `json:"warnings,omitempty"`
}
