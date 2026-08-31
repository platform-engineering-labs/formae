// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"reflect"
	"strings"
)

// Canonical character-class alphabets. These are a contract with the
// eval-time validation in internal/schema/pkl/schema/formae.pkl: a spec that
// PKL accepts must be one the value drawer can satisfy, so both sides draw
// from the same sets. The PKL cross-check test evaluates a spec excluding
// exactly these characters and requires eval to fail.
const (
	UppercaseChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	LowercaseChars = "abcdefghijklmnopqrstuvwxyz"
	DigitChars     = "0123456789"
	SymbolChars    = "!@#$%^&*()-_=+[]{}<>?/|~"
)

// alphabet returns the characters a spec can actually draw, i.e. the union of
// its enabled classes with excludeCharacters removed.
func (p *PasswordGenerator) alphabet() map[rune]bool {
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
	add(p.Uppercase, UppercaseChars)
	add(p.Lowercase, LowercaseChars)
	add(p.Digits, DigitChars)
	add(p.Symbols, SymbolChars)
	return out
}

// guaranteedClasses returns the classes a value drawn under this spec is known
// to contain. Without requireEachIncludedType a draw guarantees nothing.
func (p *PasswordGenerator) guaranteedClasses() map[string]bool {
	if !p.RequireEachIncludedType {
		return map[string]bool{}
	}
	return p.enabledClasses()
}

func (p *PasswordGenerator) enabledClasses() map[string]bool {
	out := map[string]bool{}
	for name, enabled := range map[string]bool{
		"uppercase": p.Uppercase, "lowercase": p.Lowercase,
		"digits": p.Digits, "symbols": p.Symbols,
	} {
		if enabled {
			out[name] = true
		}
	}
	return out
}

// GenerationSatisfies reports whether a generation drawn under `drawn` is still
// acceptable under `desired`.
//
// This is a static relation between two specs, never an inspection of the
// value: a generated value is hashed at rest and cannot be read back. It
// therefore answers the conservative question "could EVERY value `drawn`
// produces satisfy `desired`", and so returns false in some cases where the
// particular value drawn would in fact have passed. Regenerating is the safe
// direction of that error.
func GenerationSatisfies(drawn, desired Generator) bool {
	// A typed nil on either side is a generator that could not be resolved,
	// not a spec. The check must come before the GetType comparison below,
	// not inside the arm: GetType is a method call on the nil pointer, and an
	// arm whose GetType reads a field would dereference it.
	if drawn == nil || desired == nil || isTypedNil(drawn) || isTypedNil(desired) {
		return false
	}
	if drawn.GetType() != desired.GetType() {
		return false
	}
	switch d := drawn.(type) {
	case *PasswordGenerator:
		w, ok := desired.(*PasswordGenerator)
		if !ok {
			return false
		}
		// Length is exact, so any change invalidates the drawn value.
		if d.Length != w.Length {
			return false
		}
		// Every character the drawn value could contain must remain drawable.
		wide := w.alphabet()
		for c := range d.alphabet() {
			if !wide[c] {
				return false
			}
		}
		// Every class the desired spec now demands must have been guaranteed.
		guaranteed := d.guaranteedClasses()
		for name := range w.guaranteedClasses() {
			if !guaranteed[name] {
				return false
			}
		}
		return true
	default:
		// An unknown arm is never provably satisfied.
		return false
	}
}

// isTypedNil reports whether g is a non-nil interface holding a nil pointer.
// Reflection rather than a type switch on purpose: this stays correct for
// every arm, including ones added after it was written, which a switch would
// silently stop covering.
func isTypedNil(g Generator) bool {
	v := reflect.ValueOf(g)
	return v.Kind() == reflect.Ptr && v.IsNil()
}
