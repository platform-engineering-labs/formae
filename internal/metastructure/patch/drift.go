// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package patch

import (
	"encoding/json"
	"fmt"

	"github.com/platform-engineering-labs/jsonpatch"
)

// DriftPatch computes the JSON-patch document describing the diff between old
// and current cloud state. It is used to populate the PatchDocument field on a
// ResourceModification so the CLI can show which properties drifted.
//
// Unlike GeneratePatch (which filters by schema, strips empty collections,
// etc.), DriftPatch performs a plain exact-match diff: it reports every field
// that changed, was added, or was removed.
func DriftPatch(old, current json.RawMessage) (json.RawMessage, error) {
	collections := jsonpatch.Collections{
		EntitySets: jsonpatch.EntitySets{},
		Arrays:     []jsonpatch.Path{},
		Atomics:    []jsonpatch.Path{},
	}
	ignoredFields := []jsonpatch.Path{}
	strategy := jsonpatch.PatchStrategyExactMatch

	ops, err := jsonpatch.CreatePatch(old, current, collections, ignoredFields, strategy)
	if err != nil {
		return nil, fmt.Errorf("failed to create drift patch: %w", err)
	}

	result, err := json.Marshal(ops)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal drift patch operations: %w", err)
	}

	return result, nil
}
