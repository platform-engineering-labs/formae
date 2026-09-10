// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package metastructure

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/blugelabs/bluge"
	querystr "github.com/blugelabs/query_string"
	"github.com/platform-engineering-labs/formae/internal/constants"

	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ExtractDesiredStacks returns complete accepted declarations for explicitly
// selected managed stacks. Inventory is never a fallback desired declaration.
// This read certificate expires with the read; it does not authorize an apply.
func (m *Metastructure) ExtractDesiredStacks(query string) (*pkgmodel.Forma, error) {
	labels, err := desiredStackSelection(query)
	if err != nil {
		return nil, err
	}
	seed := &pkgmodel.Forma{}
	for _, label := range labels {
		seed.Stacks = append(seed.Stacks, pkgmodel.Stack{Label: label})
	}
	ds := newPlanningDatastore(m.Datastore, seed)
	for attempt := 0; attempt < 16; attempt++ {
		var result *pkgmodel.Forma
		_, err = ds.certify(func() error { var e error; result, e = extractDesiredStacks(ds, labels); return e })
		if errors.Is(err, errPlanningScopeExpanded) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return result, nil
	}
	return nil, fmt.Errorf("desired extraction dependency scope did not settle")
}

func desiredStackSelection(query string) ([]string, error) {
	invalid := func(reason string) ([]string, error) { return nil, apimodel.InvalidQueryError{Reason: reason} }
	if strings.TrimSpace(query) == "" {
		return invalid("desired extraction requires explicit complete stack selection")
	}
	root, err := querystr.ParseQueryString(query, querystr.QueryStringOptions{})
	if err != nil {
		return invalid(err.Error())
	}
	selected := map[string]bool{}
	var visit func(bluge.Query) error
	visit = func(q bluge.Query) error {
		switch node := q.(type) {
		case *bluge.MatchQuery:
			if node.Field() != "stack" || node.Match() == "" || node.Match() == constants.UnmanagedStack {
				return fmt.Errorf("desired extraction accepts only complete managed stack literals")
			}
			selected[node.Match()] = true
		case *bluge.MatchPhraseQuery:
			if node.Field() != "stack" || node.Phrase() == "" || node.Phrase() == constants.UnmanagedStack || node.Slop() != 0 {
				return fmt.Errorf("desired extraction accepts only complete managed stack literals")
			}
			selected[node.Phrase()] = true
		case *bluge.BooleanQuery:
			if len(node.MustNots()) > 0 || len(node.Musts()) > 1 || (len(node.Musts()) > 0 && len(node.Shoulds()) > 0) {
				return fmt.Errorf("use space-separated complete stack selectors")
			}
			for _, child := range append(node.Musts(), node.Shoulds()...) {
				if err := visit(child); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("desired extraction requires literal stack selectors; partial filters are unsafe")
		}
		return nil
	}
	if err := visit(root); err != nil {
		return invalid(err.Error())
	}
	if len(selected) == 0 {
		return invalid("no stacks selected")
	}
	labels := make([]string, 0, len(selected))
	for label := range selected {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return labels, nil
}

func extractDesiredStacks(ds *planningDatastore, labels []string) (*pkgmodel.Forma, error) {
	result := &pkgmodel.Forma{Extraction: &pkgmodel.ExtractionContext{}, Resources: []pkgmodel.Resource{}, Targets: []pkgmodel.Target{}}
	targets := map[string]bool{}
	policies := map[string]bool{}
	for _, label := range labels {
		stack, err := ds.GetStackByLabel(label)
		if err != nil {
			return nil, err
		}
		if stack == nil || stack.ID == "" {
			return nil, apimodel.InvalidQueryError{Reason: fmt.Sprintf("managed stack %q does not exist", label)}
		}
		stack = ownPlanningValue(stack)
		stack.Policies = nil
		inline, err := ds.GetDesiredInlinePoliciesForStack(stack.ID)
		if err != nil {
			return nil, err
		}
		for _, policy := range inline {
			raw, err := marshalDesiredPolicy(policy)
			if err != nil {
				return nil, err
			}
			stack.Policies = append(stack.Policies, raw)
		}
		attached, err := ds.GetAttachedPolicyLabelsForStack(label)
		if err != nil {
			return nil, err
		}
		sort.Strings(attached)
		for _, policyLabel := range attached {
			raw, err := json.Marshal(map[string]string{"$ref": "policy://" + policyLabel})
			if err != nil {
				return nil, err
			}
			stack.Policies = append(stack.Policies, raw)
			if !policies[policyLabel] {
				policy, err := ds.GetStandalonePolicy(policyLabel)
				if err != nil {
					return nil, err
				}
				raw, err = marshalDesiredPolicy(policy)
				if err != nil {
					return nil, err
				}
				result.Policies = append(result.Policies, raw)
				policies[policyLabel] = true
			}
		}
		result.Stacks = append(result.Stacks, *stack)
		result.Extraction.CompleteStacks = append(result.Extraction.CompleteStacks, pkgmodel.Stack{Label: stack.Label, ID: stack.ID})
		declarations, err := ds.GetResourcesAtLastReconcile(label)
		if err != nil {
			return nil, err
		}
		for _, snapshot := range declarations {
			if snapshot.Declaration == nil || snapshot.StackID != stack.ID {
				return nil, fmt.Errorf("desired declaration for %s has no matching stable stack identity", snapshot.KSUID)
			}
			r := ownPlanningValue(*snapshot.Declaration)
			if r.Stack != label || r.Type == "" || r.Label == "" || !json.Valid(r.Properties) {
				return nil, fmt.Errorf("incomplete desired declaration for %s", snapshot.KSUID)
			}
			r.Properties, err = filterCoOwnedProperties(r.Properties, r.Schema, r.OwnedMembers)
			if err != nil {
				return nil, fmt.Errorf("cannot extract owned declaration %s: %w", r.Label, err)
			}
			r.OwnedMembers = nil
			r.ReadOnlyProperties = nil
			r.PatchDocument = nil
			r.Version = ""
			result.Resources = append(result.Resources, r)
			targets[r.Target] = true
		}
		generators, err := ds.LoadDesiredGeneratorsByStack(label)
		if err != nil {
			return nil, err
		}
		for _, generator := range generators {
			if generator.GetStack() != label {
				return nil, fmt.Errorf("generator stack identity mismatch")
			}
			raw, err := json.Marshal(generator)
			if err != nil {
				return nil, err
			}
			if _, err = pkgmodel.ParseGenerator(raw); err != nil {
				return nil, err
			}
			result.Generators = append(result.Generators, raw)
		}
	}
	for _, label := range sortedScope(targets) {
		if label == "" {
			return nil, fmt.Errorf("desired resource has no target")
		}
		found, err := ds.LoadTargetsByLabels([]string{label})
		if err != nil {
			return nil, err
		}
		if len(found) != 1 || found[0] == nil {
			return nil, fmt.Errorf("desired target %q is missing", label)
		}
		target := ownPlanningValue(*found[0])
		target.ExecutionIncarnation = ""
		result.Targets = append(result.Targets, target)
	}
	if err := translateDesiredReferences(ds, result); err != nil {
		return nil, err
	}
	return result, nil
}
func marshalDesiredPolicy(policy pkgmodel.Policy) (json.RawMessage, error) {
	if policy == nil {
		return nil, fmt.Errorf("missing desired policy")
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		return nil, err
	}
	if _, err := pkgmodel.ParsePolicy(raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// Reverse translation resolves declared identities deliberately. Current
// inventory may have a patch rename or belong to a reused stack incarnation.
func translateDesiredReferences(ds *planningDatastore, forma *pkgmodel.Forma) error {
	return translateDesiredReferencesInScope(ds, forma, forma.Stacks)
}

// A partial command delta declares contribution identities, not whole stacks.
// Missing dependencies supply rendering context only; they never join Resources
// or managed Generators. Complete extraction retains strict omission checks.
func translatePartialDesiredReferences(ds *planningDatastore, forma *pkgmodel.Forma) error {
	return translateDesiredReferencesInScope(ds, forma, nil)
}

func translateDesiredReferencesInScope(ds *planningDatastore, forma *pkgmodel.Forma, completeStacks []pkgmodel.Stack) error {
	declared := map[string]pkgmodel.Resource{}
	loaded := map[string]bool{}
	selected := map[string]bool{}
	for _, s := range completeStacks {
		loaded[s.Label] = true
		selected[s.Label] = true
	}
	for _, r := range forma.Resources {
		declared[r.Ksuid] = r
	}
	generators := map[pkgmodel.GeneratorKey]bool{}
	for _, raw := range forma.Generators {
		g, e := pkgmodel.ParseGenerator(raw)
		if e != nil {
			return e
		}
		generators[pkgmodel.GeneratorKey{Stack: g.GetStack(), Label: g.GetLabel()}] = true
	}
	reference := func(id string) (pkgmodel.Resource, error) {
		if r, ok := declared[id]; ok {
			return r, nil
		}
		observed, e := ds.GetResourceObservation(id)
		if e != nil {
			return pkgmodel.Resource{}, e
		}
		if observed == nil {
			return pkgmodel.Resource{}, fmt.Errorf("missing desired reference %s", id)
		}
		stack, e := ds.GetStackByLabel(observed.Stack)
		if e != nil {
			return pkgmodel.Resource{}, e
		}
		if stack == nil || observed.StackID == "" || observed.StackID != stack.ID {
			return pkgmodel.Resource{}, fmt.Errorf("ambiguous desired reference incarnation %s", id)
		}
		if !loaded[stack.Label] {
			snapshots, e := ds.GetResourcesAtLastReconcile(stack.Label)
			if e != nil {
				return pkgmodel.Resource{}, e
			}
			for _, s := range snapshots {
				if s.Declaration == nil || s.StackID != stack.ID {
					return pkgmodel.Resource{}, fmt.Errorf("missing desired reference declaration %s", s.KSUID)
				}
				if _, recorded := declared[s.KSUID]; !recorded {
					declared[s.KSUID] = *s.Declaration
				}
			}
			loaded[stack.Label] = true
		}
		if r, ok := declared[id]; ok {
			return r, nil
		}
		return pkgmodel.Resource{}, fmt.Errorf("reference %s has no eligible desired declaration", id)
	}
	var walk func(any) error
	document := func(raw json.RawMessage) (json.RawMessage, error) {
		if len(raw) == 0 {
			return raw, nil
		}
		var value any
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.UseNumber()
		if !json.Valid(raw) {
			return nil, fmt.Errorf("invalid desired JSON")
		}
		if e := dec.Decode(&value); e != nil {
			return nil, e
		}
		if e := walk(value); e != nil {
			return nil, e
		}
		return json.Marshal(value)
	}
	walk = func(value any) error {
		switch n := value.(type) {
		case []any:
			for _, child := range n {
				if e := walk(child); e != nil {
					return e
				}
			}
		case map[string]any:
			if ref, ok := n["$ref"].(string); ok {
				uri := pkgmodel.FormaeURI(ref)
				id := uri.KSUID()
				if id == "" {
					return fmt.Errorf("invalid desired resource reference %q", ref)
				}
				r, e := reference(id)
				if e != nil {
					return e
				}
				delete(n, "$ref")
				n["$res"] = true
				n["$label"] = r.Label
				n["$stack"] = r.Stack
				n["$type"] = r.Type
				n["$property"] = uri.PropertyPath()
			}
			if n["$gen"] == true {
				var generator pkgmodel.Generator
				if id, ok := n["$generator"].(string); ok && id != "" {
					var e error
					generator, e = generatorLookup(ds)(id)
					if e != nil {
						return e
					}
					if generator == nil {
						return fmt.Errorf("missing desired generator %s", id)
					}
					actual, e := ds.GetGeneratorIdentity(generator.GetLabel(), generator.GetStack())
					if e != nil {
						return e
					}
					if actual.ID != id {
						byID, e := neverDrawnGeneratorsByKsuid(ds)
						if e != nil {
							return e
						}
						generator = byID[id]
						if generator == nil {
							return fmt.Errorf("desired generator identity %s no longer exists", id)
						}
					}
					n["$label"] = generator.GetLabel()
					n["$stack"] = generator.GetStack()
				}
				label, _ := n["$label"].(string)
				stack, _ := n["$stack"].(string)
				output, _ := n["$output"].(string)
				if label == "" || stack == "" || output == "" {
					return fmt.Errorf("incomplete desired generator reference")
				}
				key := pkgmodel.GeneratorKey{Label: label, Stack: stack}
				if !generators[key] {
					if generator == nil {
						var e error
						generator, e = ds.GetGenerator(label, stack)
						if e != nil {
							return e
						}
					}
					if generator == nil {
						return fmt.Errorf("missing desired generator %s/%s", stack, label)
					}
					owner, e := ds.GetStackByLabel(stack)
					if e != nil {
						return e
					}
					if owner == nil {
						return fmt.Errorf("missing generator owner stack %s", stack)
					}
					raw, e := json.Marshal(generator)
					if e != nil {
						return e
					}
					if _, e = pkgmodel.ParseGenerator(raw); e != nil {
						return e
					}
					if selected[stack] {
						return fmt.Errorf("desired generator declaration omitted from selected stack")
					}
					forma.Extraction.ReferenceGenerators = append(forma.Extraction.ReferenceGenerators, raw)
					generators[key] = true
				}
				delete(n, "$generator")
				if strategy, ok := n["$strategy"]; ok && strategy != nil && strategy != "" && strategy != "Update" {
					return fmt.Errorf("unsupported desired reference strategy %v", strategy)
				}
				delete(n, "$strategy")
			}
			if n["$res"] == true || n["$gen"] == true {
				for _, key := range []string{"$value", "$hashed", "$applied", "$resolvedFrom"} {
					delete(n, key)
				}
			}
			if n["$embed"] == true {
				if template, ok := n["$template"].(string); ok {
					spans, e := pkgmodel.ScanEmbedSpans(template)
					if e != nil {
						return e
					}
					for i := len(spans) - 1; i >= 0; i-- {
						span := spans[i]
						raw, e := document(json.RawMessage(span.EnvelopeJSON))
						if e != nil {
							return e
						}
						template = template[:span.Start] + pkgmodel.FrameEnvelope(string(raw)) + template[span.End:]
					}
					n["$template"] = template
				}
			}
			for _, child := range n {
				if e := walk(child); e != nil {
					return e
				}
			}
		}
		return nil
	}
	for i := range forma.Resources {
		raw, e := document(forma.Resources[i].Properties)
		if e != nil {
			return e
		}
		forma.Resources[i].Properties = raw
	}
	for i := range forma.Targets {
		raw, e := document(forma.Targets[i].Config)
		if e != nil {
			return e
		}
		forma.Targets[i].Config = raw
	}
	return nil
}
