// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

// Alias marks a previous identity of a Resource, Stack, or Target.
// Used by the rebind phase to find an existing managed row by its old
// label and rewrite its identity columns to the new label. See RFC-0041.
type Alias struct {
	Label string `json:"Label" pkl:"Label"`
}
