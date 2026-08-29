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
}

// PasswordGenerator produces a random password value. Fields mirror the
// PKL PasswordGenerator.render() output.
type PasswordGenerator struct {
	Type                    string `json:"Type"` // "password"
	Label                   string `json:"Label"`
	Stack                   string `json:"Stack,omitempty"`
	EverySeconds            *int64 `json:"EverySeconds,omitempty"`
	Length                  int    `json:"Length"`
	Uppercase               bool   `json:"Uppercase"`
	Lowercase               bool   `json:"Lowercase"`
	Digits                  bool   `json:"Digits"`
	Symbols                 bool   `json:"Symbols"`
	ExcludeCharacters       string `json:"ExcludeCharacters,omitempty"`
	RequireEachIncludedType bool   `json:"RequireEachIncludedType"`
}

func (g *PasswordGenerator) GetLabel() string      { return g.Label }
func (g *PasswordGenerator) GetType() string       { return "password" }
func (g *PasswordGenerator) GetStack() string      { return g.Stack }
func (g *PasswordGenerator) SetStack(stack string) { g.Stack = stack }

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
