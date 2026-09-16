// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package patch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/platform-engineering-labs/jsonpatch"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ReadEquivalent reports whether a freshly read property document carries the
// same state as the stored one, under the schema's collection semantics: an
// unkeyed collection is a set, a field hinted as an ordered array compares
// positionally, and an entity set matches its elements by key. It is the same
// contract patch generation applies, so a difference that would never produce
// a patch operation is not a change worth a new version either.
//
// The diff is taken in both directions because patch generation keeps keys
// that are absent from the target document, so a key deleted between the two
// documents only surfaces when it is the target that carries it. Empty and
// null documents compare as empty objects. A document shape the diff cannot
// handle is reported as an error, never as equal.
func ReadEquivalent(stored, read json.RawMessage, schema pkgmodel.Schema) (equal bool, err error) {
	a, b := emptyAsObject(stored), emptyAsObject(read)
	if identical, err := structurallyEqual(a, b); err != nil {
		return false, err
	} else if identical {
		return true, nil
	}
	collections := collectionSemanticsFromFieldHints(schema.Hints)
	defer func() {
		if r := recover(); r != nil {
			equal, err = false, fmt.Errorf("compare read with stored properties: %v", r)
		}
	}()
	forward, err := jsonpatch.CreatePatch(a, b, collections, nil, jsonpatch.PatchStrategyExactMatch)
	if err != nil {
		return false, err
	}
	if len(forward) > 0 {
		return false, nil
	}
	backward, err := jsonpatch.CreatePatch(b, a, collections, nil, jsonpatch.PatchStrategyExactMatch)
	if err != nil {
		return false, err
	}
	return len(backward) == 0, nil
}

// structurallyEqual is the fast path for the common case of an unchanged
// read, and the authority for values the diff does not compare the way
// equality requires: the diff reports two nulls as different, so an atomic
// field carrying a null would otherwise count as changed on every read.
func structurallyEqual(a, b []byte) (bool, error) {
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		return false, err
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		return false, err
	}
	return reflect.DeepEqual(va, vb), nil
}

func emptyAsObject(doc json.RawMessage) []byte {
	trimmed := bytes.TrimSpace(doc)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return []byte("{}")
	}
	return trimmed
}
