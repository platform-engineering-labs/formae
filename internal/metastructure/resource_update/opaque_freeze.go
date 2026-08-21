// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/transformations"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// opaquePreservedSentinel stands in for an opaque property whose desired value
// formae holds only as a stored hash, wherever that document is about to leave
// the agent as DesiredProperties on a plugin Update.
//
// It is the DesiredProperties-side counterpart of opaqueRedactedSentinel, and
// deliberately the same "present but unusable" shape the SDK already documents
// for PriorProperties — a distinct value only so the two sides stay tellable
// apart in a trace. Present-but-unusable rather than omitted: absence on
// DesiredProperties means "no value", so a plugin that rebuilds a whole-document
// write from it — a strategy the SDK permits — would send the document without
// the secret and could clear it. A non-scalar where the schema declares a scalar
// fails that plugin's parse instead, which is a loud failure rather than silent
// data loss.
const opaquePreservedSentinel = `{"$opaque":"preserved"}`

// FreezeUnrecoverableOpaqueValues replaces, in a desired document about to be
// converted for a plugin Update, every opaque leaf whose desired value is a
// stored hash matching prior state with opaquePreservedSentinel.
//
// setOnce is implemented by substituting the STORED value into the desired
// properties, and for an opaque field that stored value is a $hashed envelope.
// The guarded conversion that builds DesiredProperties rightly refuses to send a
// digest as if it were the live secret, so without this every unrelated property
// on the same resource is frozen too: the guard protecting the value blocks the
// whole document.
//
// Deliberately narrower than SuppressUnchangedOpaqueValues: it acts only on
// $hashed values — the ones formae structurally cannot send. An unchanged opaque
// value whose desired side is live plaintext is recoverable and is left exactly
// as it is. A $hashed value that does NOT match prior state means something
// upstream is broken; it is left alone so it still fails the guard loudly.
//
// Inputs are not mutated; a rewritten copy of desired is returned, so the caller
// keeps its durable record of the stored hash.
func FreezeUnrecoverableOpaqueValues(
	prior, desired json.RawMessage,
	priorSchema, desiredSchema pkgmodel.Schema,
	resourceType string,
) (json.RawMessage, error) {
	if len(prior) == 0 || len(desired) == 0 {
		return desired, nil
	}

	// Take the union of both schemas, as the prior-properties redaction does: a
	// hint removed or renamed between prior and desired would otherwise leave a
	// value that was opaque when it was stored unclassified here.
	opaqueNames := transformations.OpaqueFields(desiredSchema, resourceType)
	for name := range transformations.OpaqueFields(priorSchema, resourceType) {
		opaqueNames[name] = true
	}

	priorResult := gjson.ParseBytes(prior)
	desiredResult := gjson.ParseBytes(desired)

	seen := make(map[string]bool)
	var paths []string
	addPath := func(path string) {
		if !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	// Walk BOTH documents: a desired representation that lost its inline
	// $visibility marker still has to classify from the prior side.
	collectOpaqueLeafPaths(desiredResult, opaqueNames, addPath)
	collectOpaqueLeafPaths(priorResult, opaqueNames, addPath)

	out := string(desired)
	for _, path := range paths {
		if !isUnrecoverableStoredValue(desiredResult.Get(path), priorResult.Get(path)) {
			continue
		}
		var err error
		out, err = sjson.SetRaw(out, path, opaquePreservedSentinel)
		if err != nil {
			// The path can contain user-authored map keys, so it stays out of
			// the error: this text reaches an operator-visible failure reason.
			return nil, fmt.Errorf("failed to substitute an unrecoverable opaque value: %w", err)
		}
	}

	return json.RawMessage(out), nil
}

// isUnrecoverableStoredValue reports whether desired holds a stored hash that
// formae cannot turn back into the value the provider expects.
//
// $strategy is compared on neither side: the persist transformer canonicalises a
// missing strategy on an already-hashed envelope, so requiring it to match would
// reintroduce the permanent failure this exists to remove. Any other shape —
// a non-boolean marker, an absent or non-string value, a prior that is not an
// envelope — does not match and falls through to the guard.
func isUnrecoverableStoredValue(desired, prior gjson.Result) bool {
	if !desired.IsObject() || desired.Get("$hashed").Type != gjson.True {
		return false
	}
	desiredValue := desired.Get("$value")
	if desiredValue.Type != gjson.String {
		return false
	}
	if !prior.IsObject() {
		return false
	}
	priorValue := prior.Get("$value")
	return priorValue.Type == gjson.String && priorValue.String() == desiredValue.String()
}

// collectOpaqueLeafPaths reports the gjson path of every opaque leaf in doc.
//
// Two path readings are carried at once because they answer different
// questions. The hint name accumulates by plain concatenation and skips array
// indices, matching how the opaque-path walker resolves a name emitted for a
// property nested in a SubResource — pkl emits those index-free. The gjson path
// carries indices and escapes special characters, so it addresses the leaf that
// actually matched rather than a neighbour the path syntax happens to resolve
// to. A match stops the descent, exactly as the walker does: the matched name is
// the declared secret, whole.
func collectOpaqueLeafPaths(doc gjson.Result, opaqueNames map[string]bool, addPath func(string)) {
	var visit func(path, name string, node gjson.Result)
	visit = func(path, name string, node gjson.Result) {
		// Test the declared name before the inline marker, as the walker does,
		// so a map-shaped secret that happens to carry a $visibility key is
		// never read as an envelope.
		if opaqueNames[name] {
			addPath(path)
			return
		}
		switch {
		case node.IsObject():
			if node.Get("$visibility").String() == "Opaque" {
				addPath(path)
				return
			}
			node.ForEach(func(key, val gjson.Result) bool {
				visit(childPath(path, key.String()), childName(name, key.String()), val)
				return true
			})
		case node.IsArray():
			for i, elem := range node.Array() {
				visit(childPath(path, strconv.Itoa(i)), name, elem)
			}
		}
	}

	doc.ForEach(func(key, val gjson.Result) bool {
		visit(childPath("", key.String()), childName("", key.String()), val)
		return true
	})
}

func childPath(base, key string) string {
	if base == "" {
		return escapeGjsonKey(key)
	}
	return base + "." + escapeGjsonKey(key)
}

func childName(base, key string) string {
	if base == "" {
		return key
	}
	return base + "." + key
}

// gjsonSpecialChars are the characters gjson and sjson read as path syntax
// rather than as part of a key. A key containing one is escaped so it addresses
// itself — "a.b" as a literal key, not the "b" under an "a".
const gjsonSpecialChars = `\.*?#@`

func escapeGjsonKey(key string) string {
	if !strings.ContainsAny(key, gjsonSpecialChars) {
		return key
	}
	var escaped strings.Builder
	escaped.Grow(len(key) + 4)
	for _, r := range key {
		if strings.ContainsRune(gjsonSpecialChars, r) {
			escaped.WriteByte('\\')
		}
		escaped.WriteRune(r)
	}
	return escaped.String()
}
