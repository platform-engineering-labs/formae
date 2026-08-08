// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package stats

type Stats struct {
	Clients            int            `json:"Clients"`
	Commands           map[string]int `json:"Commands"`
	States             map[string]int `json:"States"`
	Stacks             int            `json:"Stacks"`
	ManagedResources   map[string]int `json:"Resources"`          // key: namespace (e.g., "AWS", "Azure")
	UnmanagedResources map[string]int `json:"UnmanagedResources"` // key: namespace
	Targets            map[string]int `json:"Targets"`            // key: namespace
	ResourceTypes      map[string]int `json:"ResourceTypes"`      // key: resource type (e.g., "AWS::S3::Bucket")
	// ResourceErrors counts live resources whose latest completed operation
	// failed, so a resource that later succeeds stops being counted. A retry
	// that is still in flight does not clear the failure: only a completed
	// outcome supersedes an earlier one. A resource the current inventory no
	// longer holds — destroyed, reaped, or never created — is not counted at
	// all, and the type it is reported under is the one that inventory records
	// now rather than the one stored on the failing operation. The count
	// reflects the last operation formae recorded, not verified cloud health.
	ResourceErrors map[string]int `json:"ResourceErrors"` // key: resource type
}
