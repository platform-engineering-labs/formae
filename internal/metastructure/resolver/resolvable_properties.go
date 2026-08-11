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

// ResolvableProperties is a map of KSUIDs to property names to values
// This can include resource.Properties and resource.ReadOnlyProperties
type ResolvableProperties struct {
	props map[string]map[string]string // ksuid -> property -> value
}

func NewResolvableProperties() ResolvableProperties {
	return ResolvableProperties{
		props: make(map[string]map[string]string),
	}
}

func (p *ResolvableProperties) Add(ksuid, property, value string) {
	if _, ok := p.props[ksuid]; !ok {
		p.props[ksuid] = make(map[string]string)
	}
	p.props[ksuid][property] = value
}

func (p *ResolvableProperties) Get(ksuid, property string) (string, bool) {
	if resourceProps, ok := p.props[ksuid]; ok {
		if value, ok := resourceProps[property]; ok {
			return value, true
		}
	}
	return "", false
}

func LoadResolvablePropertiesFromStacks(resource pkgmodel.Resource, allResources map[string][]*pkgmodel.Resource) (ResolvableProperties, error) {
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
// A value stored hashed at rest is a SHA-256 digest, not the value the source
// holds, so it can never stand in for a resolution: it would be compared, and
// on a $json reference parsed, as if it were the live value. Refusing it here
// leaves the reference to execution-time resolution (RemainingResolvables),
// which reads the source live through its plugin — the same way the create path
// resolves it, and the only way a secret is ever read.
func resolvableValueFrom(properties json.RawMessage, propertyPath string) (string, bool) {
	if properties == nil {
		return "", false
	}
	extracted := gjson.GetBytes(properties, propertyPath)
	if !extracted.Exists() {
		return "", false
	}
	if extracted.Get("$hashed").Bool() {
		return "", false
	}
	return ExtractPropertyValue(extracted), true
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
