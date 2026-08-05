// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// buildChangesetWithResolves mirrors the production Update-construction phase:
// generate synthetic Resolve target ops via target_update.SynthesizeResolveTargetUpdates,
// then build the (ds-free) changeset. Keeps the old changeset.NewChangeset call
// shape so existing tests port with a mechanical rename.
func buildChangesetWithResolves(
	resourceUpdates []resource_update.ResourceUpdate,
	targetUpdates []target_update.TargetUpdate,
	commandID string,
	command pkgmodel.Command,
	ds target_update.TargetDatastore,
) (changeset.Changeset, error) {
	synth, err := target_update.SynthesizeResolveTargetUpdates(
		resource_update.ReferencedTargetLabels(resourceUpdates),
		resource_update.SourceTargetByKsuid(resourceUpdates),
		targetUpdates, ds)
	if err != nil {
		return changeset.Changeset{}, err
	}
	return changeset.NewChangeset(resourceUpdates, append(targetUpdates, synth...), commandID, command)
}
