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

func TestClassifyConfigChange_EmptySchema_AnyChange_IsImmutable_ZeroValue(t *testing.T) {
	existing := json.RawMessage(`{"Region":"us-east-1"}`)
	new := json.RawMessage(`{"Region":"us-west-2"}`)

	result := ClassifyConfigChange(existing, new, pkgmodel.ConfigSchema{})
	assert.Equal(t, ConfigImmutableChange, result)
}

func TestClassifyConfigChange_EmptySchema_NoChange_ZeroValue(t *testing.T) {
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
