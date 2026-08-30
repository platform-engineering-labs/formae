// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"fmt"
	"sort"
	"strings"
)

// ByteSource yields cryptographically secure random bytes. crypto/rand.Read in
// production; a deterministic source in tests so the rejection paths are
// reachable without relying on chance.
type ByteSource func([]byte) (int, error)

// maxDrawAttempts bounds the requireEachIncludedType rejection loop. Any spec
// PKL accepts is satisfied well within this many attempts (each attempt has
// an overwhelming chance of success); the cap exists only so a defensively
// unsatisfiable spec returns an error instead of hanging an unattended agent.
const maxDrawAttempts = 100_000

// Draw produces one value satisfying the generator's spec, drawing entropy
// from src. It never logs the discarded candidates or the returned value.
func Draw(spec Generator, src ByteSource) (string, error) {
	if spec == nil || isTypedNil(spec) {
		return "", fmt.Errorf("draw: generator is nil")
	}
	switch g := spec.(type) {
	case *PasswordGenerator:
		return drawPassword(g, src)
	default:
		return "", fmt.Errorf("draw: unsupported generator type %q", spec.GetType())
	}
}

func drawPassword(p *PasswordGenerator, src ByteSource) (string, error) {
	if p.Length < 0 {
		return "", fmt.Errorf("draw: %q has a negative length", p.Label)
	}

	alphabetSet := p.alphabet()
	if len(alphabetSet) == 0 {
		return "", fmt.Errorf("draw: %q has no drawable characters", p.Label)
	}

	// alphabet() returns a map, and Go randomizes map iteration order.
	// Sorting makes the byte-to-character mapping deterministic, so a given
	// ByteSource always produces the same value.
	chars := make([]rune, 0, len(alphabetSet))
	for c := range alphabetSet {
		chars = append(chars, c)
	}
	sort.Slice(chars, func(i, j int) bool { return chars[i] < chars[j] })

	// required is the set of classes the returned value must contain. Empty
	// unless requireEachIncludedType is set, in which case satisfiesAll below
	// is trivially true and the loop below runs exactly once.
	required := p.guaranteedClasses()
	if len(required) > p.Length {
		// Pigeonhole: len(required) distinct classes cannot fit in fewer
		// positions than that, no matter how many candidates are drawn.
		return "", fmt.Errorf("draw: %q requires %d classes in a value of length %d", p.Label, len(required), p.Length)
	}

	for attempt := 0; attempt < maxDrawAttempts; attempt++ {
		candidate := make([]rune, p.Length)
		present := map[string]bool{}
		for i := range candidate {
			c, err := drawRune(chars, src)
			if err != nil {
				return "", err
			}
			candidate[i] = c
			present[classOf(c)] = true
		}
		if satisfiesAll(required, present) {
			return string(candidate), nil
		}
		// The candidate is missing a required class: discard it entirely and
		// draw a fresh one. Never patch a character into a position to fix
		// it — that would bias both the position and the class frequencies.
	}
	return "", fmt.Errorf("draw: %q did not satisfy requireEachIncludedType within %d attempts", p.Label, maxDrawAttempts)
}

// classOf reports which canonical alphabet c belongs to, or "" if none (not
// reachable for a character drawn from this generator's own alphabet, since
// that alphabet is built from exactly these four sets).
func classOf(c rune) string {
	switch {
	case strings.ContainsRune(UppercaseChars, c):
		return "uppercase"
	case strings.ContainsRune(LowercaseChars, c):
		return "lowercase"
	case strings.ContainsRune(DigitChars, c):
		return "digits"
	case strings.ContainsRune(SymbolChars, c):
		return "symbols"
	default:
		return ""
	}
}

func satisfiesAll(required, present map[string]bool) bool {
	for name := range required {
		if !present[name] {
			return false
		}
	}
	return true
}

// drawRune draws one unbiased index into chars from src. A byte in the
// modulo-biased tail — the range beyond the largest multiple of len(chars)
// that fits in a byte — is discarded and redrawn rather than folded with %,
// which would over-represent the first 256%len(chars) entries of chars.
func drawRune(chars []rune, src ByteSource) (rune, error) {
	n := len(chars)
	limit := 256 - (256 % n) // chars is never larger than the four alphabets combined, well under 256.
	buf := make([]byte, 1)
	for {
		if _, err := src(buf); err != nil {
			return 0, fmt.Errorf("draw: reading random bytes: %w", err)
		}
		b := int(buf[0])
		if b >= limit {
			continue // biased tail; discard and redraw, never logged.
		}
		return chars[b%n], nil
	}
}
