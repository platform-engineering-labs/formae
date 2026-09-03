// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package drift

import (
	"log/slog"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/patch"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// RetainConfrontable drops from an unabsorbed set the modifications whose
// out-of-band movement a reconcile need not confront: those on a resource the
// forma declares whose movement is confined to a co-owned collection's
// never-owned members (a co-actor's content). It exists because the earlier
// gate rejected any modified resource the forma also plans to change, which
// meant a single co-actor-injected label made every edit of that resource
// fail — the churn the ownership partition is meant to end.
//
// A modification is kept (still confrontable) when it has no matching forma
// declaration (an orphan, which cannot be classified), or when
// patch.ModificationConfrontable finds real drift: a plain-field change, a
// witnessed provider-default move, or a declared/formerly-owned co-owned
// member move. Classification errs toward keeping: a resource whose
// modification cannot be read is left in the set.
func RetainConfrontable(unabsorbed []datastore.ResourceModification, recordByKsuid map[string]pkgmodel.OwnedMembers, forma *pkgmodel.Forma) []datastore.ResourceModification {
	kept := make([]datastore.ResourceModification, 0, len(unabsorbed))
	for _, mod := range unabsorbed {
		if !modificationTolerated(mod, recordByKsuid, forma) {
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
func modificationTolerated(mod datastore.ResourceModification, recordByKsuid map[string]pkgmodel.OwnedMembers, forma *pkgmodel.Forma) bool {
	if mod.Operation != "update" || len(mod.OldProperties) == 0 || len(mod.Properties) == 0 {
		return false
	}
	decl := findDeclarationForModification(forma, mod)
	if decl == nil {
		return false
	}
	confrontable, err := patch.ModificationConfrontable(
		mod.OldProperties, mod.Properties, decl.Properties,
		recordByKsuid[mod.Ksuid], decl.Schema)
	if err != nil {
		slog.Warn("Failed to classify modification for confrontation; keeping it as drift",
			"stack", mod.Stack, "type", mod.Type, "label", mod.Label, "error", err)
		return false
	}
	return !confrontable
}
