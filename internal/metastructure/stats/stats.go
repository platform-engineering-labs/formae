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
	ResourceErrors     map[string]int `json:"ResourceErrors"`     // key: resource type
}
