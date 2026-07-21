// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package messages

import (
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

type LoadResource struct {
	ResourceURI pkgmodel.FormaeURI
}

type LoadResourceResult struct {
	Resource pkgmodel.Resource
	Target   pkgmodel.Target
}

// CleanupEmptyStacks is sent to ResourcePersister after a changeset completes
// to delete any stacks that no longer have resources.
type CleanupEmptyStacks struct {
	StackLabels []string
	CommandID   string
}

// UpdateTargetHealth is sent asynchronously to ResourcePersister to record a
// health observation for a target. The persister applies it via an in-place
// UPDATE guarded by monotonicity and incarnation checks.
type UpdateTargetHealth struct {
	Observation pkgmodel.TargetHealthObservation
}
