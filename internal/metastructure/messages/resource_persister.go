// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package messages

import (
	"time"

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

// AdvanceTargetAccrual is sent asynchronously to ResourcePersister by the
// TargetReaper to apply one tick's computed unreachability-accrual delta for
// a target. The persister applies it via an in-place UPDATE guarded by
// incarnation match and current max-version pinning (see
// datastore.AdvanceTargetAccrual).
type AdvanceTargetAccrual struct {
	TargetLabel   string
	IncarnationID string
	LastSampleAt  time.Time
	DeltaSeconds  int64
}

// PersistTargetReap is sent (via Call) to ResourcePersister to execute a target
// reap as one datastore transaction. Unlike the accrual/health messages it needs
// a return value — whether the reap committed — so it is a Call and replies with
// a PersistTargetReapResult. Sent by the TargetReaper for each candidate it
// actually reaps this tick (subject to the rate cap and dry-run mode).
type PersistTargetReap struct {
	Label            string
	IncarnationID    string
	LastSeenBefore   time.Time
	LastSampleBefore time.Time
	ReapedAt         time.Time
}

// PersistTargetReapResult is the reply to a PersistTargetReap call. Reaped is
// true only when the reap transaction committed. ReapedStackLabels holds the
// distinct stacks whose live resources the reap tombstoned; the persister uses
// them to clean up any stack the reap empties.
type PersistTargetReapResult struct {
	Reaped            bool
	ReapedStackLabels []string
}
