// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/provenance"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// referenceOccurrence is one reference envelope found in a properties
// document, with the consumer-side dotted path it lives at.
type referenceOccurrence struct {
	Path     string
	Envelope gjson.Result
}

// collectReferenceEnvelopes walks a properties document and returns every
// reference envelope ($ref, $res, or $gen shaped) with its dotted
// destination path, in the resolver's walk convention (array elements
// addressed by index).
func collectReferenceEnvelopes(properties json.RawMessage) []referenceOccurrence {
	var out []referenceOccurrence
	var walk func(prefix string, node gjson.Result)
	walk = func(prefix string, node gjson.Result) {
		switch {
		case node.IsObject():
			if node.Get("$ref").Exists() || node.Get("$res").Bool() || node.Get("$gen").Bool() {
				out = append(out, referenceOccurrence{Path: prefix, Envelope: node})
				return
			}
			node.ForEach(func(key, val gjson.Result) bool {
				child := key.String()
				if prefix != "" {
					child = prefix + "." + child
				}
				walk(child, val)
				return true
			})
		case node.IsArray():
			i := 0
			node.ForEach(func(_, val gjson.Result) bool {
				child := prefix + "." + itoa(i)
				if prefix == "" {
					child = itoa(i)
				}
				walk(child, val)
				i++
				return true
			})
		}
	}
	walk("", gjson.ParseBytes(properties))
	return out
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [8]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

// destinationIsCreateOnly reports whether the consumer-side destination path
// falls on or under a createOnly-hinted schema field (dotted-prefix match,
// the schema hint convention).
func destinationIsCreateOnly(schema pkgmodel.Schema, destinationPath string) bool {
	for _, field := range schema.CreateOnly() {
		if destinationPath == field || strings.HasPrefix(destinationPath, field+".") || strings.HasPrefix(field, destinationPath+".") {
			return true
		}
	}
	return false
}

// buildProvenanceRecords computes the per-occurrence provenance records for
// one consumer: desired vs stored envelopes, the resolver's opaque answers,
// and the ordered classification rules. Stable records are additionally
// marked on the resolvable properties so reference flattening suppresses
// their op at the mint site. Returns nil when the consumer has no reference
// occurrences.
func buildProvenanceRecords(
	desired, stored json.RawMessage,
	answers resolver.ResolvableProperties,
	schema pkgmodel.Schema,
	force bool,
) []OccurrenceRecord {
	occurrences := collectReferenceEnvelopes(desired)
	if len(occurrences) == 0 {
		return nil
	}
	storedParsed := gjson.ParseBytes(stored)

	var records []OccurrenceRecord
	for _, occ := range occurrences {
		desiredID, desiredOK := NormalizeOccurrenceIdentity(occ.Envelope, nil, nil)
		storedEnvelope := storedParsed.Get(occ.Path)
		storedID, storedOK := NormalizeOccurrenceIdentity(storedEnvelope, nil, nil)

		rec := OccurrenceRecord{
			DestinationPath:  occ.Path,
			DesiredIdentity:  desiredID,
			StoredIdentity:   storedID,
			HasStoredWritten: storedOK && storedEnvelope.Get("$value").Exists(),
		}
		if !desiredOK {
			// Fail closed: an unnormalizable desired envelope is never stable.
			rec.Class = OccurrenceDeferredUpdate
			records = append(records, rec)
			continue
		}
		if storedOK {
			if rf := storedEnvelope.Get("$resolvedFrom").String(); provenance.Valid(rf) {
				rec.WrittenProvenance = rf
			}
			if storedEnvelope.Get("$value").Exists() {
				sv := storedEnvelope.Get("$value")
				if storedEnvelope.Get("$hashed").Bool() {
					rec.WrittenDigest = provenance.FromStored(sv.String())
				} else if sv.Type == gjson.String {
					rec.WrittenDigest = provenance.DigestOfString(sv.String())
				} else {
					rec.WrittenDigest = provenance.DigestOfJSON(sv.Raw)
				}
			}
		}

		var answer resolver.SourceAnswer
		if desiredOK {
			answer, _ = answers.Answer(desiredID.Ksuid, desiredID.PropertyPath)
			rec.SourceRootDigest = answer.SourceRootDigest
		}

		leafDigest := func() (string, bool) {
			if desiredID.JSONPath == "" || answer.SourceRaw() == "" {
				return "", false
			}
			extracted, err := resolver.ExtractJSONPath(answer.SourceRaw(), desiredID.JSONPath)
			if err != nil {
				return "", false
			}
			return provenance.DigestOfString(extracted), true
		}

		ClassifyOccurrence(&rec, answer.Opaque, destinationIsCreateOnly(schema, occ.Path), force, leafDigest)
		if rec.Class == OccurrenceStable {
			answers.SuppressStableAt(occ.Path)
		} else if answer.Opaque && rec.HasStoredWritten {
			// The classification requires this occurrence to plan, but its
			// unresolved reference flattens to an empty string that the
			// top-level empty-value filter would drop as PKL rendering noise.
			// The mark exempts it. Non-opaque occurrences and first
			// declarations stay with normal diff semantics.
			answers.MarkConvergeAt(occ.Path)
		}
		records = append(records, rec)
	}
	return records
}
