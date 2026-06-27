// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// A Route53 RecordSet whose ResourceRecords is sourced from a list-valued
// resolvable (an ACM Certificate's ValidationRecords[0].Values) must reconcile
// to a no-op once applied. This drives the real factory path so that
// ConvertToPluginFormat runs on both sides: the existing side carries a cached
// $value and is unwrapped to a native array, while the desired side keeps its
// $ref envelope (its $value was not cached) and is resolved from
// ResolvableProperties — which stores the list as raw JSON text. Without
// representation normalization in the patch path the two sides diff forever.
func TestNewResourceUpdateForExisting_ListResolvableMatchingLiveValue_NoUpdate(t *testing.T) {
	certKsuid := util.NewID()
	recordKsuid := util.NewID()
	uri := "formae://" + certKsuid + "#/ValidationRecords.0.Values"

	schema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Type", "ResourceRecords"},
	}

	// Existing (persisted) side: the resolvable carries its cached resolved
	// value, so ConvertToPluginFormat unwraps it to a native array.
	existing := pkgmodel.Resource{
		Ksuid:    recordKsuid,
		Label:    "agent-bridge-cert-validation",
		Type:     "AWS::Route53::RecordSet",
		Stack:    "infrastructure",
		Target:   "test-target",
		NativeID: "Z123_record",
		Managed:  true,
		Schema:   schema,
		Properties: json.RawMessage(`{
			"Name": "_7a296.example.com",
			"Type": "CNAME",
			"ResourceRecords": {"$ref": "` + uri + `", "$value": ["_7a296.acm-validations.aws"]}
		}`),
	}

	// Desired side: the same reference, but with no cached $value — the envelope
	// survives ConvertToPluginFormat and is resolved from ResolvableProperties.
	newResource := existing
	newResource.Properties = json.RawMessage(`{
		"Name": "_7a296.example.com",
		"Type": "CNAME",
		"ResourceRecords": {"$ref": "` + uri + `"}
	}`)

	resolvableProps := resolver.NewResolvableProperties()
	resolvableProps.Add(certKsuid, "ValidationRecords.0.Values", `["_7a296.acm-validations.aws"]`)

	target := pkgmodel.Target{Label: "test-target", Namespace: "aws", Config: json.RawMessage(`{}`)}

	updates, err := NewResourceUpdateForExisting(resolvableProps, existing, newResource, target, target,
		pkgmodel.FormaApplyModeReconcile, FormaCommandSourceUser)
	require.NoError(t, err)
	assert.Empty(t, updates, "an unchanged list-valued resolvable must reconcile to a no-op")
}
