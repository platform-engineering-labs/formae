// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package provenance defines the canonical digest domain for reference
// resolution provenance: the record of which source value a reference
// resolution came from, compared across plans to decide whether a
// secret-sourced occurrence moved.
//
// The canonical domain deliberately IS the legacy at-rest hash domain
// (pkgmodel.ComputeValueHash over Value.GetStringValue's rendering, with
// values decoded by plain encoding/json into float64 numbers), so digests
// computed here compare equal to the digests source rows already store at
// rest - for scalar strings and structured values alike. Idealizing the
// rendering (json.Number precision, canonical JSON) would break that
// comparability and turn every undeclared-source comparison into unknown.
// The only addition is an explicit domain-version marker, so malformed,
// legacy, or forged values are objectively recognizable and never compared.
package provenance

import (
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// domainPrefix marks a digest as belonging to the current canonical domain.
const domainPrefix = "v1:"

const hexDigestLen = 64

// DigestOfDecoded digests an already-decoded value in the canonical domain.
// The rendering is exactly the legacy persist path's: Value.GetStringValue.
// Use this wherever the value's type is already known; never guess a type by
// parsing content.
func DigestOfDecoded(v any) string {
	rendered := (&pkgmodel.Value{Value: v}).GetStringValue()
	return domainPrefix + pkgmodel.ComputeValueHash(rendered)
}

// DigestOfJSON digests a raw JSON text in the canonical domain, decoding it
// exactly as the persist path decodes properties: plain encoding/json into
// any (numbers become float64, so 1e3 renders as "1000" - identical to the
// legacy at-rest digest of the same document). Input that is not valid JSON
// is digested as the raw string.
func DigestOfJSON(rawJSON string) string {
	var v any
	if err := json.Unmarshal([]byte(rawJSON), &v); err != nil {
		return DigestOfDecoded(rawJSON)
	}
	return DigestOfDecoded(v)
}

// DigestOfString digests a value known to be a string. The content is never
// parsed: a string that happens to look like JSON digests as the string it
// is.
func DigestOfString(s string) string {
	return DigestOfDecoded(s)
}

// FromStored adapts a source row's at-rest digest (unversioned lowercase-hex
// SHA-256) into the canonical domain. Anything that is not exactly a
// 64-character lowercase-hex digest yields "" (unknown), never a comparable
// value.
func FromStored(storedDigest string) string {
	if !isLowerHexDigest(storedDigest) {
		return ""
	}
	return domainPrefix + storedDigest
}

// Valid reports whether d is a well-formed current-domain digest: exactly
// the version marker followed by a 64-character lowercase-hex SHA-256.
// Malformed, legacy, or forged values are never comparable.
func Valid(d string) bool {
	payload, ok := strings.CutPrefix(d, domainPrefix)
	return ok && isLowerHexDigest(payload)
}

func isLowerHexDigest(s string) bool {
	if len(s) != hexDigestLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// UnwrapEffectiveValue is the single type-preserving unwrap through which
// every provenance digest or raw-value computation routes. A value envelope
// ({"$value": ..} carrying $visibility/$strategy/$hashed metadata) yields its
// $value; reference envelopes ($ref/$res) and every other node yield
// themselves. Persistence hashes only an envelope's $value and execution
// unwraps before resolving, so digesting a raw envelope would digest
// metadata and never compare equal to either.
func UnwrapEffectiveValue(node gjson.Result) gjson.Result {
	if !node.IsObject() {
		return node
	}
	if node.Get("$ref").Exists() || node.Get("$res").Exists() {
		return node
	}
	value := node.Get("$value")
	if !value.Exists() {
		return node
	}
	if node.Get("$visibility").Exists() || node.Get("$strategy").Exists() || node.Get("$hashed").Exists() {
		return value
	}
	return node
}
