// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package target_update

import (
	"fmt"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// targetLoader is the minimal datastore surface revalidateResolveTarget needs:
// it re-reads a persisted target by label. The full datastore.Datastore
// satisfies it, and a narrow stub can be used in tests.
type targetLoader interface {
	LoadTarget(label string) (*pkgmodel.Target, error)
}

// revalidateResolveTarget closes the TOCTOU window between changeset build and
// execute for a synthetic Resolve op.
//
// A Resolve TU is built (in NewChangeset) from a target's PERSISTED config at
// build time and carries that config plus the target's revision as the embedded
// Target.Version snapshot. Between build and execute a concurrent command can
// update the same target — new config, bumped revision. If the Resolve op then
// resolved the stale build-time config it could resolve the wrong (or a
// no-longer-declared) credential.
//
// At execute time this re-reads the live persisted target. If its revision
// differs from the snapshot the TU carries, it replaces the TU's config with the
// current config and rebuilds RemainingResolvables from it, so resolution runs
// against current state rather than the stale snapshot. If the target was deleted
// between build and execute it returns an error instead of resolving a phantom.
//
// A SINGLE re-read is authoritative: target mutation is serialized through the
// metastructure's ResourcePersister actor, so once this read returns within the
// executing command there is no further concurrent mutation to chase — there is
// no retry loop to bound.
//
// Only synthetic Resolve ops are re-validated; every other operation is returned
// untouched, because a real create/update/delete TU already carries the desired
// (and freshly re-resolved) config generated for this command.
func revalidateResolveTarget(tu TargetUpdate, ds targetLoader) (TargetUpdate, error) {
	if tu.Operation != TargetOperationResolve || ds == nil {
		return tu, nil
	}

	label := tu.Target.Label
	current, err := ds.LoadTarget(label)
	if err != nil {
		return tu, fmt.Errorf("re-validate resolve target %q: %w", label, err)
	}
	if current == nil {
		return tu, fmt.Errorf("re-validate resolve target %q: target no longer exists", label)
	}

	if current.Version == tu.Target.Version {
		// Revision unchanged: the snapshot config is still current, so resolve
		// against it as-is with no wasted re-read of resolvables.
		return tu, nil
	}

	// Revision advanced under us: rebuild against the current persisted config so
	// resolution never runs against the stale snapshot.
	tu.Target.Config = current.Config
	tu.Target.Version = current.Version
	tu.RemainingResolvables = resolver.ExtractResolvableURIsFromJSON(current.Config)
	return tu, nil
}
