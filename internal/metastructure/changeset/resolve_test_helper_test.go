// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package changeset

import (
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// buildChangesetForTest mirrors the production Update-construction phase: it
// generates any synthetic Resolve target ops (and runs the transitive-opaque
// rejection) via target_update.SynthesizeResolveTargetUpdates, then builds the
// (now ds-free) changeset. It keeps the old NewChangeset(rus, tus, id, cmd, ds)
// call shape so the many existing tests port with a mechanical rename, and it
// preserves their assertions: a reject error surfaces from the synthesis step,
// a dependency-cycle error from NewChangeset.
func buildChangesetForTest(
	resourceUpdates []resource_update.ResourceUpdate,
	targetUpdates []target_update.TargetUpdate,
	commandID string,
	command pkgmodel.Command,
	ds target_update.TargetDatastore,
) (Changeset, error) {
	synth, err := target_update.SynthesizeResolveTargetUpdates(
		resource_update.ReferencedTargetLabels(resourceUpdates),
		resource_update.SourceTargetByKsuid(resourceUpdates),
		targetUpdates, ds)
	if err != nil {
		return Changeset{}, err
	}
	return NewChangeset(resourceUpdates, append(targetUpdates, synth...), commandID, command)
}
