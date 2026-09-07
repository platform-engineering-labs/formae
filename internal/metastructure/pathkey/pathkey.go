// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package pathkey holds the one escaping rule for gjson/sjson paths built out
// of resource data.
//
// Resource properties are arbitrary JSON: a map key may contain any character,
// including the dots, wildcards and modifiers that gjson and sjson read as path
// syntax. A Kubernetes annotation key such as "objectset.rio.cattle.io/applied"
// used raw as a path addresses a nested object tree, so a read misses and a
// write explodes the key into that tree beside the literal one. Every path
// assembled from data-derived keys is therefore escaped segment by segment at
// construction, and every consumer of such a path uses that single escaped
// form.
//
// The rule is the union of the two engines' path grammars: gjson.Escape covers
// gjson's, and a colon is escaped on top of it because sjson reads a colon at
// the start of a path segment as its force marker while gjson has no such rule.
// pkg/model, a separately versioned module that cannot import this package,
// carries its own unexported implementation of the same rule so that the SDK
// surface gains no new exported API; a parity test pins the two against each
// other.
//
// Known limitation: an empty map key is not addressable. Escaping it yields "",
// and an empty gjson path denotes the document root, so its segment cannot be
// distinguished from no segment at all.
package pathkey

import (
	"strings"

	"github.com/tidwall/gjson"
)

// Escape renders a literal JSON map key as a single gjson/sjson path segment.
// Keys free of path syntax are returned unchanged, so escaping at construction
// leaves ordinary paths byte-identical.
func Escape(key string) string {
	escaped := gjson.Escape(key)
	// gjson.Escape leaves colons alone: they carry no meaning in a gjson path.
	// sjson reads one at the start of a segment as its force marker, so it has
	// to go. Escaping every colon rather than only a leading one keeps Escape a
	// function of the key alone, independent of where the segment lands.
	if strings.IndexByte(escaped, ':') < 0 {
		return escaped
	}
	return strings.ReplaceAll(escaped, ":", `\:`)
}

// Join escapes each literal key and joins them into a path.
func Join(segments ...string) string {
	if len(segments) == 0 {
		return ""
	}
	escaped := make([]string, len(segments))
	for i, segment := range segments {
		escaped[i] = Escape(segment)
	}
	return strings.Join(escaped, ".")
}

// Split is the inverse of Join: it splits on unescaped dots and unescapes each
// segment back to the literal key it names. On a path built without escaping it
// behaves as strings.Split(path, "."), so a consumer migrating to it keeps its
// current behavior on the plain-key paths it handles today.
func Split(path string) []string {
	var (
		segments []string
		current  strings.Builder
	)
	for i := 0; i < len(path); i++ {
		switch path[i] {
		case '\\':
			// An escape consumes the character after it. A trailing backslash
			// has nothing to escape, so it stands for itself.
			if i+1 < len(path) {
				i++
				current.WriteByte(path[i])
			} else {
				current.WriteByte('\\')
			}
		case '.':
			segments = append(segments, current.String())
			current.Reset()
		default:
			current.WriteByte(path[i])
		}
	}
	return append(segments, current.String())
}
