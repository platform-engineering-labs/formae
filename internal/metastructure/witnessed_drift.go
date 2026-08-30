// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"encoding/json"
	"log/slog"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/patch"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// witnessedMovedModifications returns the modifications whose out-of-band
// movement sits on provider-default content formae's own last write
// witnessed. Such movement is drift, exactly like drift on a declared
// field: the caller adds these to the unabsorbed set so a soft reconcile
// rejects showing them, and a forced reconcile reverts them via the witness
// assertion. Candidates are the modifications filterUnabsorbedModifications
// absorbs (the unabsorbed ones already reject); a candidate that cannot be
// classified (a non-update operation, a missing property blob, no matching
// forma declaration, no write witness, or a diff error) is left absorbed,
// which is the pre-existing tolerant behavior.
func witnessedMovedModifications(modifications []datastore.ResourceModification, witnessByKsuid map[string]json.RawMessage, forma *pkgmodel.Forma, fa *forma_command.FormaCommand) []datastore.ResourceModification {
	type modKey struct {
		stack, typeName, label, operation string
	}
	unabsorbed := map[modKey]bool{}
	for _, mod := range filterUnabsorbedModifications(modifications, forma, fa) {
		unabsorbed[modKey{mod.Stack, mod.Type, mod.Label, mod.Operation}] = true
	}

	var moved []datastore.ResourceModification
	for _, mod := range modifications {
		if unabsorbed[modKey{mod.Stack, mod.Type, mod.Label, mod.Operation}] {
			continue
		}
		if mod.Operation != "update" || len(mod.OldProperties) == 0 || len(mod.Properties) == 0 {
			continue
		}
		decl := findDeclarationForModification(forma, mod)
		if decl == nil {
			continue
		}
		witness := witnessByKsuid[mod.Ksuid]
		if witness == nil {
			continue
		}
		diffs, err := patch.SuppressedFieldDiffs(mod.OldProperties, mod.Properties, decl.Properties, witness, decl.Schema)
		if err != nil {
			slog.Warn("Failed to classify witnessed drift for resource; treating as absorbed",
				"stack", mod.Stack, "type", mod.Type, "label", mod.Label, "error", err)
			continue
		}
		if len(diffs) > 0 {
			moved = append(moved, mod)
		}
	}
	return moved
}

// assertWitnessesIntoForma returns a copy of the forma in which every
// resource matching a modification with a write witness has formae's
// witnessed values asserted onto its omitted provider-default fields (see
// patch.AssertWitnessedSuppressed). A forced reconcile plans against the
// asserted desired state, so witnessed out-of-band movement is reverted the
// same way declared drift is overwritten. Resources without a witnessed
// modification are untouched; assertion failures degrade to the unasserted
// declaration.
func assertWitnessesIntoForma(forma *pkgmodel.Forma, modificationsByStack map[string][]datastore.ResourceModification, witnessByKsuid map[string]json.RawMessage) *pkgmodel.Forma {
	witnessForResource := map[*pkgmodel.Resource]json.RawMessage{}
	for _, modifications := range modificationsByStack {
		for _, mod := range modifications {
			if mod.Operation != "update" {
				continue
			}
			witness := witnessByKsuid[mod.Ksuid]
			if witness == nil {
				continue
			}
			if decl := findDeclarationForModification(forma, mod); decl != nil {
				witnessForResource[decl] = witness
			}
		}
	}
	if len(witnessForResource) == 0 {
		return forma
	}

	asserted := *forma
	asserted.Resources = append([]pkgmodel.Resource(nil), forma.Resources...)
	for i := range forma.Resources {
		witness, ok := witnessForResource[&forma.Resources[i]]
		if !ok {
			continue
		}
		props, err := patch.AssertWitnessedSuppressed(forma.Resources[i].Properties, witness, forma.Resources[i].Schema)
		if err != nil {
			slog.Warn("Failed to assert witnessed values for forced reconcile; leaving declaration as written",
				"stack", forma.Resources[i].Stack, "label", forma.Resources[i].Label, "error", err)
			continue
		}
		asserted.Resources[i].Properties = props
	}
	return &asserted
}

// findDeclarationForModification resolves a modification to its forma
// declaration by current label or by the declaration's alias (a rename
// records the modification under the old label), mirroring the matching
// filterUnabsorbedModifications uses for absorption.
func findDeclarationForModification(forma *pkgmodel.Forma, mod datastore.ResourceModification) *pkgmodel.Resource {
	for i := range forma.Resources {
		r := &forma.Resources[i]
		if r.Stack != mod.Stack || r.Type != mod.Type {
			continue
		}
		if r.Label == mod.Label || (r.Alias != "" && r.Alias == mod.Label) {
			return r
		}
	}
	return nil
}
