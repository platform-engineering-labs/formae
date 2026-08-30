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
	// SuppressedDrift is present on every reconcile-mode response, including
	// a no-changes one (where Command is empty), so callers see suppressed
	// out-of-band movement regardless of whether anything was planned.
	SuppressedDrift []SuppressedDriftNote `json:"SuppressedDrift,omitempty"`
}

type ListCommandStatusResponse struct {
	Commands []Command `json:"Commands"`
}

// CommandScope selects which commands an *empty* command-status query
// covers. It has no effect on a non-empty query: there the query itself
// decides (`client:me` narrows to the caller), and the scope is redundant.
//
//   - CommandScopeClient — the calling client's single most recent command.
//     This is the default when the parameter is absent, so callers written
//     against the older API keep their behavior.
//   - CommandScopeAgent — every client's commands, newest first, up to the
//     requested page size. This is what `formae command list` asks for.
//
// Either way only user-initiated commands are visible; scheduler bookkeeping
// (sync, discovery, auto-reconcile, stack expiry) is never returned.
type CommandScope string

const (
	CommandScopeClient CommandScope = "client"
	CommandScopeAgent  CommandScope = "agent"
)

type Command struct {
	CommandID        string            `json:"CommandId"`
	Command          string            `json:"Command"`
	Mode             string            `json:"Mode,omitempty"` // "reconcile" | "patch"
	Source           string            `json:"Source,omitempty"`
	Subject          string            `json:",omitempty"`
	SubjectName      string            `json:",omitempty"`
	State            string            `json:"State"`
	StartTs          time.Time         `json:"StartTs,omitempty"`
	EndTs            time.Time         `json:"EndTs,omitempty"`
	ResourceUpdates  []ResourceUpdate  `json:"ResourceUpdates,omitempty"`
	TargetUpdates    []TargetUpdate    `json:"TargetUpdates,omitempty"`
	StackUpdates     []StackUpdate     `json:"StackUpdates,omitempty"`
	PolicyUpdates    []PolicyUpdate    `json:"PolicyUpdates,omitempty"`
	GeneratorUpdates []GeneratorUpdate `json:"GeneratorUpdates,omitempty"`
	// SuppressedDrift records out-of-band movement on provider-default
	// fields this command's plan could not see; completing the command
	// absorbs it (see SuppressedDriftNote).
	SuppressedDrift []SuppressedDriftNote `json:"SuppressedDrift,omitempty"`
}

// SuppressedDriftNote is out-of-band movement on a provider-default field a
// reconcile's plan cannot see: the field is annotated hasProviderDefault and
// the forma does not declare it, so the diff suppresses it. From and To
// carry the moved content; both are absent for an opaque path, which records
// path and movement only. Disposition says what the submission did about it:
// "remaining" (nothing executed; the drift stays in the
// changes-since-last-reconcile window) or "absorbed" (the command's
// completion advances the window past it without addressing it).
type SuppressedDriftNote struct {
	Stack       string          `json:"Stack"`
	Type        string          `json:"Type"`
	Label       string          `json:"Label"`
	Path        string          `json:"Path"`
	From        json.RawMessage `json:"From,omitempty"`
	To          json.RawMessage `json:"To,omitempty"`
	Opaque      bool            `json:"Opaque,omitempty"`
	Disposition string          `json:"Disposition"`
}

// wrapper for machine-readable output
type CommandID struct {
	CommandID string `json:"CommandId"`
}

type CancelCommandResponse struct {
	CommandIDs           []string                       `json:"CommandIds"`
	ResourceUpdateStates map[string]CancelResourceState `json:"ResourceUpdateStates,omitempty"`
	// Forced is true when the cancellation was requested with --force. The CLI
	// uses this to surface the force-cancel warning and the list of resources
	// that were abandoned mid-operation.
	Forced bool `json:"Forced,omitempty"`
}

// CancelResourceState represents the state of a resource update at cancel time.
type CancelResourceState struct {
	State string `json:"State"` // "Canceled", "InProgress", "Success", "Failed"
	// ForceCanceled is true when this resource update was force-canceled while an
	// operation was actually in progress (it carries an OperationStatusCanceled
	// progress entry). These are the resources whose cloud-side state may be
	// orphaned and need manual verification.
	ForceCanceled bool `json:"ForceCanceled,omitempty"`
	// CommandID attributes this update to the canceled command it belongs to;
	// the ResourceUpdateStates map is flat across all canceled commands.
	CommandID string `json:"CommandId,omitempty"`
}

type ResourceUpdate struct {
	ResourceID    string `json:"ResourceId"`
	ResourceType  string `json:"ResourceType"`
	ResourceLabel string `json:"ResourceLabel,omitempty"`
	// OldLabel is the resource's previous label. Populated only when a
	// label rename is part of this update (via the alias path); empty
	// otherwise. The renderer uses it to surface the rename to the user.
	OldLabel      string          `json:"OldLabel,omitempty"`
	StackName     string          `json:"StackName,omitempty"`
	OldStackName  string          `json:"OldStackName,omitempty"`
	Operation     string          `json:"Operation"`
	PatchDocument json.RawMessage `json:"PatchDocument,omitempty"`
	// CreateOnlyPatch is a JSON-patch document (same format as PatchDocument)
	// listing only the ops against createOnly fields that triggered a
	// resource replacement. Populated on the delete half of a replace pair
	// so the CLI can render which immutable properties forced the replace.
	// Never sent to resource plugins — the replace executes as a plain
	// destroy + create.
	CreateOnlyPatch json.RawMessage   `json:"CreateOnlyPatch,omitempty"`
	State           string            `json:"State"`
	StartedAt       time.Time         `json:"StartedAt,omitempty"` // when the update began (for live elapsed)
	Duration        int64             `json:"Duration,omitempty"`  // milliseconds (final, set on completion)
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
	TargetLabel    string          `json:"TargetLabel"`
	Operation      string          `json:"Operation"`
	State          string          `json:"State"`
	Duration       int64           `json:"Duration,omitempty"` // milliseconds
	ErrorMessage   string          `json:"ErrorMessage,omitempty"`
	Discoverable   bool            `json:"Discoverable"`
	ExistingConfig json.RawMessage `json:"ExistingConfig,omitempty"`
	DesiredConfig  json.RawMessage `json:"DesiredConfig,omitempty"`
	StartTs        time.Time       `json:"StartTs,omitempty"`
	ModifiedTs     time.Time       `json:"ModifiedTs,omitempty"`
	IsCascade      bool            `json:"IsCascade,omitempty"`
	CascadeSource  string          `json:"CascadeSource,omitempty"`
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
	PolicyConfig      json.RawMessage `json:"PolicyConfig,omitempty"`      // Current policy configuration
	OldPolicyConfig   json.RawMessage `json:"OldPolicyConfig,omitempty"`   // Previous policy configuration (for updates)
	ReferencingStacks []string        `json:"ReferencingStacks,omitempty"` // For skip operations - stacks still referencing this policy
	StartTs           time.Time       `json:"StartTs,omitempty"`
	ModifiedTs        time.Time       `json:"ModifiedTs,omitempty"`
}

// GeneratorUpdate is the API projection of a generator change: a create, an
// update (spec change and/or rename), or a delete. GeneratorConfig and
// OldGeneratorConfig carry the generator's declared spec only — the fields a
// forma author writes. A generator's own identity (its KSUID) and drawn
// generation are controller state that never reaches this projection: no
// concrete Generator marshals its ID, and the value a generation drew does
// not exist at plan/simulate time to project in the first place.
type GeneratorUpdate struct {
	GeneratorLabel     string          `json:"GeneratorLabel"`
	GeneratorType      string          `json:"GeneratorType"` // "password", etc.
	StackName          string          `json:"StackName,omitempty"`
	Operation          string          `json:"Operation"`
	State              string          `json:"State"`
	Duration           int64           `json:"Duration,omitempty"` // milliseconds
	ErrorMessage       string          `json:"ErrorMessage,omitempty"`
	GeneratorConfig    json.RawMessage `json:"GeneratorConfig,omitempty"`    // Current generator configuration
	OldGeneratorConfig json.RawMessage `json:"OldGeneratorConfig,omitempty"` // Previous generator configuration (for updates)
	StartTs            time.Time       `json:"StartTs,omitempty"`
	ModifiedTs         time.Time       `json:"ModifiedTs,omitempty"`
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
	Plugins            []PluginInfo   `json:"Plugins"`
	// ReapPendingTargets counts targets that are still 'unreachable' but have
	// already accrued at least their configured reap-after duration — they
	// are due to be reaped (on an upcoming reaper tick, or held back by the
	// rate cap or an in-flight command). Surfaced so an over-threshold target
	// is visible before any tombstone.
	ReapPendingTargets int `json:"ReapPendingTargets"`
	// ReapedTargets counts targets whose current health state is 'reaped'.
	ReapedTargets int `json:"ReapedTargets"`
}

// PluginInfo represents information about a registered plugin
// including the merged config (plugin defaults + user overrides).
type PluginInfo struct {
	Namespace               string                 `json:"Namespace"`
	Version                 string                 `json:"Version"`
	NodeName                string                 `json:"NodeName"`
	MaxRequestsPerSecond    int                    `json:"MaxRequestsPerSecond"`
	ResourceCount           int                    `json:"ResourceCount"`
	ResourceTypesToDiscover []string               `json:"ResourceTypesToDiscover,omitempty"`
	RetryConfig             *pkgmodel.RetryConfig  `json:"RetryConfig,omitempty"`
	LabelConfig             *pkgmodel.LabelConfig  `json:"LabelConfig,omitempty"`
	DiscoveryFilters        []pkgmodel.MatchFilter `json:"DiscoveryFilters,omitempty"`
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
	Name              string   `json:"name"`
	Kind              string   `json:"kind,omitempty"`
	Type              string   `json:"type"`
	Namespace         string   `json:"namespace,omitempty"`
	Category          string   `json:"category,omitempty"`
	Summary           string   `json:"summary,omitempty"`
	Description       string   `json:"description,omitempty"`
	Publisher         string   `json:"publisher,omitempty"`
	License           string   `json:"license,omitempty"`
	InstalledVersion  string   `json:"installedVersion,omitempty"`
	AvailableVersions []string `json:"availableVersions,omitempty"`
	// LocalPath is the absolute path on the agent's filesystem to the
	// plugin's PklProject file (containing the plugin's PKL schema).
	// Populated by the discovery scan when the plugin is installed
	// locally; empty when no on-disk install is found. Used by the CLI's
	// --schema-location local flow to import schemas via PklProject.deps
	// rather than fetching from the hub. Same-box only — the path is
	// only meaningful when the CLI shares a filesystem with the agent.
	LocalPath string `json:"localPath,omitempty"`

	Channel    string                       `json:"channel,omitempty"`
	Frozen     bool                         `json:"frozen,omitempty"`
	ManagedBy  string                       `json:"managedBy,omitempty"`
	LoadStatus string                       `json:"loadStatus,omitempty"`
	Metadata   map[string]map[string]string `json:"metadata,omitempty"`
}

// PluginOperation describes a single operation performed on a plugin.
type PluginOperation struct {
	Name    string `json:"name"`
	Type    string `json:"type,omitempty"`
	Version string `json:"version,omitempty"`
	Action  string `json:"action"` // "install" | "remove" | "update" | "noop"
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
	Channel  string       `json:"channel,omitempty"`
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

type UpdatePluginsRequest struct {
	Packages []PackageRef `json:"packages,omitempty"`
	Channel  string       `json:"channel,omitempty"`
}

type UpdatePluginsResponse struct {
	Operations      []PluginOperation `json:"operations"`
	RequiresRestart bool              `json:"requiresRestart"`
	Warnings        []string          `json:"warnings,omitempty"`
}
