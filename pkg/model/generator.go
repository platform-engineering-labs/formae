// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"fmt"
)

// Generator is a source of a generated value that secrets will later
// reference. It is neither a resource nor a policy: it has no target, no
// NativeID, no provider and no Read, so it is never discovered and never
// drifts. It belongs to a stack, exactly as a resource does.
type Generator interface {
	GetLabel() string
	GetType() string
	GetStack() string
	SetStack(stack string)
	GetStackID() string
	SetStackID(id string)
	// GetID/SetID carry the generator's own KSUID, assigned during command
	// translation (mirroring resource_update.assignKSUIDs' handling of
	// pkgmodel.Resource.Ksuid) so a resource property that references a
	// generator declared in the SAME command embeds the exact KSUID the
	// generator's own persisted row will carry, rather than one CreateGenerator
	// invents independently. "" until translation assigns one.
	GetID() string
	SetID(id string)
	// GetRotation returns the cadence on which this generator draws a fresh
	// value, or nil when it declares none. A generator with no cadence draws
	// only when an apply needs a value; nothing rotates it on its own.
	GetRotation() *RotationSpec
	// GetAlias returns the generator's previous label, if any. Identity is
	// the datastore row's KSUID, not the label, so a generator that is
	// renamed (its label changed) needs some way to say "this is still the
	// same generator" — Alias is that: an apply that finds no live generator
	// at the current label falls back to a lookup by Alias, in the same
	// stack, and renames the matched row in place instead of deleting the
	// old one and creating a new one. Mirrors formae.Resource.Alias.
	GetAlias() string
}

// RotationSpec is the cadence on which a generator draws a fresh value.
// Mirrors the PKL RotationSpec.render() output.
//
// EverySeconds has no zero-value meaning: PKL requires `every`, so a spec that
// reaches here always names an interval. A non-positive interval is a spec
// nothing authored through the schema can produce, and the rotation scheduler
// treats it as no cadence at all rather than as "rotate continuously".
type RotationSpec struct {
	EverySeconds int `json:"EverySeconds"`
}

// PasswordGenerator produces a random password value. Fields mirror the
// PKL PasswordGenerator.render() output. Type is not a field: it is a
// constant discriminator, injected by MarshalJSON, so there is exactly one
// place that says what type this generator is.
type PasswordGenerator struct {
	Label   string `json:"Label"`
	Stack   string `json:"Stack,omitempty"`
	StackID string `json:"-"` // Set during processing, not from PKL
	ID      string `json:"-"` // Set during processing (translation), not from PKL
	Alias   string `json:"Alias,omitempty"`
	// Rotation is omitted when absent rather than marshalled as null: the
	// marshalled spec is what a drawn generation is recorded under, so a key
	// that appears only as null is one more way two identical specs can
	// differ.
	Rotation                *RotationSpec `json:"Rotation,omitempty"`
	Length                  int           `json:"Length"`
	Uppercase               bool          `json:"Uppercase"`
	Lowercase               bool          `json:"Lowercase"`
	Digits                  bool          `json:"Digits"`
	Symbols                 bool          `json:"Symbols"`
	ExcludeCharacters       string        `json:"ExcludeCharacters,omitempty"`
	RequireEachIncludedType bool          `json:"RequireEachIncludedType"`
}

func (g *PasswordGenerator) GetLabel() string      { return g.Label }
func (g *PasswordGenerator) GetType() string       { return "password" }
func (g *PasswordGenerator) GetStack() string      { return g.Stack }
func (g *PasswordGenerator) SetStack(stack string) { g.Stack = stack }
func (g *PasswordGenerator) GetStackID() string    { return g.StackID }
func (g *PasswordGenerator) SetStackID(id string)  { g.StackID = id }
func (g *PasswordGenerator) GetID() string         { return g.ID }
func (g *PasswordGenerator) SetID(id string)       { g.ID = id }
func (g *PasswordGenerator) GetAlias() string      { return g.Alias }

func (g *PasswordGenerator) GetRotation() *RotationSpec { return g.Rotation }

// MarshalJSON injects the "Type": "password" discriminator that
// ParseGenerator dispatches on, so callers never set Type by hand and there
// is no way for the marshalled Type to disagree with GetType().
func (g *PasswordGenerator) MarshalJSON() ([]byte, error) {
	type alias PasswordGenerator
	return json.Marshal(struct {
		Type string `json:"Type"`
		alias
	}{
		Type:  g.GetType(),
		alias: alias(*g),
	})
}

// ParseGenerator parses a single generator from JSON, dispatching on the
// discriminated Type field the same way ParsePolicy does.
func ParseGenerator(raw json.RawMessage) (Generator, error) {
	var header struct {
		Type string `json:"Type"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, fmt.Errorf("failed to parse generator type: %w", err)
	}

	switch header.Type {
	case "password":
		var g PasswordGenerator
		if err := json.Unmarshal(raw, &g); err != nil {
			return nil, fmt.Errorf("failed to parse password generator: %w", err)
		}
		return &g, nil
	default:
		return nil, fmt.Errorf("unknown generator type: %s", header.Type)
	}
}

// ParseGenerators parses multiple generators from JSON.
func ParseGenerators(rawGenerators []json.RawMessage) ([]Generator, error) {
	generators := make([]Generator, 0, len(rawGenerators))
	for i, raw := range rawGenerators {
		generator, err := ParseGenerator(raw)
		if err != nil {
			return nil, fmt.Errorf("failed to parse generator at index %d: %w", i, err)
		}
		generators = append(generators, generator)
	}
	return generators, nil
}
