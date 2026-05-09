// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package target_update

import (
	"encoding/json"

	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ConfigChangeType classifies the nature of a target config change.
type ConfigChangeType int

const (
	// ConfigNoChange means the configs are identical.
	ConfigNoChange ConfigChangeType = iota
	// ConfigMutableChange means only mutable (non-createOnly) fields changed.
	ConfigMutableChange
	// ConfigImmutableChange means at least one immutable (createOnly) field changed.
	ConfigImmutableChange
)

// ClassifyConfigChange compares two target configs field-by-field using the
// ConfigSchema to determine if the change is mutable, immutable, or absent.
//
// When schema has no hints (backwards compat), any change is treated as immutable.
// Fields without a hint in the schema are treated as immutable (createOnly=true).
func ClassifyConfigChange(existing, new json.RawMessage, schema pkgmodel.ConfigSchema) ConfigChangeType {
	// Drop cached $value entries up front so a config with $ref+$value compares
	// equal to one with only $ref. Without this, the early-return branches below
	// would treat a cache-only diff as a real change.
	existing = stripResolvableValuesRaw(existing)
	new = stripResolvableValuesRaw(new)

	if util.JsonEqualRaw(existing, new) {
		return ConfigNoChange
	}

	if len(schema.Hints) == 0 {
		return ConfigImmutableChange
	}

	var existingMap, newMap map[string]json.RawMessage
	if err := json.Unmarshal(existing, &existingMap); err != nil {
		return ConfigImmutableChange
	}
	if err := json.Unmarshal(new, &newMap); err != nil {
		return ConfigImmutableChange
	}

	allKeys := make(map[string]bool)
	for k := range existingMap {
		allKeys[k] = true
	}
	for k := range newMap {
		allKeys[k] = true
	}

	hasChange := false
	for key := range allKeys {
		existingVal, existingOK := existingMap[key]
		newVal, newOK := newMap[key]

		if existingOK && newOK && jsonBytesEqual(existingVal, newVal) {
			continue
		}

		hasChange = true

		hint, hintExists := schema.Hints[key]
		if !hintExists || hint.CreateOnly {
			return ConfigImmutableChange
		}
	}

	if hasChange {
		return ConfigMutableChange
	}
	return ConfigNoChange
}

func jsonBytesEqual(a, b json.RawMessage) bool {
	var aVal, bVal any
	if err := json.Unmarshal(a, &aVal); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bVal); err != nil {
		return false
	}
	// Strip $value from resolvables so that resolved vs unresolved
	// configs compare equal when the $ref is the same.
	stripResolvableValues(aVal)
	stripResolvableValues(bVal)
	aBytes, _ := json.Marshal(aVal)
	bBytes, _ := json.Marshal(bVal)
	return string(aBytes) == string(bBytes)
}

// stripResolvableValuesRaw returns a copy of raw with $value stripped from
// every object that also carries a $ref. If raw is not valid JSON, it is
// returned unchanged.
func stripResolvableValuesRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	stripResolvableValues(v)
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

// stripResolvableValues recursively removes $value from any object that
// contains a $ref key. This ensures that a resolved config (with cached
// $value) compares equal to an unresolved config (only $ref).
func stripResolvableValues(v any) {
	switch val := v.(type) {
	case map[string]any:
		if _, hasRef := val["$ref"]; hasRef {
			delete(val, "$value")
		}
		for _, child := range val {
			stripResolvableValues(child)
		}
	case []any:
		for _, item := range val {
			stripResolvableValues(item)
		}
	}
}
