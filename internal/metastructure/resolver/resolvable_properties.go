// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resolver

import (
	"encoding/json"
	"fmt"
	"log/slog"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/tidwall/gjson"
)

// AnswerKind classifies why a SourceAnswer's value may be trusted, or that it
// carries none yet.
type AnswerKind int

const (
	// AnswerDeferred: not derivable at plan time; execution-time resolution
	// through RemainingResolvables answers it. Zero value on purpose: a
	// missing or forgotten answer must read as the least-trusting kind, never
	// the most.
	AnswerDeferred AnswerKind = iota
	// AnswerResolved: the value is derivable at plan time from effective
	// desired state (a literal this command declares).
	AnswerResolved
	// AnswerStable: the persisted value is valid because nothing this
	// command does moves the source property.
	AnswerStable
)

// SourceAnswer is the resolver's answer for one property on one source
// resource: whether, and why, a value is available at plan time.
type SourceAnswer struct {
	Kind   AnswerKind
	Value  string // Resolved and Stable only
	Opaque bool   // the source property is opaque; consumers of the seam decide what that means
}

// ResolvableProperties is a map of KSUIDs to property names to answers.
// This can include resource.Properties and resource.ReadOnlyProperties
type ResolvableProperties struct {
	props map[string]map[string]SourceAnswer // ksuid -> property -> answer
}

func NewResolvableProperties() ResolvableProperties {
	return ResolvableProperties{
		props: make(map[string]map[string]SourceAnswer),
	}
}

func (p *ResolvableProperties) Add(ksuid, property, value string) {
	p.AddAnswer(ksuid, property, SourceAnswer{Kind: AnswerStable, Value: value})
}

func (p *ResolvableProperties) AddAnswer(ksuid, property string, a SourceAnswer) {
	if _, ok := p.props[ksuid]; !ok {
		p.props[ksuid] = make(map[string]SourceAnswer)
	}
	p.props[ksuid][property] = a
}

func (p *ResolvableProperties) Get(ksuid, property string) (string, bool) {
	if resourceProps, ok := p.props[ksuid]; ok {
		if answer, ok := resourceProps[property]; ok {
			if answer.Kind == AnswerResolved || answer.Kind == AnswerStable {
				return answer.Value, true
			}
		}
	}
	return "", false
}

func (p *ResolvableProperties) Answer(ksuid, property string) (SourceAnswer, bool) {
	if resourceProps, ok := p.props[ksuid]; ok {
		if answer, ok := resourceProps[property]; ok {
			return answer, true
		}
	}
	return SourceAnswer{}, false
}

func LoadResolvablePropertiesFromStacks(resource pkgmodel.Resource, allResources map[string][]*pkgmodel.Resource, effective map[string]json.RawMessage) (ResolvableProperties, error) {
	res := NewResolvableProperties()

	resourcesByKsuid := make(map[string]*pkgmodel.Resource)
	for _, resources := range allResources {
		for _, r := range resources {
			if r.Ksuid != "" {
				resourcesByKsuid[r.Ksuid] = r
			}
		}
	}

	uris := ExtractResolvableURIs(resource)

	for _, uri := range uris {
		ksuid := uri.KSUID()
		propertyPath := uri.PropertyPath()

		targetResource, exists := resourcesByKsuid[ksuid]
		if !exists {
			return res, fmt.Errorf("resource with KSUID %s not found", ksuid)
		}

		if effDoc, declared := effective[ksuid]; declared {
			effVal := gjson.GetBytes(effDoc, propertyPath)
			if effVal.Exists() && !isReferenceEnvelope(effVal) && !containsHashedValue(effVal) &&
				!containsOpaqueVisibility(effVal) && !isSourcePropertyOpaque(targetResource, propertyPath) {
				res.AddAnswer(ksuid, propertyPath, SourceAnswer{Kind: AnswerResolved, Value: ExtractPropertyValue(effVal)})
				continue
			}
			// Reference envelopes, hashed shapes, and opaque sources — persisted,
			// schema-declared, or only ever inline-marked in the desired document
			// itself — fall through to the persisted-row path unchanged: envelopes
			// keep the cached value, opaque and hashed sources keep today's
			// deferral.
		}

		if value, ok := resolvableValueFrom(targetResource.ReadOnlyProperties, propertyPath); ok {
			res.Add(ksuid, propertyPath, value)
			continue
		}

		if value, ok := resolvableValueFrom(targetResource.Properties, propertyPath); ok {
			res.Add(ksuid, propertyPath, value)
			continue
		}

		// Property not available yet — this happens for forward references to
		// new resources whose read-only properties are assigned at creation time,
		// and for a secret, whose value is only ever read live. The value will be
		// resolved at execution time via RemainingResolvables.
		slog.Debug("Skipping unresolvable property (will resolve at execution time)",
			"property", propertyPath,
			"resource", targetResource.Label,
			"ksuid", ksuid)
		continue
	}

	return res, nil
}

// resolvableValueFrom reads propertyPath out of one persisted property
// collection and reports whether it yields a value a reference may resolve to.
//
// Two shapes at rest are NOT values, and admitting either would have a
// reference resolve to something the source does not hold — compared, and on a
// $json reference parsed, as if it were live:
//
//   - a value stored hashed at rest, which is a SHA-256 digest. It is refused
//     wherever it sits, including inside a structure the reference names as a
//     whole, since a digest buried in an object is no more a value than one at
//     the top.
//   - a reference envelope that carries no value of its own, which is what
//     reference-don't-store leaves at rest. Its raw text is not a value either.
//
// Refusing both leaves the reference to execution-time resolution
// (RemainingResolvables), which reads the source live through its plugin — the
// same way the create path resolves it, and the only way a secret is ever read.
func resolvableValueFrom(properties json.RawMessage, propertyPath string) (string, bool) {
	if properties == nil {
		return "", false
	}
	extracted := gjson.GetBytes(properties, propertyPath)
	if !extracted.Exists() {
		return "", false
	}
	if containsHashedValue(extracted) {
		return "", false
	}
	if isReferenceEnvelope(extracted) && !extracted.Get("$value").Exists() {
		return "", false
	}
	return ExtractPropertyValue(extracted), true
}

// containsHashedValue reports whether value is, or contains anywhere within it,
// a value stored hashed at rest.
func containsHashedValue(value gjson.Result) bool {
	if !value.IsObject() && !value.IsArray() {
		return false
	}
	if value.Get("$hashed").Bool() {
		return true
	}
	found := false
	value.ForEach(func(_, child gjson.Result) bool {
		if containsHashedValue(child) {
			found = true
			return false
		}
		return true
	})
	return found
}

// containsOpaqueVisibility reports whether value is, or contains anywhere
// within it, an inline $visibility: Opaque marker. This is the desired
// document's own spelling of opacity — a plain {"$value": ..., "$visibility":
// "Opaque"} envelope the command submits before that property has ever been
// persisted or hashed, so neither a persisted shape check nor the
// schema/known-opaque table sees it. The structural sibling of
// containsHashedValue: same recursive shape, different marker.
func containsOpaqueVisibility(value gjson.Result) bool {
	if !value.IsObject() && !value.IsArray() {
		return false
	}
	if value.Get("$visibility").String() == pkgmodel.VisibilityOpaque {
		return true
	}
	found := false
	value.ForEach(func(_, child gjson.Result) bool {
		if containsOpaqueVisibility(child) {
			found = true
			return false
		}
		return true
	})
	return found
}

// isReferenceEnvelope reports whether value is a reference rather than a value:
// the persisted ($ref) or source ($res) spelling of one.
func isReferenceEnvelope(value gjson.Result) bool {
	if !value.IsObject() {
		return false
	}
	return value.Get("$ref").Exists() || value.Get("$res").Exists()
}

// ExtractPropertyValue extracts the actual value from a gjson.Result.
// If the property is itself a $ref object (nested resolvable), it extracts
// the $value from within it. Otherwise, it returns the string representation.
// This handles the case where a resource's property references another resource's
// property that is itself a reference (e.g., Subnet -> VCN -> Compartment).
func ExtractPropertyValue(extracted gjson.Result) string {
	if extracted.IsObject() && extracted.Get("$value").Exists() {
		return extracted.Get("$value").String()
	}
	return extracted.String()
}
