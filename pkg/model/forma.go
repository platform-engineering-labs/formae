// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
)

type Forma struct {
	Description Description       `json:"Description"`
	Properties  map[string]Prop   `json:"Properties,omitempty"`
	Stacks      []Stack           `json:"Stacks,omitempty"`
	Targets     []Target          `json:"Targets,omitempty"`
	Resources   []Resource        `json:"Resources,omitempty"`
	Policies    []json.RawMessage `json:"Policies,omitempty"`   // Standalone policies
	Generators  []json.RawMessage `json:"Generators,omitempty"` // Generators, keyed to a stack by their own Stack field
}

type Prop struct {
	Default any    `json:"Default,omitempty"`
	Value   any    `json:"Value,omitempty"`
	Flag    string `json:"Flag,omitempty"`
	Type    string `json:"Type,omitempty"`
}

func (f *Forma) ToJSON() string {
	result, _ := json.Marshal(f)

	return string(result)
}

func (f *Forma) SingleStackLabel() string {
	if len(f.Stacks) != 1 {
		return "default"
	}

	return f.Stacks[0].Label
}

// SplitByStack splits a Forma into multiple Forma instances, one per stack
func (f *Forma) SplitByStack() []Forma {
	// Pointer required to dynamically change properties
	stacks := make(map[string]*Forma)
	result := make([]Forma, 0)

	for _, resource := range f.Resources {
		_, ok := stacks[resource.Stack]
		if ok {
			stacks[resource.Stack].Resources = append(stacks[resource.Stack].Resources, resource)
		} else {
			var stack *Stack
			for _, s := range f.Stacks {
				if s.Label == resource.Stack {
					stack = &s
				}
			}

			if stack != nil {
				stacks[resource.Stack] = &Forma{
					Properties: f.Properties,
					Stacks:     []Stack{*stack},
					Resources:  []Resource{resource},
				}
			} else {
				// Not present in forma.Stacks - create a minimal stack
				stacks[resource.Stack] = &Forma{
					Properties: f.Properties,
					Stacks:     []Stack{{Label: resource.Stack}},
					Resources:  []Resource{resource},
				}
			}
		}
	}

	// Generators are raw JSON (like Policies), so their Stack has to be read
	// out rather than accessed as a Go field. Unlike Policies, a generator
	// names its own stack directly (mirroring Resource.Stack), so — unlike
	// Policies, which this function drops entirely today — each generator is
	// routed to the stack it names.
	for _, raw := range f.Generators {
		var header struct {
			Stack string `json:"Stack"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			continue // malformed generator; nothing sane to route it to
		}

		if existing, ok := stacks[header.Stack]; ok {
			existing.Generators = append(existing.Generators, raw)
			continue
		}

		var stack *Stack
		for _, s := range f.Stacks {
			if s.Label == header.Stack {
				stack = &s
			}
		}

		if stack != nil {
			stacks[header.Stack] = &Forma{
				Properties: f.Properties,
				Stacks:     []Stack{*stack},
				Generators: []json.RawMessage{raw},
			}
		} else {
			// Not present in forma.Stacks - create a minimal stack
			stacks[header.Stack] = &Forma{
				Properties: f.Properties,
				Stacks:     []Stack{{Label: header.Stack}},
				Generators: []json.RawMessage{raw},
			}
		}
	}

	for _, value := range stacks {
		result = append(result, *value)
	}

	return result
}

func FormaFromResources(resources []*Resource) *Forma {
	f := &Forma{
		Stacks:    make([]Stack, 0),
		Targets:   make([]Target, 0),
		Resources: make([]Resource, 0),
	}

	for _, res := range resources {
		if res.Stack != "" {
			found := false
			for _, stack := range f.Stacks {
				if stack.Label == res.Stack {
					found = true
					break
				}
			}
			if !found {
				f.Stacks = append(f.Stacks, Stack{
					Label: res.Stack,
				})
			}
		}
		if res.Target != "" {
			found := false
			for _, target := range f.Targets {
				if target.Label == res.Target {
					found = true
					break
				}
			}
			if !found {
				f.Targets = append(f.Targets, Target{
					Label: res.Target,
				})
			}
		}

		f.Resources = append(f.Resources, *res)
	}

	return f
}
