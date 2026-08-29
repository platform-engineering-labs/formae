// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"encoding/json"
	"log/slog"
	"sort"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/patch"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// computeSuppressedDriftNotes classifies out-of-band movement the plan cannot
// see. It is strictly additive over the drift-rejection decision: candidates
// are exactly the modifications filterUnabsorbedModifications absorbs (the
// unabsorbed ones are rejection territory, displayed in full there), and for
// each candidate the note carries the provider-default fields whose
// suppressed content moved between the last reconcile and now, gated on the
// write witness: only content present in the resource's last write echo
// (witnessByKsuid, from GetPropertiesAtLastWrite) can be reported as moved;
// values that only ever arrived through sync are the infrastructure's
// business and stay in the drift list. A candidate that cannot be
// classified (a non-update operation, a missing property blob, no matching
// forma declaration, no write witness, or a diff error) produces no note:
// every fallback is today's behavior, silence.
//
// The caller stamps the disposition: absorbed when a reconcile executes and
// takes the drift out of the window, remaining when nothing executes and the
// drift stays.
func computeSuppressedDriftNotes(modificationsByStack map[string][]datastore.ResourceModification, witnessByKsuid map[string]json.RawMessage, forma *pkgmodel.Forma, fa *forma_command.FormaCommand) []forma_command.SuppressedDriftNote {
	type modKey struct {
		stack, typeName, label, operation string
	}

	stacks := make([]string, 0, len(modificationsByStack))
	for stack := range modificationsByStack {
		stacks = append(stacks, stack)
	}
	sort.Strings(stacks)

	var notes []forma_command.SuppressedDriftNote
	for _, stack := range stacks {
		mods := modificationsByStack[stack]
		unabsorbed := map[modKey]bool{}
		for _, mod := range filterUnabsorbedModifications(mods, forma, fa) {
			unabsorbed[modKey{mod.Stack, mod.Type, mod.Label, mod.Operation}] = true
		}

		for _, mod := range mods {
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
				slog.Warn("Failed to classify suppressed drift for resource; skipping note",
					"stack", mod.Stack, "type", mod.Type, "label", mod.Label, "error", err)
				continue
			}
			for _, d := range diffs {
				notes = append(notes, forma_command.SuppressedDriftNote{
					Stack:  mod.Stack,
					Type:   mod.Type,
					Label:  mod.Label,
					Path:   d.Path,
					From:   d.From,
					To:     d.To,
					Opaque: d.Opaque,
				})
			}
		}
	}
	return notes
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

// stampSuppressedDriftDisposition returns the notes with the given
// disposition set on each.
func stampSuppressedDriftDisposition(notes []forma_command.SuppressedDriftNote, disposition forma_command.SuppressedDriftDisposition) []forma_command.SuppressedDriftNote {
	if len(notes) == 0 {
		return nil
	}
	out := make([]forma_command.SuppressedDriftNote, len(notes))
	for i, n := range notes {
		n.Disposition = disposition
		out[i] = n
	}
	return out
}
