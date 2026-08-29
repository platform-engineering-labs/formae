// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package patch

import (
	"strings"

	"github.com/tidwall/gjson"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// firstPointerSegment returns the first segment of an RFC 6901 JSON Pointer,
// unescaped (~1 -> /, then ~0 -> ~). ok is false for anything that is not a
// pointer with at least one non-empty segment. Matching fidelity roots by
// first segment (never by string prefix) is what keeps "/Specification" from
// matching a root named "Spec".
func firstPointerSegment(path string) (string, bool) {
	rest, found := strings.CutPrefix(path, "/")
	if !found || rest == "" {
		return "", false
	}
	seg, _, _ := strings.Cut(rest, "/")
	if seg == "" {
		return "", false
	}
	seg = strings.ReplaceAll(seg, "~1", "/")
	seg = strings.ReplaceAll(seg, "~0", "~")
	return seg, true
}

// isKeepableReferenceEnvelope reports whether a desired-side value is a
// reference envelope well-formed enough that its flattened placeholder must
// survive the top-level empty-value drop: either a $ref that parses to a
// formae URI with a KSUID, or a complete $res declaration. $ref wins when
// both appear (the translated form is authoritative). Deliberately stricter
// than occurrence collection: an envelope with no usable reference identity
// is not kept, so a malformed object can never mint a placeholder op.
func isKeepableReferenceEnvelope(value gjson.Result) bool {
	if !value.IsObject() {
		return false
	}
	if ref := value.Get("$ref"); ref.Exists() {
		return ref.Type == gjson.String && pkgmodel.FormaeURI(ref.String()).KSUID() != ""
	}
	if res := value.Get("$res"); res.Exists() {
		if !res.IsBool() || !res.Bool() {
			return false
		}
		for _, key := range []string{"$label", "$type", "$stack", "$property"} {
			member := value.Get(key)
			if member.Type != gjson.String || member.String() == "" {
				return false
			}
		}
		return true
	}
	return false
}

// referenceEnvelopeFields returns the schema fields whose desired pre-flatten
// value is a keepable reference envelope. Absent, malformed, or non-object
// input contributes nothing: the failure direction is always "field dropped
// as today", never "non-envelope kept".
func referenceEnvelopeFields(desiredEnvelopes []byte, schemaFields []string) map[string]bool {
	fields := map[string]bool{}
	if len(desiredEnvelopes) == 0 {
		return fields
	}
	parsed := gjson.ParseBytes(desiredEnvelopes)
	if !parsed.IsObject() {
		return fields
	}
	for _, field := range schemaFields {
		if isKeepableReferenceEnvelope(parsed.Get(field)) {
			fields[field] = true
		}
	}
	return fields
}
