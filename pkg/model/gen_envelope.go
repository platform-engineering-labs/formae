// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"

	"github.com/tidwall/gjson"
)

// GenObject is one $gen occurrence found in a property document, in its
// authored (pre-translation) shape: {"$gen":true,"$label":…,"$stack":…,
// "$output":…,"$visibility":"Opaque"}. Path is the resolver's dotted-path
// walk convention (array elements addressed by index), mirroring
// ResolvableObject.Path for $res.
type GenObject struct {
	Path   string
	Label  string
	Stack  string
	Output string
}

// FindGenObjectsFromProperties traverses a properties document and finds
// every object with $gen: true. It mirrors FindResolvablesFromProperties:
// the walk returns as soon as it recognizes a $gen node rather than
// descending into the envelope's own fields, and it does not scan string
// contents (a $gen framed inside an interpolated string is invisible here —
// see ScanEmbedSpans for that case).
func FindGenObjectsFromProperties(properties json.RawMessage) []GenObject {
	var genObjects []GenObject
	result := gjson.ParseBytes(properties)
	findGenObjectsRecursive("", result, &genObjects)
	return genObjects
}

func findGenObjectsRecursive(basePath string, value gjson.Result, genObjects *[]GenObject) {
	if value.IsObject() {
		if IsGenObject(value) {
			*genObjects = append(*genObjects, GenObject{
				Path:   basePath,
				Label:  value.Get("$label").String(),
				Stack:  value.Get("$stack").String(),
				Output: value.Get("$output").String(),
			})
			return
		}

		value.ForEach(func(key, val gjson.Result) bool {
			newPath := key.String()
			if basePath != "" {
				newPath = basePath + "." + key.String()
			}
			findGenObjectsRecursive(newPath, val, genObjects)
			return true
		})
	} else if value.IsArray() {
		value.ForEach(func(key, val gjson.Result) bool {
			newPath := key.String()
			if basePath != "" {
				newPath = basePath + "." + key.String()
			}
			findGenObjectsRecursive(newPath, val, genObjects)
			return true
		})
	}
}

// IsGenObject checks if a JSON object is a generator reference ($gen: true).
// The flag is present on both the authored shape ($label/$stack/$output) and
// the translated shape ($generator/$output) — unlike $res/$ref, $gen does not
// change key on translation, only its sibling fields do.
func IsGenObject(value gjson.Result) bool {
	if !value.IsObject() {
		return false
	}

	genField := value.Get("$gen")
	return genField.Exists() && genField.Bool()
}

// GenGeneratorKSUID returns the resolved generator KSUID from a translated
// $gen envelope ({"$gen":true,"$generator":"<ksuid>",...}), or "" for an
// authored envelope that has not been translated yet (or for a non-$gen
// value).
func GenGeneratorKSUID(value gjson.Result) string {
	if !IsGenObject(value) {
		return ""
	}
	return value.Get("$generator").String()
}

// KnownGeneratorOutputs enumerates every output name a currently supported
// generator type can produce. PasswordGenerator is the only arm today, and
// its only output is "value" (see PasswordOutputs in the PKL schema); a
// $gen naming any other output is rejected at translation.
var KnownGeneratorOutputs = map[string]bool{
	"value": true,
}

// GeneratorKey identifies a generator by the pair an authored $gen envelope
// names it with: its label and the stack it belongs to. Mirrors TripletKey
// for resources, minus the type dimension a generator does not have.
type GeneratorKey struct {
	Label string
	Stack string
}

// MissingGenerator names one $gen occurrence that could not be resolved to a
// live generator, or whose $output the target generator does not produce.
type MissingGenerator struct {
	Label  string
	Stack  string
	Output string
}

// GeneratorIdentity is controller state for one generator: its stable KSUID
// and the generation it currently holds. Deliberately kept off Generator so
// it can never participate in desired-config equality.
//
// GenerationSpec's bytes are NOT canonical: Postgres and Aurora store it as
// JSONB, which normalizes key order and whitespace on write, so what comes
// back can differ byte-for-byte from what AdvanceGeneration was given. Parse
// it; never byte-compare or hash it against the spec that was drawn.
//
// Defined here (rather than internal/datastore) so the resource_update
// package — which must not import internal/datastore, to avoid a cycle
// (internal/datastore imports resource_update for ResourceUpdate) — can
// still express a generator lookup that returns it. internal/datastore
// aliases its GeneratorIdentity to this type.
type GeneratorIdentity struct {
	ID             string          // the generator's stable KSUID
	GenerationID   string          // "" until a generation has been drawn
	GenerationSpec json.RawMessage // the spec that generation was drawn under; nil when GenerationID is ""
}
