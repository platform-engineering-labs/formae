// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package resource_update

import (
	"maps"
	"slices"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// acceptedOwnershipView owns only the inventory rows whose declaration metadata
// it overlays. Properties, schema and raw observed Version remain the live view.
// The optional reader avoids a datastore import cycle and preserves old test
// doubles; all production backends and the certified planning reader implement it.
func acceptedOwnershipView(ds ResourceDataLookup, forma *pkgmodel.Forma, observed map[string][]*pkgmodel.Resource) (map[string][]*pkgmodel.Resource, error) {
	reader, ok := ds.(interface {
		GetDesiredOwnership(string) (map[string]pkgmodel.OwnedMembers, error)
	})
	if !ok {
		return observed, nil
	}
	result := maps.Clone(observed)
	labels := map[string]bool{}
	for _, stack := range forma.Stacks {
		labels[stack.Label] = true
	}
	for _, r := range forma.Resources {
		labels[r.Stack] = true
	}
	for label := range labels {
		records, err := reader.GetDesiredOwnership(label)
		if err != nil {
			return nil, err
		}
		rows := slices.Clone(observed[label])
		result[label] = rows
		for i, r := range rows {
			record, ok := records[r.Ksuid]
			if !ok {
				continue
			}
			copy := *r
			if record == nil {
				copy.OwnedMembers = nil
			} else {
				copy.OwnedMembers = maps.Clone(record)
				for path, entry := range copy.OwnedMembers {
					entry.Members = slices.Clone(entry.Members)
					copy.OwnedMembers[path] = entry
				}
			}
			rows[i] = &copy
		}
	}
	return result, nil
}
