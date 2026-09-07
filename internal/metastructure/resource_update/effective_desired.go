// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"encoding/json"
	"fmt"

	"github.com/platform-engineering-labs/formae/internal/metastructure/patch"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// EffectiveDesired maps an existing resource's KSUID to the desired properties
// document this command will drive it to: the forma declaration after the same
// value-strategy filtering that decides the resource's own patch — SetOnce
// substitution AND the no-baseline writeOnly+createOnly strip. The lookup must
// never advertise a value the producer's pipeline will not write: a consumer
// resolving such a value would be planned (and for a createOnly destination,
// destructively replaced) against state that never materializes. It is
// computed once per command, ahead of the resolvable lookup, and the same
// artifact feeds both the lookup and the update factory, so the filtering is
// never re-derived in two places.
type EffectiveDesired map[string]json.RawMessage

// ComputeEffectiveDesired builds the map for every forma resource whose KSUID
// matches a persisted row. A resource with no persisted row (a create) has no
// entry: its declaration is already effective as written.
func ComputeEffectiveDesired(forma *pkgmodel.Forma, allResourcesByStack map[string][]*pkgmodel.Resource) (EffectiveDesired, error) {
	persisted := make(map[string]*pkgmodel.Resource)
	for _, resources := range allResourcesByStack {
		for _, r := range resources {
			if r.Ksuid != "" {
				persisted[r.Ksuid] = r
			}
		}
	}
	eff := make(EffectiveDesired)
	for i := range forma.Resources {
		r := &forma.Resources[i]
		if r.Ksuid == "" {
			continue
		}
		row, ok := persisted[r.Ksuid]
		if !ok {
			continue
		}
		filtered, err := filterSetOnceProps(row.Properties, r.Properties, r.Label)
		if err != nil {
			return nil, fmt.Errorf("failed to compute effective desired properties for %s: %w", r.Label, err)
		}
		// The baseline is probed against the persisted row's properties, while
		// patch generation probes the flattened document; for the fields in
		// question (writeOnly+createOnly, schema-hinted) presence in the
		// persisted row is the baseline in both pipelines.
		filtered, err = patch.StripFieldsWithoutBaseline(filtered, row.Properties, r.Schema)
		if err != nil {
			return nil, fmt.Errorf("failed to strip no-baseline fields from effective desired properties for %s: %w", r.Label, err)
		}
		eff[r.Ksuid] = filtered
	}
	return eff, nil
}
