// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resolver

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/metastructure/provenance"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/tidwall/gjson"
)

// ReferenceCycleError reports that plan-time resolution followed a chain of
// references back onto itself. The cycle is rejected when the lookup runs,
// before any changeset exists, because the execution DAG's cycle detection
// cannot see references that resolve within the lookup itself.
type ReferenceCycleError struct {
	Chain []string // "<ksuid>#/<propertyPath>" hops in traversal order, first repeated hop last
}

func (e ReferenceCycleError) Error() string {
	return fmt.Sprintf("reference cycle detected at plan time: %s", strings.Join(e.Chain, " -> "))
}

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
	// SourceRootDigest is the canonical-domain digest of the source property's
	// whole (pre-extraction) value: the effective desired value when the
	// command declares the source, the stored at-rest digest otherwise. Empty
	// means unknown. Populated for opaque sources only.
	SourceRootDigest string
	// sourceRaw holds the UNWRAPPED effective-desired raw JSON of a declared
	// opaque source, for in-memory $json leaf comparison. Never serialized and
	// never placed in the public string map.
	sourceRaw string
}

// SourceRaw returns the unwrapped effective-desired raw JSON of a declared
// opaque source, or "" when the source is undeclared or non-opaque.
func (a SourceAnswer) SourceRaw() string {
	return a.sourceRaw
}

// ResolvableProperties is a map of KSUIDs to property names to answers.
// This can include resource.Properties and resource.ReadOnlyProperties
type ResolvableProperties struct {
	props map[string]map[string]SourceAnswer // ksuid -> property -> answer
	// suppressed holds consumer-side destination paths whose occurrence was
	// classified provably stable: reference flattening substitutes the stored
	// value on the desired side for these paths so no op is minted. The set
	// is decided by the update generator's provenance classification; absence
	// always means "do not suppress".
	suppressed map[string]bool
	// converging holds consumer-side destination paths whose occurrence the
	// classification requires to plan (moved, repointed, forced, or unknown
	// movement on a mutable destination). An unresolved reference flattens to
	// an empty string, which the top-level empty-value filter treats as PKL
	// rendering noise; these paths are exempted from that drop so the
	// converging op survives. Absence means "no exemption".
	converging map[string]bool
}

func NewResolvableProperties() ResolvableProperties {
	return ResolvableProperties{
		props:      make(map[string]map[string]SourceAnswer),
		suppressed: make(map[string]bool),
		converging: make(map[string]bool),
	}
}

// SuppressStableAt marks a consumer destination path as provably stable.
func (p *ResolvableProperties) SuppressStableAt(destinationPath string) {
	if p.suppressed == nil {
		p.suppressed = make(map[string]bool)
	}
	p.suppressed[destinationPath] = true
}

// StableSuppressedAt reports whether the destination path was classified
// provably stable.
func (p *ResolvableProperties) StableSuppressedAt(destinationPath string) bool {
	return p.suppressed[destinationPath]
}

// MarkConvergeAt marks a consumer destination path as requiring a converging
// update.
func (p *ResolvableProperties) MarkConvergeAt(destinationPath string) {
	if p.converging == nil {
		p.converging = make(map[string]bool)
	}
	p.converging[destinationPath] = true
}

// ConvergeMarkedAt reports whether the destination path was classified as
// requiring a converging update.
func (p *ResolvableProperties) ConvergeMarkedAt(destinationPath string) bool {
	return p.converging[destinationPath]
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

// LoadResolvablePropertiesFromStacks answers, for each resolvable URI on
// resource, whether a value is available at plan time and what it is.
//
// Classification is recursive over the desired reference graph: a declared
// source's effective desired value that is itself a non-opaque reference
// envelope is resolved by following that reference in turn (applying any
// nested $json extraction in memory), so a chain of references converges to
// the value its root will hold after this command in a single pass, however
// many hops deep. An opaque marker anywhere on a hop stops the recursion
// there and keeps the persisted-row fallthrough unchanged. A reference cycle
// is rejected as a ReferenceCycleError naming the chain.
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
		answer, err := classifySourceProperty(uri.KSUID(), uri.PropertyPath(), resourcesByKsuid, effective, nil)
		if err != nil {
			return res, err
		}
		if answer.Kind == AnswerDeferred {
			// Property not available yet — this happens for forward references to
			// new resources whose read-only properties are assigned at creation
			// time, and for a secret, whose value is only ever read live. The
			// value will be resolved at execution time via RemainingResolvables.
			// The answer is STORED anyway: Get still refuses to hand out a
			// value, but classification metadata (opacity, source digest)
			// must reach downstream consumers of the seam.
			slog.Debug("Deferring unresolvable property (will resolve at execution time)",
				"property", uri.PropertyPath(), "ksuid", uri.KSUID())
		}
		res.AddAnswer(uri.KSUID(), uri.PropertyPath(), answer)
	}

	return res, nil
}

// classifySourceProperty answers whether propertyPath on the resource named
// by ksuid has a value available at plan time, following a chain of
// references recursively when the value is itself a non-opaque reference.
//
// visiting is the ordered chain of hops currently being classified, used both
// to detect a cycle (linear membership check; chains here are short) and, on
// a cycle, as the error's Chain.
func classifySourceProperty(ksuid, propertyPath string, resourcesByKsuid map[string]*pkgmodel.Resource, effective map[string]json.RawMessage, visiting []string) (SourceAnswer, error) {
	answer, err := classifySourcePropertyValue(ksuid, propertyPath, resourcesByKsuid, effective, visiting)
	if err != nil {
		return answer, err
	}
	decorateOpacity(&answer, ksuid, propertyPath, resourcesByKsuid, effective)
	return answer, nil
}

// decorateOpacity attaches the opacity flag and the source root digest to an
// already-classified answer, never altering its Kind or Value: opacity
// metadata is additive so every pre-existing resolution pin holds.
func decorateOpacity(answer *SourceAnswer, ksuid, propertyPath string, resourcesByKsuid map[string]*pkgmodel.Resource, effective map[string]json.RawMessage) {
	if answer.Opaque {
		return // a chain hop already propagated richer metadata
	}
	target := resourcesByKsuid[ksuid]
	if target == nil {
		return
	}
	opaque := isSourcePropertyOpaque(target, propertyPath)

	if effDoc, declared := effective[ksuid]; declared {
		effVal := gjson.GetBytes(effDoc, propertyPath)
		if effVal.Exists() && !isReferenceEnvelope(effVal) {
			if !opaque {
				opaque = containsHashedValue(effVal) || containsOpaqueVisibility(effVal)
			}
			if opaque {
				answer.Opaque = true
				unwrapped := provenance.UnwrapEffectiveValue(effVal)
				switch {
				case effVal.Get("$hashed").Bool():
					// A re-applied extract round-trip: the declared value IS
					// the at-rest digest.
					answer.SourceRootDigest = provenance.FromStored(unwrapped.String())
				case unwrapped.Type == gjson.String:
					answer.SourceRootDigest = provenance.DigestOfString(unwrapped.String())
					answer.sourceRaw = unwrapped.Raw
				default:
					answer.SourceRootDigest = provenance.DigestOfJSON(unwrapped.Raw)
					answer.sourceRaw = unwrapped.Raw
				}
				return
			}
		}
	}

	if !opaque {
		return
	}
	answer.Opaque = true
	// Undeclared (or declared as a chain that did not decorate): the stored
	// at-rest digest is the only comparable record.
	answer.SourceRootDigest = storedRootDigest(target, propertyPath)
}

// storedRootDigest adapts the persisted at-rest digest of an opaque property
// into the canonical domain, or "" when none is stored (including the
// documented legacy gap: an empty value is never hashed at rest).
func storedRootDigest(target *pkgmodel.Resource, propertyPath string) string {
	for _, props := range [][]byte{target.Properties, target.ReadOnlyProperties} {
		if props == nil {
			continue
		}
		v := gjson.GetBytes(props, propertyPath)
		if v.Exists() && v.Get("$hashed").Bool() {
			return provenance.FromStored(v.Get("$value").String())
		}
	}
	return ""
}

func classifySourcePropertyValue(ksuid, propertyPath string, resourcesByKsuid map[string]*pkgmodel.Resource, effective map[string]json.RawMessage, visiting []string) (SourceAnswer, error) {
	key := ksuid + "#/" + propertyPath
	for _, v := range visiting {
		if v == key {
			return SourceAnswer{}, ReferenceCycleError{Chain: append(append([]string{}, visiting...), key)}
		}
	}
	visiting = append(visiting, key)

	targetResource, exists := resourcesByKsuid[ksuid]
	if !exists {
		return SourceAnswer{}, fmt.Errorf("resource with KSUID %s not found", ksuid)
	}

	if effDoc, declared := effective[ksuid]; declared {
		effVal := gjson.GetBytes(effDoc, propertyPath)
		if effVal.Exists() {
			refused := containsHashedValue(effVal) || containsOpaqueVisibility(effVal) ||
				isSourcePropertyOpaque(targetResource, propertyPath)
			if !refused && !isReferenceEnvelope(effVal) {
				return SourceAnswer{Kind: AnswerResolved, Value: ExtractPropertyValue(effVal)}, nil
			}
			if !refused && isReferenceEnvelope(effVal) {
				nested := pkgmodel.FormaeURI(effVal.Get("$ref").String())
				if nested != "" && nested.KSUID() != "" {
					sub, err := classifySourceProperty(nested.KSUID(), nested.PropertyPath(), resourcesByKsuid, effective, visiting)
					if err != nil {
						return SourceAnswer{}, err
					}
					if sub.Kind == AnswerDeferred {
						// Propagate the chain hop's opacity metadata: this
						// occurrence's movement follows the chain root. A hop
						// carrying its own $json extraction derives its value
						// from the root; extract in memory when possible so
						// the digest matches the hop's actual property value.
						hop := SourceAnswer{Kind: AnswerDeferred, Opaque: sub.Opaque,
							SourceRootDigest: sub.SourceRootDigest, sourceRaw: sub.sourceRaw}
						if sub.Opaque {
							if jsonPath := effVal.Get("$json").String(); jsonPath != "" {
								if sub.sourceRaw != "" {
									if extracted, jerr := ExtractJSONPath(sub.sourceRaw, jsonPath); jerr == nil {
										hop.SourceRootDigest = provenance.DigestOfString(extracted)
										hop.sourceRaw = extracted
									} else {
										hop.SourceRootDigest = ""
										hop.sourceRaw = ""
									}
								} else {
									// Root value not in memory: the extracted
									// hop value is underivable.
									hop.SourceRootDigest = ""
								}
							}
						}
						return hop, nil
					}
					if sub.Kind == AnswerResolved && !sub.Opaque {
						value := sub.Value
						derivable := true
						if jsonPath := effVal.Get("$json").String(); jsonPath != "" {
							extracted, jerr := ExtractJSONPath(value, jsonPath)
							if jerr != nil {
								derivable = false // underivable extraction: fall through, execution resolves live
							} else {
								value = extracted
							}
						}
						if derivable {
							return SourceAnswer{Kind: AnswerResolved, Value: value}, nil
						}
					}
					// AnswerStable (transitive source unmoved): the cached value on
					// this hop's persisted envelope is the last applied resolution
					// and remains valid. Fall through to the persisted path, which
					// answers exactly that (or defers for a value-less envelope),
					// preserving prior behavior. Resolved-but-opaque and an
					// underivable extraction fall through the same way.
				}
			}
			// refused shapes fall through to the persisted path unchanged
		}
	}

	if value, ok := resolvableValueFrom(targetResource.ReadOnlyProperties, propertyPath); ok {
		return SourceAnswer{Kind: AnswerStable, Value: value}, nil
	}
	if value, ok := resolvableValueFrom(targetResource.Properties, propertyPath); ok {
		return SourceAnswer{Kind: AnswerStable, Value: value}, nil
	}
	return SourceAnswer{Kind: AnswerDeferred}, nil
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
