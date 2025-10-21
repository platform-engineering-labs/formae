// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resolver

import (
	"fmt"

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

func LoadResolvablePropertiesFromStacks(resource pkgmodel.Resource, everything []*pkgmodel.Forma) (ResolvableProperties, error) {
	res := NewResolvableProperties()

	resourcesByKsuid := make(map[string]pkgmodel.Resource)
	for _, f := range everything {
		for _, r := range f.Resources {
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

		if targetResource.ReadOnlyProperties != nil {
			extracted := gjson.GetBytes(targetResource.ReadOnlyProperties, propertyPath)
			if extracted.Exists() {
				res.Add(ksuid, propertyPath, extracted.String())
				continue
			}
		}

		if targetResource.Properties != nil {
			extracted := gjson.GetBytes(targetResource.Properties, propertyPath)
			if extracted.Exists() {
				res.Add(ksuid, propertyPath, extracted.String())
				continue
			}
		}

		return res, fmt.Errorf("property %s not found in resource %s (KSUID: %s)", propertyPath, targetResource.Label, ksuid)
	}

	return res, nil
}
