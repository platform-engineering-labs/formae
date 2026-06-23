// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"testing"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

// A surfaced writeOnly field is echoed back by some providers as a bare cleartext
// string in the Read result. It must not be persisted into Properties or
// ReadOnlyProperties; the managed (hashed) value in desired Properties is kept.
func TestUpdateProperties_DropsWriteOnlyFromReadBack(t *testing.T) {
	ru := &ResourceUpdate{
		DesiredState: pkgmodel.Resource{
			Properties: json.RawMessage(`{"Name":"n","SecretString":{"$value":"deadbeefhash","$visibility":"Opaque"}}`),
			Schema: pkgmodel.Schema{
				Fields: []string{"Name", "SecretString"},
				Hints: map[string]pkgmodel.FieldHint{
					"SecretString": {WriteOnly: true},
				},
			},
		},
	}

	// Provider Read returns the writeOnly value as bare cleartext, plus a real read-only field.
	readBack := `{"Name":"n","SecretString":"super-secret-cleartext","Arn":"arn:aws:secretsmanager:::secret:x"}`
	require.NoError(t, ru.updateResourceProperties(readBack))

	props := string(ru.DesiredState.Properties)
	ro := string(ru.DesiredState.ReadOnlyProperties)

	require.NotContains(t, ro, "super-secret-cleartext", "cleartext must not land in ReadOnlyProperties")
	require.NotContains(t, ro, "SecretString", "writeOnly field must not appear in ReadOnlyProperties")
	require.NotContains(t, props, "super-secret-cleartext", "provider read-back cleartext must not overwrite the managed value")
	require.Contains(t, props, "deadbeefhash", "managed hashed value is preserved")
	require.Contains(t, ro, "Arn", "genuine read-only fields are still captured")
}
