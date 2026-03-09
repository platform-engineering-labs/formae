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
	if util.JsonEqualRaw(existing, new) {
		return ConfigNoChange
	}

	// No schema — all changes are immutable (safe default)
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

	hasChange := false

	// Check all keys in both maps
	allKeys := make(map[string]bool)
	for k := range existingMap {
		allKeys[k] = true
	}
	for k := range newMap {
		allKeys[k] = true
	}

	for key := range allKeys {
		existingVal, existingOK := existingMap[key]
		newVal, newOK := newMap[key]

		// Field unchanged
		if existingOK && newOK && jsonBytesEqual(existingVal, newVal) {
			continue
		}

		// Field changed (added, removed, or value differs)
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

// jsonBytesEqual compares two json.RawMessage values for semantic equality.
func jsonBytesEqual(a, b json.RawMessage) bool {
	var aVal, bVal any
	if err := json.Unmarshal(a, &aVal); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bVal); err != nil {
		return false
	}
	aBytes, _ := json.Marshal(aVal)
	bBytes, _ := json.Marshal(bVal)
	return string(aBytes) == string(bBytes)
}
