// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package drift

import (
	"encoding/json"
	"log/slog"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/patch"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// RetainConfrontable drops tolerated movement even when the declaration also
// plans an unrelated change to the resource. Tolerated movement includes
// never-owned co-owned members and initial population of undeclared provider
// defaults. Plain fields, established or declared default values, and owned
// collection members remain protected. Missing declarations or unreadable
// properties are retained because they cannot safely be classified.
func RetainConfrontable(unabsorbed []datastore.ResourceModification, recordByKsuid map[string]pkgmodel.OwnedMembers, witnessByKsuid map[string]json.RawMessage, forma *pkgmodel.Forma) []datastore.ResourceModification {
	kept := make([]datastore.ResourceModification, 0, len(unabsorbed))
	for _, mod := range unabsorbed {
		if !modificationTolerated(mod, recordByKsuid, witnessByKsuid, forma) {
			kept = append(kept, mod)
		}
	}
	return kept
}

// modificationTolerated reports whether a modification is safe to drop from
// the unabsorbed set. It is true only for an update on a declared resource
// whose movement patch.ModificationConfrontable classifies as non-confrontable.
// Every uncertain case (a non-update op, missing properties, no declaration,
// a classification error) returns false so the modification is kept.
func modificationTolerated(mod datastore.ResourceModification, recordByKsuid map[string]pkgmodel.OwnedMembers, witnessByKsuid map[string]json.RawMessage, forma *pkgmodel.Forma) bool {
	if mod.Operation != "update" || len(mod.OldProperties) == 0 || len(mod.Properties) == 0 {
		return false
	}
	decl := findDeclarationForModification(forma, mod)
	if decl == nil {
		return false
	}
	confrontable, err := patch.ModificationConfrontable(
		mod.OldProperties, mod.Properties, decl.Properties, witnessByKsuid[mod.Ksuid],
		recordByKsuid[mod.Ksuid], decl.Schema)
	if err != nil {
		slog.Warn("Failed to classify modification for confrontation; keeping it as drift",
			"stack", mod.Stack, "type", mod.Type, "label", mod.Label, "error", err)
		return false
	}
	return !confrontable
}
