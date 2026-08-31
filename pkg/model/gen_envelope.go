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
	Path  string
	Label string
	Stack string
	// Generator is the resolved generator KSUID a TRANSLATED envelope
	// carries, and "" for an authored one. It is read off the node during
	// the walk rather than looked back up by Path, because Path is
	// dot-joined and a map key containing a dot cannot be addressed by it.
	Generator string
	Output    string
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
				Path:      basePath,
				Label:     value.Get("$label").String(),
				Stack:     value.Get("$stack").String(),
				Generator: value.Get("$generator").String(),
				Output:    value.Get("$output").String(),
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

// resourcePropertiesField is the member of a stored resource document that
// holds its properties (Resource.Properties' JSON name). It is the only part
// of the document a $gen binding can live in.
const resourcePropertiesField = "Properties"

// BindsGenerator reports whether the given resource document binds any
// property to the generator with this KSUID through a translated $gen
// envelope. It is the authoritative answer to "is this row a destination of
// that generator": the datastores use their indexes only to narrow the
// candidate set and confirm every candidate here, so all backends agree
// regardless of how their SQL happens to match.
//
// It takes the whole stored resource document, because that is what a
// resources row holds, and scans only that document's properties member. That
// is the same reach as the draw rule (which reads DesiredState.Properties), so
// the reverse index and the rule that decides what a draw rotates agree by
// construction: a $gen appearing anywhere else in the row — read-only
// properties, a patch document — names no destination and must not be able to
// refuse an apply. Each backend's SQL stays deliberately broader than this, so
// a candidate it matched for another reason is dropped here rather than
// missed.
//
// The walk is shared with FindGenObjectsFromProperties rather than duplicated,
// which is why an authored (untranslated) envelope, whose generator is named
// by label and stack, matches nothing here.
//
// An empty generator KSUID binds nothing, so authored envelopes cannot match
// it by accident.
func BindsGenerator(document []byte, generatorKsuid string) bool {
	if len(document) == 0 || generatorKsuid == "" {
		return false
	}

	properties := gjson.GetBytes(document, resourcePropertiesField)
	if !properties.Exists() {
		return false
	}

	for _, gen := range FindGenObjectsFromProperties([]byte(properties.Raw)) {
		if gen.Generator == generatorKsuid {
			return true
		}
	}

	return false
}

// KnownGeneratorOutputs enumerates every output name a currently supported
// generator type can produce. PasswordGenerator is the only arm today, and
// its only output is "value" (see PasswordOutputs in the PKL schema); a
// $gen naming any other output is rejected at translation.
//
// This is a flat union across every arm because there is only one arm. A
// second generator type introduces an output name that is valid for IT but
// not for others (e.g. a second arm's own "value"-shaped or differently
// named output could collide or diverge in meaning), so checking $output
// against this union alone stops being sufficient: the resolution site would
// need to also resolve the referenced generator's TYPE (via genKeyToKsuid's
// declared generator, or the datastore identity) and validate $output
// against THAT type's own output set, not the global union. Widening this
// map to add a key is not enough on its own at that point.
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
