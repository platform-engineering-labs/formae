// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ReferenceToRemovedPropertyError reports that a resource references a
// property that this command's reconcile removes from its source: the
// reference would dangle the moment the producer's update executes. The plan
// is rejected so the author either keeps the property declared or removes the
// reference.
type ReferenceToRemovedPropertyError struct {
	ConsumerLabel string
	SourceLabel   string
	PropertyPath  string
}

func (e ReferenceToRemovedPropertyError) Error() string {
	return fmt.Sprintf("resource %q references property %q of %q, which this command removes; keep the property declared or remove the reference", e.ConsumerLabel, e.PropertyPath, e.SourceLabel)
}

// removedProperty records that a reconcile-generated update removes the
// JSON-pointer path Path from the resource this update targets, along with
// that resource's label (for error reporting).
type removedProperty struct {
	Label string
	Path  string
}

// jsonPatchRemoveOp is the shape this package needs out of a PatchDocument
// entry to detect a "remove" op — decoupled from the jsonpatch package's own
// operation type so an unrelated field added there can't break decoding here.
type jsonPatchRemoveOp struct {
	Op   string `json:"op"`
	Path string `json:"path"`
}

// validateReferencesAgainstRemovals rejects a reconcile plan in which one
// resource's generated update removes a property that another declared
// resource still references. Absence has more than one cause — a
// provider-computed output that was never declared, a patch-mode omission
// that leaves the field untouched, a reconcile omission that mints no remove
// op — and none of those dangle a reference. Only an actual "remove" op in a
// generated PatchDocument does, so this pass keys off that op alone.
//
// Removes are read off the generated updates (only an OperationUpdate's
// PatchDocument can carry one). Consumers are read off the forma's own
// declared resources rather than the generated updates: a resource whose own
// declaration is unchanged, and therefore never receives a ResourceUpdate,
// still holds the dangling reference once its source is removed, and the
// forma sweep is what makes that consumer visible.
func validateReferencesAgainstRemovals(updates []ResourceUpdate, forma *pkgmodel.Forma) error {
	// First sweep: collect every "remove" op's JSON-pointer path, keyed by
	// the KSUID of the resource the removal executes against.
	removedByKsuid := make(map[string][]removedProperty)
	for _, u := range updates {
		if u.Operation != OperationUpdate {
			continue
		}
		if len(u.DesiredState.PatchDocument) == 0 {
			continue
		}
		var ops []jsonPatchRemoveOp
		if err := json.Unmarshal(u.DesiredState.PatchDocument, &ops); err != nil {
			// Malformed patch documents are caught elsewhere; this pass only
			// cares about well-formed "remove" ops.
			continue
		}
		for _, op := range ops {
			if op.Op != "remove" {
				continue
			}
			removedByKsuid[u.DesiredState.Ksuid] = append(removedByKsuid[u.DesiredState.Ksuid], removedProperty{
				Label: u.DesiredState.Label,
				Path:  op.Path,
			})
		}
	}
	if len(removedByKsuid) == 0 {
		return nil
	}

	// Second sweep: every resource the forma declares — whether it ends up
	// unchanged, updated, or created — must not reference a path removed
	// above (an exact match, or a nested path under it — a removal of a
	// member within a collection, e.g. /Tags/1, does not dangle a reference
	// to the collection itself, /Tags).
	for _, r := range forma.Resources {
		for _, uri := range resolver.ExtractResolvableURIs(r) {
			removed, ok := removedByKsuid[uri.KSUID()]
			if !ok {
				continue
			}
			refPath := "/" + uri.PropertyPath()
			for _, rp := range removed {
				if refPath == rp.Path || strings.HasPrefix(refPath, rp.Path+"/") {
					return ReferenceToRemovedPropertyError{
						ConsumerLabel: r.Label,
						SourceLabel:   rp.Label,
						PropertyPath:  uri.PropertyPath(),
					}
				}
			}
		}
	}
	return nil
}
