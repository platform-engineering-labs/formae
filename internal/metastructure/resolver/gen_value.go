// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resolver

import (
	"encoding/json"
	"fmt"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// SetGenValues writes value into the $value field of the $gen envelope at
// each of paths, and changes nothing else. It is the generator sibling of
// setRefValue/resolveReference: those resolve a $ref, this delivers a drawn
// value, and neither ever replaces the envelope it writes into.
//
// Writing INSIDE the envelope is the whole point, not an implementation
// detail. A translated $gen always carries $visibility:"Opaque", and that
// marker is the only reason the persist path hashes the value at rest.
// Replacing the envelope with the bare drawn string would strip the marker
// along with it and persist a live credential in cleartext in the resource's
// properties. The plugin-format conversion that runs at the provider boundary
// is what unwraps the envelope to the scalar a plugin receives, and it is the
// only thing that should.
//
// A path that is absent, or that holds something other than a generator
// envelope, is an error rather than an overwrite: paths come from a walk of
// this same document, so either condition means the caller and the document
// have diverged, and writing a credential over an arbitrary node would put
// plaintext somewhere nothing marked opaque. Errors name the path only —
// never the value.
func SetGenValues(properties json.RawMessage, paths []string, value string) (json.RawMessage, error) {
	result := string(properties)

	for _, path := range paths {
		node := gjson.Get(result, path)
		if !node.Exists() {
			return nil, fmt.Errorf("cannot deliver a generated value: no property at %q", path)
		}
		if !pkgmodel.IsGenObject(node) {
			return nil, fmt.Errorf("cannot deliver a generated value: the property at %q is not a generator reference", path)
		}

		envelope := make(map[string]any)
		node.ForEach(func(key, val gjson.Result) bool {
			envelope[key.String()] = val.Value()
			return true
		})
		envelope["$value"] = value

		encoded, err := json.Marshal(envelope)
		if err != nil {
			return nil, fmt.Errorf("cannot deliver a generated value: failed to encode the envelope at %q", path)
		}

		updated, err := sjson.SetRaw(result, path, string(encoded))
		if err != nil {
			return nil, fmt.Errorf("cannot deliver a generated value: failed to write the envelope at %q", path)
		}
		result = updated
	}

	return json.RawMessage(result), nil
}
