// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package messages

import "github.com/platform-engineering-labs/formae/pkg/model"

type ResolveValue struct {
	ResourceURI model.FormaeURI
}

type ValueResolved struct {
	ResourceURI model.FormaeURI
	Value       string
}

type FailedToResolveValue struct {
	ResourceURI model.FormaeURI
	// Reason is a human-readable explanation of why the value could not be
	// resolved (e.g. which property was missing on which source resource).
	// It is surfaced to the operator as the failed resource update's
	// ErrorMessage.
	Reason string
}
