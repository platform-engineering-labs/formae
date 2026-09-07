// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package transformations

import (
	"fmt"
	"strings"
)

// maxCandidateCollectionSegments bounds how many collection positions a patch
// path may carry before the candidate set is capped. Every position doubles the
// set, and real patch paths carry none or one or two, so 5 (32 candidates) is
// far above anything the generators emit while still bounding the pathological
// case instead of letting it grow without limit.
const maxCandidateCollectionSegments = 5

// jsonPointer is a decoded RFC 6901 pointer.
//
// Segments are kept decoded and typed rather than as a normalized string
// because the two cannot be recovered from one another: "" (the whole document)
// and "/" (a property whose name is the empty string) collapse to the same
// thing under split-and-rejoin, and "/a//b" and "/a/" are valid pointers
// addressing empty-string keys rather than malformed input.
type jsonPointer struct {
	// Root reports the whole-document pointer, which has no segments at all.
	Root     bool
	Segments []string
}

// decodeJSONPointer decodes an RFC 6901 pointer. A patch document is
// agent-generated, but several producers write one and none of them can
// faithfully encode a key containing "/", "~" or a dot by string substitution,
// so provenance is not a substitute for decoding correctly on a boundary that
// decides what stays secret.
func decodeJSONPointer(pointer string) (jsonPointer, error) {
	if pointer == "" {
		return jsonPointer{Root: true}, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return jsonPointer{}, fmt.Errorf("json pointer %q does not start with %q", pointer, "/")
	}

	tokens := strings.Split(pointer[1:], "/")
	segments := make([]string, 0, len(tokens))
	for _, token := range tokens {
		segment, err := unescapePointerToken(token)
		if err != nil {
			return jsonPointer{}, fmt.Errorf("json pointer %q: %w", pointer, err)
		}
		segments = append(segments, segment)
	}
	return jsonPointer{Segments: segments}, nil
}

// unescapePointerToken resolves "~1" to "/" and "~0" to "~", in that order, so
// "~01" decodes to "~1" rather than to "/".
func unescapePointerToken(token string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(token); i++ {
		if token[i] != '~' {
			b.WriteByte(token[i])
			continue
		}
		if i+1 >= len(token) {
			return "", fmt.Errorf("token %q ends with an unescaped %q", token, "~")
		}
		switch token[i+1] {
		case '0':
			b.WriteByte('~')
		case '1':
			b.WriteByte('/')
		default:
			return "", fmt.Errorf("token %q contains invalid escape %q", token, token[i:i+2])
		}
		i++
	}
	return b.String(), nil
}

// name is the hint name this prefix would match as a whole — the accumulated
// keys joined the way a schema emits a nested hint.
func (p prefix) name() string {
	return strings.Join(p.steps, ".")
}

// newPrefix builds a prefix from its keys.
func newPrefix(steps []string) prefix {
	p := prefix{steps: steps}
	if len(steps) > 0 {
		p.path = p.name() + "."
	}
	return p
}

// candidatePrefixes returns every reading of a decoded patch path that could
// correspond to a hint name, and whether the candidate set had to be capped.
//
// Hint names are index-free, so a segment sitting in a collection position may
// be either part of the name or an index that the name omits. Trying only
// "elide every index" and "retain every index" is not enough: for
// /accounts/0/webhooks/1/password against the hint accounts.0.webhooks.password
// the reading that matches retains the first and elides the second, and neither
// extreme finds it — a plaintext leak. So every combination is generated,
// bounded by maxCandidateCollectionSegments.
func candidatePrefixes(segments []string) ([]prefix, bool) {
	var collectionPositions []int
	for i, s := range segments {
		if isCollectionPosition(s) {
			collectionPositions = append(collectionPositions, i)
		}
	}

	if len(collectionPositions) > maxCandidateCollectionSegments {
		return []prefix{newPrefix(elideAll(segments, collectionPositions))}, true
	}

	candidates := make([]prefix, 0, 1<<len(collectionPositions))
	for mask := 0; mask < 1<<len(collectionPositions); mask++ {
		retained := make(map[int]bool, len(collectionPositions))
		for bit, pos := range collectionPositions {
			if mask&(1<<bit) != 0 {
				retained[pos] = true
			}
		}
		steps := make([]string, 0, len(segments))
		for i, s := range segments {
			if isCollectionPosition(s) && !retained[i] {
				continue
			}
			steps = append(steps, s)
		}
		candidates = append(candidates, newPrefix(steps))
	}
	return candidates, false
}

func elideAll(segments []string, collectionPositions []int) []string {
	elided := make(map[int]bool, len(collectionPositions))
	for _, pos := range collectionPositions {
		elided[pos] = true
	}
	steps := make([]string, 0, len(segments))
	for i, s := range segments {
		if elided[i] {
			continue
		}
		steps = append(steps, s)
	}
	return steps
}

// isCollectionPosition reports whether a segment addresses a position in a
// collection rather than naming a property: an array index, or the JSON Patch
// append token. An empty segment is a valid property name, not an index.
func isCollectionPosition(segment string) bool {
	if segment == "-" {
		return true
	}
	if segment == "" {
		return false
	}
	for i := 0; i < len(segment); i++ {
		if segment[i] < '0' || segment[i] > '9' {
			return false
		}
	}
	return true
}
