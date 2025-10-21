// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package stats

type Stats struct {
	Clients            int            `json:"Clients"`
	Commands           map[string]int `json:"Commands"`
	States             map[string]int `json:"States"`
	Stacks             int            `json:"Stacks"`
	ManagedResources   int            `json:"Resources"`
	UnmanagedResources int            `json:"UnmanagedResources"`
	Targets            int            `json:"Targets"`
	ResourceTypes      map[string]int `json:"ResourceTypes"`
	ResourceErrors     map[string]int `json:"ResourceErrors"`
}
