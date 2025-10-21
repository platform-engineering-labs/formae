// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

type FormaApplyMode string

const (
	FormaApplyModePatch      FormaApplyMode = "patch"
	FormaApplyModeReconcile  FormaApplyMode = "reconcile"
	FormaApplyModeNone       FormaApplyMode = "none"
	FormaApplyModeProperties FormaApplyMode = "properties"
)
