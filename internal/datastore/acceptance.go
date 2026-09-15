// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"fmt"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
)

// ValidateAcceptanceContributions prevents ambiguous desired baselines. Ordinary
// multi-operation updates (including replacement) keep their existing semantics.
func ValidateAcceptanceContributions(updates []resource_update.ResourceUpdate) error {
	seen := make(map[string]bool)
	for _, update := range updates {
		id := update.DesiredState.Ksuid
		acceptance := update.IsAcceptance()
		if previous, ok := seen[id]; ok && (previous || acceptance) {
			return fmt.Errorf("conflicting acceptance contribution for resource %s", id)
		}
		seen[id] = acceptance
	}
	return nil
}
