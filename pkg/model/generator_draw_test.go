// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package model_test

import (
	cryptorand "crypto/rand"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// realSource draws from the actual CSPRNG. Used for tests that only assert
// invariants (length, alphabet membership, class presence) which the
// algorithm guarantees by construction, never for a distribution assertion.
func realSource(b []byte) (int, error) { return cryptorand.Read(b) }

// expectedAlphabet reconstructs the drawable character set from the spec's
// exported fields, independent of the unexported alphabet() helper the
// production code uses, so the test is a check on Draw's contract rather than
// a mirror of its implementation.
func expectedAlphabet(p *pkgmodel.PasswordGenerator) map[rune]bool {
	out := map[rune]bool{}
	add := func(enabled bool, chars string) {
		if !enabled {
			return
		}
		for _, c := range chars {
			if !strings.ContainsRune(p.ExcludeCharacters, c) {
				out[c] = true
			}
		}
	}
	add(p.Uppercase, pkgmodel.UppercaseChars)
	add(p.Lowercase, pkgmodel.LowercaseChars)
	add(p.Digits, pkgmodel.DigitChars)
	add(p.Symbols, pkgmodel.SymbolChars)
	return out
}

func classOf(c rune) string {
	switch {
	case strings.ContainsRune(pkgmodel.UppercaseChars, c):
		return "uppercase"
	case strings.ContainsRune(pkgmodel.LowercaseChars, c):
		return "lowercase"
	case strings.ContainsRune(pkgmodel.DigitChars, c):
		return "digits"
	case strings.ContainsRune(pkgmodel.SymbolChars, c):
		return "symbols"
	default:
		return ""
	}
}

func TestDraw_CorrectLength(t *testing.T) {
	spec := pw(func(*pkgmodel.PasswordGenerator) {})
	value, err := pkgmodel.Draw(spec, realSource)
	require.NoError(t, err)
	assert.Len(t, value, spec.Length)
}

func TestDraw_EveryCharacterIsDrawable(t *testing.T) {
	spec := pw(func(*pkgmodel.PasswordGenerator) {})
	value, err := pkgmodel.Draw(spec, realSource)
	require.NoError(t, err)

	allowed := expectedAlphabet(spec)
	for _, c := range value {
		assert.True(t, allowed[c], "character %q is not drawable under the spec", c)
	}
}

func TestDraw_RequireEachIncludedType_EveryEnabledClassIsPresent(t *testing.T) {
	spec := pw(func(g *pkgmodel.PasswordGenerator) {
		g.Uppercase, g.Lowercase, g.Digits, g.Symbols = true, true, true, true
		g.RequireEachIncludedType = true
		g.Length = 32
	})
	value, err := pkgmodel.Draw(spec, realSource)
	require.NoError(t, err)

	present := map[string]bool{}
	for _, c := range value {
		present[classOf(c)] = true
	}
	assert.True(t, present["uppercase"])
	assert.True(t, present["lowercase"])
	assert.True(t, present["digits"])
	assert.True(t, present["symbols"])
}

// sequenceSource replays a fixed byte sequence, one byte per call (Draw only
// ever requests a single byte at a time), so a test can steer the algorithm
// through a specific rejection path deterministically. calls counts how many
// times the source was read.
func sequenceSource(bytes []byte) (src pkgmodel.ByteSource, calls *int) {
	i := 0
	n := 0
	calls = &n
	src = func(b []byte) (int, error) {
		*calls++
		if i >= len(bytes) {
			return 0, fmt.Errorf("sequenceSource: exhausted after %d bytes", len(bytes))
		}
		copied := copy(b, bytes[i:i+1])
		i++
		return copied, nil
	}
	return src, calls
}

// A digits-only alphabet has 10 characters; 256 % 10 == 6, so bytes 250-255
// fall in the modulo-biased tail and must be discarded rather than folded
// with %, which would over-represent '0'-'5'.
func TestDraw_ModuloBiasedByteIsDiscardedAndRedrawn(t *testing.T) {
	spec := pw(func(g *pkgmodel.PasswordGenerator) {
		g.Uppercase, g.Lowercase, g.Digits, g.Symbols = false, false, true, false
		g.RequireEachIncludedType = false
		g.Length = 4
	})
	// First byte (255) is in the biased tail and must be discarded; the
	// second byte (0) is the one actually used for position 0.
	src, calls := sequenceSource([]byte{255, 0, 1, 2, 3})

	value, err := pkgmodel.Draw(spec, src)
	require.NoError(t, err)
	assert.Equal(t, "0123", value)
	assert.Equal(t, 5, *calls, "expected the biased byte to be discarded and a 5th byte drawn")
}

// With Uppercase and Lowercase enabled, requireEachIncludedType, and Length
// 2, a candidate drawn entirely from one class must be discarded in full and
// a fresh candidate drawn, never patched.
func TestDraw_RequireEachIncludedType_DiscardsWholeCandidateOnMissingClass(t *testing.T) {
	spec := pw(func(g *pkgmodel.PasswordGenerator) {
		g.Uppercase, g.Lowercase, g.Digits, g.Symbols = true, true, false, false
		g.RequireEachIncludedType = true
		g.Length = 2
	})
	// Sorted alphabet is A..Z (indices 0-25) then a..z (indices 26-51); 52
	// divides 256 with room to spare (limit 208), so none of these bytes hit
	// the modulo-biased tail and this test exercises only the whole-candidate
	// rejection path.
	//
	// First candidate: byte 0, byte 1 -> "AB", uppercase only -> discarded.
	// Second candidate: byte 0, byte 26 -> "Aa", both classes -> accepted.
	src, calls := sequenceSource([]byte{0, 1, 0, 26})

	value, err := pkgmodel.Draw(spec, src)
	require.NoError(t, err)
	assert.Equal(t, "Aa", value)
	assert.Equal(t, 4, *calls, "expected the first, incomplete candidate to be discarded in full")
}

func drawWithTimeout(t *testing.T, spec pkgmodel.Generator, src pkgmodel.ByteSource) (string, error) {
	t.Helper()
	type result struct {
		value string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		value, err := pkgmodel.Draw(spec, src)
		done <- result{value, err}
	}()
	select {
	case r := <-done:
		return r.value, r.err
	case <-time.After(2 * time.Second):
		t.Fatal("Draw did not return within timeout; it may be looping on an unsatisfiable spec")
		return "", nil
	}
}

func TestDraw_NoClassEnabledReturnsErrorRatherThanHanging(t *testing.T) {
	spec := pw(func(g *pkgmodel.PasswordGenerator) {
		g.Uppercase, g.Lowercase, g.Digits, g.Symbols = false, false, false, false
	})
	value, err := drawWithTimeout(t, spec, realSource)
	require.Error(t, err)
	assert.Empty(t, value)
}

func TestDraw_ExcludeCharactersEmptyingTheOnlyEnabledClassReturnsError(t *testing.T) {
	spec := pw(func(g *pkgmodel.PasswordGenerator) {
		g.Uppercase, g.Lowercase, g.Digits, g.Symbols = true, false, false, false
		g.ExcludeCharacters = pkgmodel.UppercaseChars
	})
	value, err := drawWithTimeout(t, spec, realSource)
	require.Error(t, err)
	assert.Empty(t, value)
}

// requireEachIncludedType demanding more classes than there are positions
// can never be satisfied by any candidate, no matter how many are drawn.
func TestDraw_MoreRequiredClassesThanLengthReturnsErrorRatherThanHanging(t *testing.T) {
	spec := pw(func(g *pkgmodel.PasswordGenerator) {
		g.Uppercase, g.Lowercase, g.Digits, g.Symbols = true, true, false, false
		g.RequireEachIncludedType = true
		g.Length = 1
	})
	value, err := drawWithTimeout(t, spec, realSource)
	require.Error(t, err)
	assert.Empty(t, value)
}

func TestDraw_TypedNilGeneratorReturnsError(t *testing.T) {
	var nilGen *pkgmodel.PasswordGenerator
	var spec pkgmodel.Generator = nilGen
	value, err := pkgmodel.Draw(spec, realSource)
	require.Error(t, err)
	assert.Empty(t, value)
}

func TestDraw_ByteSourceErrorPropagates(t *testing.T) {
	spec := pw(func(*pkgmodel.PasswordGenerator) {})
	boom := fmt.Errorf("byte source unavailable")
	failing := func([]byte) (int, error) { return 0, boom }

	value, err := pkgmodel.Draw(spec, failing)
	require.Error(t, err)
	assert.Empty(t, value)
	assert.ErrorIs(t, err, boom)
}

// Every spec PKL's eval-time validation accepts must be one the drawer can
// satisfy: it never rejects a valid combination of enabled classes and
// requireEachIncludedType at the minimum PKL-accepted length.
func TestDraw_NeverErrorsForAnySpecPKLAccepts(t *testing.T) {
	classCombos := []struct {
		uppercase, lowercase, digits, symbols bool
	}{}
	for mask := 1; mask < 16; mask++ { // skip 0: PKL rejects no class enabled
		classCombos = append(classCombos, struct {
			uppercase, lowercase, digits, symbols bool
		}{
			uppercase: mask&1 != 0,
			lowercase: mask&2 != 0,
			digits:    mask&4 != 0,
			symbols:   mask&8 != 0,
		})
	}

	for _, combo := range classCombos {
		for _, require_ := range []bool{true, false} {
			spec := pw(func(g *pkgmodel.PasswordGenerator) {
				g.Uppercase, g.Lowercase, g.Digits, g.Symbols = combo.uppercase, combo.lowercase, combo.digits, combo.symbols
				g.RequireEachIncludedType = require_
				g.Length = 16 // PKL's minimum accepted length
				g.ExcludeCharacters = ""
			})
			value, err := pkgmodel.Draw(spec, realSource)
			require.NoErrorf(t, err, "combo %+v requireEachIncludedType=%v", combo, require_)
			assert.Len(t, value, 16)
		}
	}
}
