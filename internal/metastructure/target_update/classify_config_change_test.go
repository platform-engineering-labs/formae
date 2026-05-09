// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package target_update

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestClassifyConfigChange_NoChange(t *testing.T) {
	existing := json.RawMessage(`{"Region":"us-east-1","Profile":"prod"}`)
	new := json.RawMessage(`{"Region":"us-east-1","Profile":"prod"}`)
	schema := pkgmodel.ConfigSchema{
		Hints: map[string]pkgmodel.ConfigFieldHint{
			"Region":  {CreateOnly: true},
			"Profile": {CreateOnly: false},
		},
	}

	result := ClassifyConfigChange(existing, new, schema)
	assert.Equal(t, ConfigNoChange, result)
}

func TestClassifyConfigChange_MutableFieldOnly(t *testing.T) {
	existing := json.RawMessage(`{"Region":"us-east-1","Profile":"prod"}`)
	new := json.RawMessage(`{"Region":"us-east-1","Profile":"staging"}`)
	schema := pkgmodel.ConfigSchema{
		Hints: map[string]pkgmodel.ConfigFieldHint{
			"Region":  {CreateOnly: true},
			"Profile": {CreateOnly: false},
		},
	}

	result := ClassifyConfigChange(existing, new, schema)
	assert.Equal(t, ConfigMutableChange, result)
}

func TestClassifyConfigChange_ImmutableFieldChanged(t *testing.T) {
	existing := json.RawMessage(`{"Region":"us-east-1","Profile":"prod"}`)
	new := json.RawMessage(`{"Region":"us-west-2","Profile":"prod"}`)
	schema := pkgmodel.ConfigSchema{
		Hints: map[string]pkgmodel.ConfigFieldHint{
			"Region":  {CreateOnly: true},
			"Profile": {CreateOnly: false},
		},
	}

	result := ClassifyConfigChange(existing, new, schema)
	assert.Equal(t, ConfigImmutableChange, result)
}

func TestClassifyConfigChange_MixedChange_ImmutableDominates(t *testing.T) {
	existing := json.RawMessage(`{"Region":"us-east-1","Profile":"prod"}`)
	new := json.RawMessage(`{"Region":"us-west-2","Profile":"staging"}`)
	schema := pkgmodel.ConfigSchema{
		Hints: map[string]pkgmodel.ConfigFieldHint{
			"Region":  {CreateOnly: true},
			"Profile": {CreateOnly: false},
		},
	}

	result := ClassifyConfigChange(existing, new, schema)
	assert.Equal(t, ConfigImmutableChange, result)
}

func TestClassifyConfigChange_NilSchema_AnyChange_IsImmutable(t *testing.T) {
	existing := json.RawMessage(`{"Region":"us-east-1"}`)
	new := json.RawMessage(`{"Region":"us-west-2"}`)

	result := ClassifyConfigChange(existing, new, pkgmodel.ConfigSchema{})
	assert.Equal(t, ConfigImmutableChange, result)
}

func TestClassifyConfigChange_NilSchema_NoChange(t *testing.T) {
	existing := json.RawMessage(`{"Region":"us-east-1"}`)
	new := json.RawMessage(`{"Region":"us-east-1"}`)

	result := ClassifyConfigChange(existing, new, pkgmodel.ConfigSchema{})
	assert.Equal(t, ConfigNoChange, result)
}

func TestClassifyConfigChange_FieldWithoutHint_TreatedAsImmutable(t *testing.T) {
	existing := json.RawMessage(`{"Region":"us-east-1","NewField":"a"}`)
	new := json.RawMessage(`{"Region":"us-east-1","NewField":"b"}`)
	schema := pkgmodel.ConfigSchema{
		Hints: map[string]pkgmodel.ConfigFieldHint{
			"Region": {CreateOnly: true},
			// NewField has no hint — treated as immutable
		},
	}

	result := ClassifyConfigChange(existing, new, schema)
	assert.Equal(t, ConfigImmutableChange, result)
}

func TestClassifyConfigChange_EmptySchema_AnyChange_IsImmutable(t *testing.T) {
	existing := json.RawMessage(`{"Region":"us-east-1"}`)
	new := json.RawMessage(`{"Region":"us-west-2"}`)
	schema := pkgmodel.ConfigSchema{
		Hints: map[string]pkgmodel.ConfigFieldHint{},
	}

	result := ClassifyConfigChange(existing, new, schema)
	assert.Equal(t, ConfigImmutableChange, result)
}

func TestClassifyConfigChange_FieldAdded(t *testing.T) {
	existing := json.RawMessage(`{"Region":"us-east-1"}`)
	new := json.RawMessage(`{"Region":"us-east-1","Profile":"prod"}`)
	schema := pkgmodel.ConfigSchema{
		Hints: map[string]pkgmodel.ConfigFieldHint{
			"Region":  {CreateOnly: true},
			"Profile": {CreateOnly: false},
		},
	}

	result := ClassifyConfigChange(existing, new, schema)
	assert.Equal(t, ConfigMutableChange, result)
}

func TestClassifyConfigChange_ResolvableValueIgnored(t *testing.T) {
	// Stored config has resolved $value, new config only has $ref — should be equal
	existing := json.RawMessage(`{
		"Auth": {"Type":"EKS","Endpoint":{"$ref":"formae://abc#/Endpoint","$value":"https://cluster.eks.amazonaws.com"},"ClusterName":{"$ref":"formae://abc#/Name","$value":"my-cluster"}},
		"HasLoadBalancer": false
	}`)
	new := json.RawMessage(`{
		"Auth": {"Type":"EKS","Endpoint":{"$ref":"formae://abc#/Endpoint"},"ClusterName":{"$ref":"formae://abc#/Name"}},
		"HasLoadBalancer": true
	}`)
	schema := pkgmodel.ConfigSchema{
		Hints: map[string]pkgmodel.ConfigFieldHint{
			"Auth":            {CreateOnly: true},
			"HasLoadBalancer": {CreateOnly: false},
		},
	}

	result := ClassifyConfigChange(existing, new, schema)
	// Auth is unchanged (same $ref), HasLoadBalancer is mutable — so MutableChange, not ImmutableChange
	assert.Equal(t, ConfigMutableChange, result)
}

func TestClassifyConfigChange_ResolvableValueIgnored_NoHints(t *testing.T) {
	// Stored config has resolved $value, new config only has $ref, and schema
	// carries no hints. The sole diff is the cached $value, so the result
	// should be NoChange — not ImmutableChange from the empty-hints early return.
	existing := json.RawMessage(`{"Auth": {"Endpoint":{"$ref":"formae://abc#/Endpoint","$value":"https://cluster.eks.amazonaws.com"}}}`)
	new := json.RawMessage(`{"Auth": {"Endpoint":{"$ref":"formae://abc#/Endpoint"}}}`)

	result := ClassifyConfigChange(existing, new, pkgmodel.ConfigSchema{})
	assert.Equal(t, ConfigNoChange, result)
}

func TestClassifyConfigChange_ResolvableDifferentRef_IsImmutable(t *testing.T) {
	// Different $ref means genuinely different config
	existing := json.RawMessage(`{"Auth": {"Endpoint":{"$ref":"formae://abc#/Endpoint","$value":"https://old.com"}}}`)
	new := json.RawMessage(`{"Auth": {"Endpoint":{"$ref":"formae://xyz#/Endpoint"}}}`)
	schema := pkgmodel.ConfigSchema{
		Hints: map[string]pkgmodel.ConfigFieldHint{
			"Auth": {CreateOnly: true},
		},
	}

	result := ClassifyConfigChange(existing, new, schema)
	assert.Equal(t, ConfigImmutableChange, result)
}

func TestClassifyConfigChange_FieldRemoved(t *testing.T) {
	existing := json.RawMessage(`{"Region":"us-east-1","Profile":"prod"}`)
	new := json.RawMessage(`{"Region":"us-east-1"}`)
	schema := pkgmodel.ConfigSchema{
		Hints: map[string]pkgmodel.ConfigFieldHint{
			"Region":  {CreateOnly: true},
			"Profile": {CreateOnly: false},
		},
	}

	result := ClassifyConfigChange(existing, new, schema)
	assert.Equal(t, ConfigMutableChange, result)
}
