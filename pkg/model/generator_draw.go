// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"fmt"
	"slices"
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

// passwordClasses pairs each character class a PasswordGenerator can enable
// with its canonical alphabet, mirroring the class-by-class walk
// PasswordGenerator's own validated() does in formae.pkl.
var passwordClasses = []struct {
	name    string
	enabled func(*PasswordGenerator) bool
	chars   string
}{
	{"uppercase", func(p *PasswordGenerator) bool { return p.Uppercase }, UppercaseChars},
	{"lowercase", func(p *PasswordGenerator) bool { return p.Lowercase }, LowercaseChars},
	{"digits", func(p *PasswordGenerator) bool { return p.Digits }, DigitChars},
	{"symbols", func(p *PasswordGenerator) bool { return p.Symbols }, SymbolChars},
}

func drawPassword(p *PasswordGenerator, src ByteSource) (string, error) {
	if p.Length <= 0 {
		return "", fmt.Errorf("draw: %q has a non-positive length", p.Label)
	}

	// Walk each enabled class individually, the same way PKL's validated()
	// does, so a class that excludeCharacters empties entirely is caught and
	// named here rather than surfacing 100,000 attempts later as a generic
	// "did not satisfy requireEachIncludedType" with no indication of which
	// class was impossible to draw.
	anyEnabled := false
	for _, class := range passwordClasses {
		if !class.enabled(p) {
			continue
		}
		anyEnabled = true
		if remaining(class.chars, p.ExcludeCharacters) == "" {
			return "", fmt.Errorf("draw: %q excludeCharacters removes every %s character", p.Label, class.name)
		}
	}
	if !anyEnabled {
		return "", fmt.Errorf("draw: %q has no character class enabled", p.Label)
	}

	// alphabet() returns a map, and Go randomizes map iteration order.
	// Sorting makes the byte-to-character mapping deterministic, so a given
	// ByteSource always produces the same value.
	alphabetSet := p.alphabet()
	chars := make([]rune, 0, len(alphabetSet))
	for c := range alphabetSet {
		chars = append(chars, c)
	}
	slices.Sort(chars)

	if len(chars) < 2 {
		// A one-character alphabet is a constant, not a random value: every
		// draw would produce the same string regardless of the entropy spent
		// drawing it. PKL rejects a class emptied entirely (checked above)
		// but not this narrower case, so it is checked here.
		return "", fmt.Errorf("draw: %q has an effective alphabet of only %d character(s)", p.Label, len(chars))
	}

	// required is the set of classes the returned value must contain. Empty
	// unless requireEachIncludedType is set; when it is not set, satisfiesAll
	// below is trivially true on the first attempt and the loop runs exactly
	// once. When it is set, satisfiesAll is what forces a candidate missing a
	// class to be discarded and a fresh one drawn.
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

// remaining returns chars with every character in exclude removed. Mirrors
// PasswordGenerator.remaining() in formae.pkl.
func remaining(chars, exclude string) string {
	var b strings.Builder
	for _, c := range chars {
		if !strings.ContainsRune(exclude, c) {
			b.WriteRune(c)
		}
	}
	return b.String()
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
		read, err := src(buf)
		if err != nil {
			return 0, fmt.Errorf("draw: reading random bytes: %w", err)
		}
		if read != len(buf) {
			return 0, fmt.Errorf("draw: reading random bytes: got %d bytes, want %d", read, len(buf))
		}
		b := int(buf[0])
		if b >= limit {
			continue // biased tail; discard and redraw, never logged.
		}
		return chars[b%n], nil
	}
}
