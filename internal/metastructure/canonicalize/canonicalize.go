// Package canonicalize provides format-keyed canonicalizers for serialized
// structured String fields (JSON first), so core can compare a field's content
// rather than its byte serialization. See PLA-196.
package canonicalize

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Canonicalizer maps a raw serialized value to a stable canonical form.
type Canonicalizer func(raw string) (string, error)

var canonicalizers = map[string]Canonicalizer{
	"json": canonicalizeJSON,
}

// IsRegistered reports whether a canonicalizer exists for format.
func IsRegistered(format string) bool {
	_, ok := canonicalizers[format]
	return ok
}

// Canonicalize returns the canonical form of raw under the named format. It
// returns an error for an unregistered format or for input that cannot be
// safely canonicalized (invalid JSON, duplicate object keys). Callers treat any
// error as "leave the value as-is and compare raw".
func Canonicalize(format, raw string) (string, error) {
	c, ok := canonicalizers[format]
	if !ok {
		return "", fmt.Errorf("canonicalize: no canonicalizer registered for format %q", format)
	}
	return c(raw)
}

// canonicalizeJSON parses raw as a single JSON document and re-serializes it to
// a stable form: object keys sorted (json.Marshal sorts map keys), whitespace
// compacted, HTML escaping applied (Go default — symmetric on both sides),
// array order preserved, and numeric literals preserved via UseNumber (no
// float64 round-trip). Duplicate object keys are rejected.
func canonicalizeJSON(raw string) (string, error) {
	if err := rejectDuplicateKeys(raw); err != nil {
		return "", err
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", fmt.Errorf("canonicalize json: %w", err)
	}
	if dec.More() {
		return "", fmt.Errorf("canonicalize json: unexpected trailing content")
	}
	out, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("canonicalize json: %w", err)
	}
	return string(out), nil
}

// rejectDuplicateKeys errors if any object in raw has a duplicate key. It walks
// tokens with an explicit nesting stack: '{' pushes an object frame (a fresh
// seen-set + an expect-key toggle); inside an object, key and value tokens
// alternate, so the toggle tells a key apart from a string value; a container in
// value position flips the parent back to expect-key as it is consumed.
func rejectDuplicateKeys(raw string) error {
	type frame struct {
		inObject  bool
		expectKey bool
		seen      map[string]struct{}
	}
	var stack []*frame

	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.UseNumber()
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("canonicalize json: %w", err)
		}

		top := func() *frame {
			if len(stack) == 0 {
				return nil
			}
			return stack[len(stack)-1]
		}

		if d, ok := tok.(json.Delim); ok {
			switch d {
			case '{', '[':
				// A container in value position consumes the parent's value slot.
				if t := top(); t != nil && t.inObject && !t.expectKey {
					t.expectKey = true
				}
				if d == '{' {
					stack = append(stack, &frame{inObject: true, expectKey: true, seen: map[string]struct{}{}})
				} else {
					stack = append(stack, &frame{inObject: false})
				}
			case '}', ']':
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
			}
			continue
		}

		t := top()
		if t == nil || !t.inObject {
			continue // top-level scalar, or an array element
		}
		if t.expectKey {
			key, _ := tok.(string)
			if _, dup := t.seen[key]; dup {
				return fmt.Errorf("canonicalize json: duplicate key %q", key)
			}
			t.seen[key] = struct{}{}
			t.expectKey = false
		} else {
			// Just consumed a scalar value → next token is a key.
			t.expectKey = true
		}
	}
	return nil
}
