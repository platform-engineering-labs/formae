// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package patch

import (
	"bytes"
	"encoding/json"

	"github.com/platform-engineering-labs/jsonpatch"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ReadEquivalent reports whether a freshly read property document carries the
// same state as the stored one, under the schema's collection semantics: an
// unkeyed collection is a set, a field hinted as an ordered array compares
// positionally, and an entity set matches its elements by key. It is the same
// contract patch generation applies, so a difference that would never produce
// a patch operation is not a change worth a new version either. Empty and
// null documents compare as empty objects.
func ReadEquivalent(stored, read json.RawMessage, schema pkgmodel.Schema) (bool, error) {
	ops, err := jsonpatch.CreatePatch(emptyAsObject(stored), emptyAsObject(read), collectionSemanticsFromFieldHints(schema.Hints), nil, jsonpatch.PatchStrategyExactMatch)
	if err != nil {
		return false, err
	}
	return len(ops) == 0, nil
}

func emptyAsObject(doc json.RawMessage) []byte {
	trimmed := bytes.TrimSpace(doc)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return []byte("{}")
	}
	return trimmed
}
