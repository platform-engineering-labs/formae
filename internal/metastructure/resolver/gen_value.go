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
// generatorKsuid is the generator the value was drawn for, and every envelope
// written must name it. The caller selects paths by walking the same document
// and matching that ksuid, so this looks redundant — it is not. The walk
// addresses nodes by a dot-joined path, and a map key that itself contains a
// dot produces a path that resolves somewhere else entirely (or nowhere).
// Where it resolves onto a different generator's envelope, matching on the
// $gen marker alone would deliver one generator's credential into another
// generator's destination. Re-checking the identity at the write is what
// makes that a refusal instead.
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
// A stale $hashed marker is dropped as the value is written. An envelope can
// arrive here carrying the stored digest of the value the destination already
// holds, marked $hashed; the value being written is live plaintext, and the
// persist transformer skips re-hashing anything already marked hashed, so
// leaving the marker would persist a live credential in cleartext while
// claiming it is hashed. This mirrors what mergeResObject does when it adopts
// a fresh value over a stored digest.
//
// A path that is absent, or that holds something other than a generator
// envelope, is an error rather than an overwrite: paths come from a walk of
// this same document, so either condition means the caller and the document
// have diverged, and writing a credential over an arbitrary node would put
// plaintext somewhere nothing marked opaque. Errors name the path only —
// never the value.
// Each occurrence receives the output its envelope names ($output; absent
// means "value", the single-output arms' only name). An occurrence naming an
// output the draw did not produce is a hard error naming the path and the
// output: translation validates $output against the union across generator
// kinds, so a destination bound to the wrong KIND's output is only catchable
// here, where the draw's actual outputs are in hand. Skipping it would leave
// an undrawn envelope to dispatch; substituting another output would hand a
// provider the wrong credential with nothing anywhere erroring.
func SetGenValues(properties json.RawMessage, generatorKsuid string, paths []string, values map[string]string) (json.RawMessage, error) {
	result := string(properties)

	for _, path := range paths {
		node := gjson.Get(result, path)
		if !node.Exists() {
			return nil, fmt.Errorf("cannot deliver a generated value: no property at %q", path)
		}
		if !pkgmodel.IsGenObject(node) {
			return nil, fmt.Errorf("cannot deliver a generated value: the property at %q is not a generator reference", path)
		}
		if got := pkgmodel.GenGeneratorKSUID(node); got != generatorKsuid {
			return nil, fmt.Errorf("cannot deliver a generated value: the generator reference at %q names %q, not %q", path, got, generatorKsuid)
		}

		output := node.Get("$output").String()
		if output == "" {
			output = "value"
		}
		value, ok := values[output]
		if !ok {
			return nil, fmt.Errorf("cannot deliver a generated value: the generator reference at %q names output %q, which this generator's draw does not produce", path, output)
		}

		envelope := make(map[string]any)
		node.ForEach(func(key, val gjson.Result) bool {
			envelope[key.String()] = val.Value()
			return true
		})
		envelope["$value"] = value
		delete(envelope, "$hashed")

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
